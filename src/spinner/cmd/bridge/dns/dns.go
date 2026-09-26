// Package dns adapts authoritative DNS queries to the Gateway protocol
// planner. Go owns DNS parsing and response encoding; PHP owns endpoint and
// subscriber resolution and returns either an immediate response or an HTTP
// reply destination whose response body describes DNS records.
package dns

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	mdns "github.com/miekg/dns"
	"golang.org/x/time/rate"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

var ErrServerClosed = errors.New("DNS server closed")

const (
	defaultPlannerTimeout   = 750 * time.Millisecond
	defaultReadTimeout      = 2 * time.Second
	defaultWriteTimeout     = 2 * time.Second
	defaultMaxResponseItems = 16
	defaultRateLimit        = 5000
	defaultRateBurst        = 10000
	maxSafeUDPSize          = 1232
)

type Config struct {
	Address            string
	Zone               string
	Nameservers        []string
	SOAEmail           string
	TTL                uint32
	PlannerTimeout     time.Duration
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	MaxResponseRecords int
	RateLimit          int
	RateBurst          int
}

type Executor interface {
	Deliver(context.Context, bridge.Delivery) (bridge.Response, error)
	EnqueueGateway(bridge.GatewayDelivery) bool
}

type Server struct {
	config   Config
	planner  bridge.ProtocolPlanner
	executor Executor
	udp      *mdns.Server
	tcp      *mdns.Server
	limiter  *rate.Limiter

	mu      sync.Mutex
	started bool
	closing bool
}

func NewServer(config Config, planner bridge.ProtocolPlanner, executor Executor) (*Server, error) {
	applyDefaults(&config)
	if strings.TrimSpace(config.Address) == "" {
		return nil, fmt.Errorf("DNS listen address is required")
	}
	if strings.TrimSpace(config.Zone) == "" {
		return nil, fmt.Errorf("DNS authoritative zone is required")
	}
	if planner == nil {
		return nil, fmt.Errorf("DNS protocol planner is required")
	}
	if executor == nil {
		return nil, fmt.Errorf("DNS gateway executor is required")
	}
	if config.PlannerTimeout <= 0 || config.ReadTimeout <= 0 || config.WriteTimeout <= 0 {
		return nil, fmt.Errorf("DNS timeouts must be positive")
	}
	if config.MaxResponseRecords < 1 {
		return nil, fmt.Errorf("DNS maximum response records must be positive")
	}
	if config.RateLimit < 0 || config.RateBurst < 0 || (config.RateLimit > 0 && config.RateBurst < 1) {
		return nil, fmt.Errorf("DNS rate limit and burst settings are invalid")
	}

	config.Zone = canonicalName(config.Zone)
	if _, ok := mdns.IsDomainName(config.Zone); !ok {
		return nil, fmt.Errorf("invalid DNS authoritative zone %q", config.Zone)
	}
	if len(config.Nameservers) == 0 {
		return nil, fmt.Errorf("at least one authoritative DNS nameserver is required")
	}
	for index := range config.Nameservers {
		config.Nameservers[index] = canonicalName(config.Nameservers[index])
		if _, ok := mdns.IsDomainName(config.Nameservers[index]); !ok {
			return nil, fmt.Errorf("invalid DNS nameserver %q", config.Nameservers[index])
		}
	}
	config.SOAEmail = canonicalName(config.SOAEmail)
	if _, ok := mdns.IsDomainName(config.SOAEmail); !ok {
		return nil, fmt.Errorf("invalid DNS SOA email %q", config.SOAEmail)
	}

	server := &Server{config: config, planner: planner, executor: executor}
	if config.RateLimit > 0 {
		server.limiter = rate.NewLimiter(rate.Limit(config.RateLimit), config.RateBurst)
	}
	handler := mdns.HandlerFunc(server.ServeDNS)
	server.udp = &mdns.Server{
		Net:          "udp",
		Handler:      handler,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		UDPSize:      maxSafeUDPSize,
	}
	server.tcp = &mdns.Server{
		Net:          "tcp",
		Handler:      handler,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
	}

	return server, nil
}

func (s *Server) ListenAndServe() error {
	if s == nil || s.udp == nil || s.tcp == nil {
		return fmt.Errorf("DNS server is not initialized")
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("DNS server is already started")
	}
	s.started = true
	s.closing = false
	s.mu.Unlock()

	udpAddress, err := net.ResolveUDPAddr("udp", s.config.Address)
	if err != nil {
		s.markStopped()
		return fmt.Errorf("resolve DNS UDP address: %w", err)
	}
	udpConnection, err := net.ListenUDP("udp", udpAddress)
	if err != nil {
		s.markStopped()
		return fmt.Errorf("listen for DNS over UDP: %w", err)
	}

	tcpAddress, err := net.ResolveTCPAddr("tcp", s.config.Address)
	if err != nil {
		_ = udpConnection.Close()
		s.markStopped()
		return fmt.Errorf("resolve DNS TCP address: %w", err)
	}
	tcpListener, err := net.ListenTCP("tcp", tcpAddress)
	if err != nil {
		_ = udpConnection.Close()
		s.markStopped()
		return fmt.Errorf("listen for DNS over TCP: %w", err)
	}

	s.udp.PacketConn = udpConnection
	s.tcp.Listener = tcpListener
	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- s.udp.ActivateAndServe() }()
	go func() { errorsChannel <- s.tcp.ActivateAndServe() }()

	first := <-errorsChannel
	if !s.isClosing() {
		_ = s.shutdown(context.Background())
	}
	second := <-errorsChannel
	closing := s.markStopped()

	if closing {
		return ErrServerClosed
	}
	if first != nil {
		return first
	}
	return second
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	s.mu.Unlock()
	return s.shutdown(ctx)
}

func (s *Server) shutdown(ctx context.Context) error {
	udpErr := s.udp.ShutdownContext(ctx)
	tcpErr := s.tcp.ShutdownContext(ctx)
	return errors.Join(udpErr, tcpErr)
}

func (s *Server) isClosing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

func (s *Server) markStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	closing := s.closing
	s.started = false
	return closing
}

func (s *Server) ServeDNS(writer mdns.ResponseWriter, request *mdns.Msg) {
	if s.limiter != nil && !s.limiter.Allow() {
		return
	}
	if request == nil {
		return
	}
	response := new(mdns.Msg)
	response.SetReply(request)
	response.Authoritative = true
	response.RecursionAvailable = false

	if request.Opcode != mdns.OpcodeQuery || len(request.Question) != 1 {
		response.Rcode = mdns.RcodeFormatError
		s.writeResponse(writer, request, response)
		return
	}

	question := request.Question[0]
	queryName := canonicalName(question.Name)
	if question.Qclass != mdns.ClassINET {
		response.Rcode = mdns.RcodeNotImplemented
		s.writeResponse(writer, request, response)
		return
	}
	if !mdns.IsSubDomain(s.config.Zone, queryName) {
		response.Rcode = mdns.RcodeRefused
		s.writeResponse(writer, request, response)
		return
	}
	if queryName == s.config.Zone {
		s.answerAuthority(response, question)
		s.writeResponse(writer, request, response)
		return
	}
	if question.Qtype == mdns.TypeANY {
		response.Rcode = mdns.RcodeRefused
		s.writeResponse(writer, request, response)
		return
	}

	queryLabels := mdns.SplitDomainName(question.Name)
	zoneLabels := mdns.SplitDomainName(s.config.Zone)
	labels := queryLabels[:len(queryLabels)-len(zoneLabels)]
	if len(labels) == 0 {
		response.Rcode = mdns.RcodeNameError
		s.addSOA(response)
		s.writeResponse(writer, request, response)
		return
	}
	endpoint := strings.ToLower(labels[len(labels)-1])
	dataLabels := labels[:len(labels)-1]
	payload, err := json.Marshal(Query{
		Name:     question.Name,
		Type:     typeName(question.Qtype),
		Class:    className(question.Qclass),
		Endpoint: endpoint,
		Labels:   dataLabels,
		Data:     strings.Join(dataLabels, "."),
	})
	if err != nil {
		response.Rcode = mdns.RcodeServerFailure
		s.writeResponse(writer, request, response)
		return
	}

	event := bridge.GatewayEvent{
		ID:       queryID(writer, request, question),
		Protocol: bridge.ProtocolDNS,
		Kind:     bridge.EventQuery,
		Host:     question.Name,
		Route:    "/" + endpoint,
		Headers:  map[string][]string{"Content-Type": {"application/json"}},
		Attributes: map[string]string{
			"qname":       question.Name,
			"qtype":       typeName(question.Qtype),
			"qclass":      className(question.Qclass),
			"query_id":    fmt.Sprintf("%d", request.Id),
			"zone":        s.config.Zone,
			"endpoint":    endpoint,
			"data":        strings.Join(dataLabels, "."),
			"source_addr": remoteAddress(writer),
			"transport":   transport(writer),
		},
		Payload: payload,
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.config.PlannerTimeout)
	defer cancel()
	decision, planErr := s.planner.PlanEvent(ctx, event)
	if planErr != nil || decision.Validate() != nil || decision.Protocol != bridge.ProtocolDNS {
		response.Rcode = mdns.RcodeServerFailure
		s.writeResponse(writer, request, response)
		return
	}

	answerPayload, decisionErr := s.executeDecision(ctx, decision, payload)
	if decisionErr != nil {
		response.Rcode = mdns.RcodeServerFailure
		s.writeResponse(writer, request, response)
		return
	}

	switch decision.Action {
	case bridge.GatewayReject:
		response.Rcode = mdns.RcodeNameError
		s.addSOA(response)
	case bridge.GatewayClose:
		response.Rcode = mdns.RcodeRefused
	case bridge.GatewayAccept, bridge.GatewayDeliver, bridge.GatewayRespond:
		answers, parseErr := parseAnswers(question, answerPayload, s.config.TTL, s.config.MaxResponseRecords)
		if parseErr != nil {
			response.Rcode = mdns.RcodeServerFailure
			break
		}
		response.Answer = answers
		if len(answers) == 0 {
			s.addSOA(response)
		}
	default:
		response.Rcode = mdns.RcodeServerFailure
	}

	s.writeResponse(writer, request, response)
}

type Query struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Class    string   `json:"class"`
	Endpoint string   `json:"endpoint"`
	Labels   []string `json:"labels"`
	Data     string   `json:"data"`
}

func (s *Server) executeDecision(ctx context.Context, decision bridge.GatewayDecision, fallbackPayload []byte) ([]byte, error) {
	var answerPayload []byte
	if decision.Reply == nil {
		answerPayload = append([]byte(nil), decision.Payload...)
	} else {
		delivery, err := httpDelivery(*decision.Reply, fallbackPayload)
		if err != nil {
			return nil, err
		}
		reply, err := s.executor.Deliver(ctx, delivery)
		if err != nil {
			return nil, err
		}
		if reply.StatusCode < http.StatusOK || reply.StatusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("DNS reply subscriber returned HTTP status %d", reply.StatusCode)
		}
		answerPayload = append([]byte(nil), reply.Body...)
	}

	for index := range decision.Deliveries {
		delivery := decision.Deliveries[index]
		if len(delivery.Payload) == 0 {
			delivery.Payload = append([]byte(nil), fallbackPayload...)
		}
		if !s.executor.EnqueueGateway(delivery) {
			return nil, fmt.Errorf("DNS relay queue unavailable")
		}
	}

	return answerPayload, nil
}

func httpDelivery(delivery bridge.GatewayDelivery, fallbackPayload []byte) (bridge.Delivery, error) {
	if delivery.Protocol != bridge.ProtocolHTTP {
		return bridge.Delivery{}, fmt.Errorf("DNS reply destination must use HTTP")
	}
	method := delivery.Attributes["method"]
	if method == "" {
		method = http.MethodPost
	}
	payload := delivery.Payload
	if len(payload) == 0 {
		payload = fallbackPayload
	}
	converted := bridge.Delivery{
		URL:          delivery.Target,
		Method:       method,
		RawQuery:     delivery.Attributes["raw_query"],
		Headers:      delivery.Headers,
		Body:         append([]byte(nil), payload...),
		SubscriberID: delivery.SubscriberID,
	}
	if err := converted.Validate(); err != nil {
		return bridge.Delivery{}, err
	}
	return converted, nil
}

type answer struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

func parseAnswers(question mdns.Question, payload []byte, ttl uint32, limit int) ([]mdns.RR, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var items []answer
	if err := decoder.Decode(&items); err != nil {
		return nil, fmt.Errorf("decode DNS response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("DNS response contains trailing JSON")
	}
	if len(items) > limit {
		return nil, fmt.Errorf("DNS response contains more than %d records", limit)
	}

	direct := make([]mdns.RR, 0, len(items))
	aliases := make([]mdns.RR, 0, 1)
	for _, item := range items {
		header := mdns.RR_Header{Name: question.Name, Class: mdns.ClassINET, Ttl: ttl}
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "a":
			ip := net.ParseIP(strings.TrimSpace(item.Value))
			if ip == nil || ip.To4() == nil {
				return nil, fmt.Errorf("invalid A record value %q", item.Value)
			}
			if question.Qtype == mdns.TypeA {
				header.Rrtype = mdns.TypeA
				direct = append(direct, &mdns.A{Hdr: header, A: ip.To4()})
			}
		case "cname":
			target := canonicalName(item.Value)
			if _, ok := mdns.IsDomainName(target); !ok {
				return nil, fmt.Errorf("invalid CNAME record value %q", item.Value)
			}
			if len(aliases) != 0 {
				return nil, fmt.Errorf("DNS response cannot contain multiple CNAME records")
			}
			header.Rrtype = mdns.TypeCNAME
			aliases = append(aliases, &mdns.CNAME{Hdr: header, Target: target})
		case "txt":
			if question.Qtype == mdns.TypeTXT {
				header.Rrtype = mdns.TypeTXT
				direct = append(direct, &mdns.TXT{Hdr: header, Txt: splitTXT(item.Value)})
			}
		default:
			return nil, fmt.Errorf("unsupported DNS response type %q", item.Type)
		}
	}
	if len(direct) > 0 {
		return direct, nil
	}
	return aliases, nil
}

func splitTXT(value string) []string {
	lines := strings.Split(value, "\n")
	chunks := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			chunks = append(chunks, "")
			continue
		}
		for len(line) > 255 {
			chunks = append(chunks, line[:255])
			line = line[255:]
		}
		chunks = append(chunks, line)
	}
	return chunks
}

func (s *Server) answerAuthority(response *mdns.Msg, question mdns.Question) {
	switch question.Qtype {
	case mdns.TypeNS:
		for _, nameserver := range s.config.Nameservers {
			response.Answer = append(response.Answer, &mdns.NS{
				Hdr: mdns.RR_Header{Name: s.config.Zone, Rrtype: mdns.TypeNS, Class: mdns.ClassINET, Ttl: s.config.TTL},
				Ns:  nameserver,
			})
		}
	case mdns.TypeSOA:
		response.Answer = append(response.Answer, s.soa())
	default:
		s.addSOA(response)
	}
}

func (s *Server) addSOA(response *mdns.Msg) {
	response.Ns = append(response.Ns, s.soa())
}

func (s *Server) soa() *mdns.SOA {
	return &mdns.SOA{
		Hdr:     mdns.RR_Header{Name: s.config.Zone, Rrtype: mdns.TypeSOA, Class: mdns.ClassINET, Ttl: s.config.TTL},
		Ns:      s.config.Nameservers[0],
		Mbox:    s.config.SOAEmail,
		Serial:  uint32(time.Now().UTC().Unix() / 86400),
		Refresh: 300,
		Retry:   60,
		Expire:  86400,
		Minttl:  s.config.TTL,
	}
}

func (s *Server) writeResponse(writer mdns.ResponseWriter, request, response *mdns.Msg) {
	if writer == nil || response == nil {
		return
	}
	if request != nil && strings.HasPrefix(transport(writer), "udp") {
		size := uint16(mdns.MinMsgSize)
		if option := request.IsEdns0(); option != nil && option.UDPSize() > size {
			size = option.UDPSize()
		}
		if size > maxSafeUDPSize {
			size = maxSafeUDPSize
		}
		response.Truncate(int(size))
	}
	_ = writer.WriteMsg(response)
}

func applyDefaults(config *Config) {
	if config.PlannerTimeout == 0 {
		config.PlannerTimeout = defaultPlannerTimeout
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = defaultReadTimeout
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = defaultWriteTimeout
	}
	if config.MaxResponseRecords == 0 {
		config.MaxResponseRecords = defaultMaxResponseItems
	}
	if config.RateLimit == 0 && config.RateBurst == 0 {
		config.RateLimit = defaultRateLimit
		config.RateBurst = defaultRateBurst
	}
	if strings.TrimSpace(config.SOAEmail) == "" && strings.TrimSpace(config.Zone) != "" {
		config.SOAEmail = "hostmaster." + strings.TrimSuffix(config.Zone, ".")
	}
}

func canonicalName(value string) string {
	return strings.ToLower(mdns.Fqdn(strings.TrimSpace(value)))
}

func typeName(value uint16) string {
	if name := mdns.TypeToString[value]; name != "" {
		return name
	}
	return fmt.Sprintf("TYPE%d", value)
}

func className(value uint16) string {
	if name := mdns.ClassToString[value]; name != "" {
		return name
	}
	return fmt.Sprintf("CLASS%d", value)
}

func queryID(writer mdns.ResponseWriter, request *mdns.Msg, question mdns.Question) string {
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err == nil {
		return "dns-" + hex.EncodeToString(identifier)
	}

	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\n%d\n%d\n%d\n%s\n%d", strings.ToLower(question.Name), question.Qtype, question.Qclass, request.Id, remoteAddress(writer), time.Now().UnixNano())
	return "dns-" + hex.EncodeToString(hash.Sum(nil)[:16])
}

func remoteAddress(writer mdns.ResponseWriter) string {
	if writer == nil || writer.RemoteAddr() == nil {
		return ""
	}
	return writer.RemoteAddr().String()
}

func transport(writer mdns.ResponseWriter) string {
	if writer == nil || writer.RemoteAddr() == nil {
		return ""
	}
	return strings.ToLower(writer.RemoteAddr().Network())
}
