// Package dns adapts authoritative DNS queries to the Gateway protocol
// planner. Go owns DNS parsing and response encoding; PHP owns endpoint and
// subscriber resolution and returns an immediate response, HTTP reply, or
// normal Automation whose successful response body describes DNS records.
package spinner

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
	"strconv"
	"strings"
	"sync"
	"time"

	mdns "github.com/miekg/dns"
	"golang.org/x/time/rate"

	"github.com/webong/gateway/arranger/automation"
	bridge "github.com/webong/gateway/arranger/integration"
)

var ErrServerClosed = errors.New("DNS server closed")

const (
	defaultPlannerTimeout   = 750 * time.Millisecond
	defaultReadTimeout      = 2 * time.Second
	defaultWriteTimeout     = 2 * time.Second
	defaultMaxResponseItems = 16
	defaultRateLimit        = 5000
	defaultRateBurst        = 10000
	defaultZoneTTL          = 60
	minimumPlannerTimeout   = 10 * time.Millisecond
	maximumPlannerTimeout   = 30 * time.Second
	minimumSocketTimeout    = 100 * time.Millisecond
	maximumSocketTimeout    = 30 * time.Second
	maximumResponseItems    = 256
	maximumRateLimit        = 100000
	maximumRateBurst        = 200000
	maxSafeUDPSize          = 1232
)

type Config struct {
	Address            string
	Zones              []Zone
	PlannerTimeout     time.Duration
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	MaxResponseRecords int
	RateLimit          int
	RateBurst          int
	Forwarders         []Forwarder
	Observations       ObservationQueue
	Automation         automation.Executor
	Schedules          automation.ScheduleRegistrar
}

// Zone is one delegated authoritative namespace served by this listener.
type Zone struct {
	Name        string       `json:"name"`
	Nameservers []string     `json:"nameservers"`
	SOAEmail    string       `json:"soa_email,omitempty"`
	TTL         uint32       `json:"ttl,omitempty"`
	Records     []ZoneRecord `json:"records,omitempty"`
}

// ZoneRecord is an operator-managed exact DNS answer, not a provided endpoint.
type ZoneRecord struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Value      string  `json:"value"`
	Preference *uint16 `json:"preference,omitempty"`
}

type Executor interface {
	Deliver(context.Context, bridge.Delivery) (bridge.Response, error)
	EnqueueGateway(bridge.GatewayDelivery) bool
}

type Server struct {
	config       Config
	zones        []Zone
	planner      bridge.ProtocolPlanner
	executor     Executor
	udp          *mdns.Server
	tcp          *mdns.Server
	limiter      *rate.Limiter
	forwarders   []forwarderRoute
	observations ObservationQueue
	mu           sync.Mutex
	started      bool
	closing      bool
}

func NewServer(config Config, planner bridge.ProtocolPlanner, executor Executor) (*Server, error) {
	applyDefaults(&config)
	if strings.TrimSpace(config.Address) == "" {
		return nil, fmt.Errorf("DNS listen address is required")
	}
	if len(config.Zones) == 0 && len(config.Forwarders) == 0 {
		return nil, fmt.Errorf("at least one DNS zone or forwarder is required")
	}
	if planner == nil {
		return nil, fmt.Errorf("DNS protocol planner is required")
	}
	if executor == nil {
		return nil, fmt.Errorf("DNS gateway executor is required")
	}
	if config.PlannerTimeout < minimumPlannerTimeout || config.PlannerTimeout > maximumPlannerTimeout {
		return nil, fmt.Errorf("DNS planner timeout must be between %s and %s", minimumPlannerTimeout, maximumPlannerTimeout)
	}
	if config.ReadTimeout < minimumSocketTimeout || config.ReadTimeout > maximumSocketTimeout ||
		config.WriteTimeout < minimumSocketTimeout || config.WriteTimeout > maximumSocketTimeout {
		return nil, fmt.Errorf("DNS socket timeouts must be between %s and %s", minimumSocketTimeout, maximumSocketTimeout)
	}
	if config.MaxResponseRecords < 1 || config.MaxResponseRecords > maximumResponseItems {
		return nil, fmt.Errorf("DNS maximum response records must be between 1 and %d", maximumResponseItems)
	}
	if config.RateLimit < 0 || config.RateLimit > maximumRateLimit || config.RateBurst < 0 ||
		config.RateBurst > maximumRateBurst || (config.RateLimit > 0 && config.RateBurst < 1) {
		return nil, fmt.Errorf("DNS rate limit and burst settings are invalid")
	}

	zones, err := normalizeZones(config.Zones, defaultZoneTTL)
	if err != nil {
		return nil, err
	}
	config.Zones = zones
	forwarders, err := normalizeForwarders(config.Forwarders)
	if err != nil {
		return nil, err
	}

	server := &Server{config: config, zones: config.Zones, planner: planner, executor: executor, forwarders: forwarders, observations: config.Observations}
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
	if question.Qtype == mdns.TypeANY {
		response.Rcode = mdns.RcodeRefused
		_ = s.writeResponse(writer, request, response)
		return
	}
	observed := newDNSObservation(writer, question, queryID(writer, request, question))
	zone := s.zoneFor(queryName)
	if zone == nil {
		if forwarder := s.forwarderFor(queryName, writer.RemoteAddr()); forwarder != nil {
			observed.mode = "forwarded"
			observed.forwarder = forwarder.name
			s.forwardQuery(writer, request, *forwarder, observed)
			return
		}
		response.Rcode = mdns.RcodeRefused
		_ = s.writeResponse(writer, request, response)
		return
	}
	observed.mode = "managed"
	observed.zone = zone.Name
	if queryName == zone.Name && (question.Qtype == mdns.TypeNS || question.Qtype == mdns.TypeSOA) {
		s.answerAuthority(response, question, *zone)
		s.writeObservedResponse(writer, request, response, observed)
		return
	}
	if records, exists := zoneRecords(*zone, queryName); exists {
		payload, err := json.Marshal(answerItems(records))
		if err != nil {
			response.Rcode = mdns.RcodeServerFailure
		} else if response.Answer, err = parseAnswers(question, payload, zone.TTL, s.config.MaxResponseRecords); err != nil {
			response.Rcode = mdns.RcodeServerFailure
		} else if len(response.Answer) == 0 {
			s.addSOA(response, *zone)
		}
		if response.Rcode == mdns.RcodeServerFailure {
			observed.failure = "static_answer_validation"
		}
		s.writeObservedResponse(writer, request, response, observed)
		return
	}
	if queryName == zone.Name {
		s.addSOA(response, *zone)
		s.writeObservedResponse(writer, request, response, observed)
		return
	}

	queryLabels := mdns.SplitDomainName(question.Name)
	zoneLabels := mdns.SplitDomainName(zone.Name)
	labels := queryLabels[:len(queryLabels)-len(zoneLabels)]
	if len(labels) == 0 {
		response.Rcode = mdns.RcodeNameError
		s.addSOA(response, *zone)
		s.writeObservedResponse(writer, request, response, observed)
		return
	}
	endpoint := strings.ToLower(labels[len(labels)-1])
	observed.mode = "provided"
	observed.endpoint = endpoint
	dataLabels := labels[:len(labels)-1]
	observed.data = strings.Join(dataLabels, ".")
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
		observed.failure = "query_encoding"
		s.writeObservedResponse(writer, request, response, observed)
		return
	}

	event := bridge.GatewayEvent{
		ID:       observed.id,
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
			"zone":        zone.Name,
			"endpoint":    endpoint,
			"data":        strings.Join(dataLabels, "."),
			"source_addr": remoteAddress(writer),
			"transport":   transport(writer),
		},
		Payload: payload,
		Runtime: bridge.RuntimeCapabilities{
			AutomationLanguages: automation.Languages(s.config.Automation),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.config.PlannerTimeout)
	defer cancel()
	decision, planErr := s.planner.PlanEvent(ctx, event)
	if planErr != nil {
		response.Rcode = mdns.RcodeServerFailure
		observed.failure = "planner_unavailable"
		s.writeObservedResponse(writer, request, response, observed)
		return
	}
	if decision.Validate() != nil || decision.Protocol != bridge.ProtocolDNS {
		response.Rcode = mdns.RcodeServerFailure
		observed.failure = "invalid_decision"
		s.writeObservedResponse(writer, request, response, observed)
		return
	}
	observed.decisionAction = string(decision.Action)
	observed.decisionStatus = decision.StatusCode

	answerPayload, relayDropped, decisionErr := s.executeDecision(ctx, decision, event)
	if decisionErr != nil {
		response.Rcode = mdns.RcodeServerFailure
		observed.failure = "answer_delivery"
		s.writeObservedResponse(writer, request, response, observed)
		return
	}
	if relayDropped {
		observed.failure = "relay_queue"
	}

	switch decision.Action {
	case bridge.GatewayReject:
		response.Rcode = mdns.RcodeNameError
		s.addSOA(response, *zone)
	case bridge.GatewayClose:
		response.Rcode = mdns.RcodeRefused
	case bridge.GatewayAccept, bridge.GatewayDeliver, bridge.GatewayRespond, bridge.GatewayAutomate:
		answers, parseErr := parseAnswers(question, answerPayload, zone.TTL, s.config.MaxResponseRecords)
		if parseErr != nil {
			response.Rcode = mdns.RcodeServerFailure
			observed.failure = "answer_validation"
			break
		}
		response.Answer = answers
		if len(answers) == 0 {
			s.addSOA(response, *zone)
		}
	default:
		response.Rcode = mdns.RcodeServerFailure
	}

	s.writeObservedResponse(writer, request, response, observed)
}

type Query struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Class    string   `json:"class"`
	Endpoint string   `json:"endpoint"`
	Labels   []string `json:"labels"`
	Data     string   `json:"data"`
}

func (s *Server) executeDecision(ctx context.Context, decision bridge.GatewayDecision, event bridge.GatewayEvent) ([]byte, bool, error) {
	answerPayload := append([]byte(nil), decision.Payload...)
	deliveries := append([]bridge.GatewayDelivery(nil), decision.Deliveries...)

	if decision.Action == bridge.GatewayAutomate {
		if decision.Automation == nil || s.config.Automation == nil {
			return nil, false, errors.New("DNS automation is unavailable")
		}
		outcome, err := s.config.Automation.Execute(ctx, automation.Script{
			Language: decision.Automation.Language,
			Source:   decision.Automation.Source,
			Executor: decision.Automation.Executor,
		}, dnsAutomationInput(event))
		if err != nil {
			return nil, false, fmt.Errorf("execute DNS automation: %w", err)
		}
		if outcome.RunFailed {
			return nil, false, errors.New("DNS automation reported failure")
		}
		if len(outcome.Schedules) > 0 {
			if s.config.Schedules == nil {
				return nil, false, errors.New("DNS automation schedule registration is unavailable")
			}
			if err := s.config.Schedules.RegisterSchedules(ctx, dnsAutomationOrigin(decision, event), outcome.Schedules); err != nil {
				return nil, false, fmt.Errorf("register DNS automation schedules: %w", err)
			}
		}
		if outcome.Response == nil || outcome.Response.Status < http.StatusOK || outcome.Response.Status >= http.StatusMultipleChoices {
			return nil, false, errors.New("DNS automation requires a successful response")
		}
		answerPayload = []byte(outcome.Response.Body)
		deliveries = append(deliveries, dnsAutomationDeliveries(outcome.Deliveries, event.ID)...)
	} else if decision.Reply != nil {
		delivery, err := httpDelivery(*decision.Reply, event.Payload)
		if err != nil {
			return nil, false, err
		}
		reply, err := s.executor.Deliver(ctx, delivery)
		if err != nil {
			return nil, false, err
		}
		if reply.StatusCode < http.StatusOK || reply.StatusCode >= http.StatusMultipleChoices {
			return nil, false, fmt.Errorf("DNS reply subscriber returned HTTP status %d", reply.StatusCode)
		}
		answerPayload = append([]byte(nil), reply.Body...)
	}

	relayDropped := false
	for index := range deliveries {
		delivery := deliveries[index]
		if len(delivery.Payload) == 0 {
			delivery.Payload = append([]byte(nil), event.Payload...)
		}
		if !s.executor.EnqueueGateway(delivery) {
			relayDropped = true
		}
	}

	return answerPayload, relayDropped, nil
}

func dnsAutomationInput(event bridge.GatewayEvent) automation.Input {
	var query map[string]any
	if err := json.Unmarshal(event.Payload, &query); err != nil {
		query = map[string]any{"content": string(event.Payload)}
	}
	eventDocument := map[string]any{
		"id": event.ID, "protocol": event.Protocol, "kind": event.Kind,
		"host": event.Host, "path": event.Route, "headers": event.Headers,
		"attributes": event.Attributes, "body": string(event.Payload),
	}
	return automation.Input{Event: eventDocument, Variables: map[string]any{
		"request": query,
		"dns":     query,
	}}
}

func dnsAutomationOrigin(decision bridge.GatewayDecision, event bridge.GatewayEvent) string {
	if endpointID := strings.TrimSpace(decision.Metadata["endpoint_id"]); endpointID != "" {
		return "endpoint:" + endpointID
	}
	sum := sha256.Sum256([]byte(event.Host + "\n" + event.Route))
	return fmt.Sprintf("dns:%x", sum)
}

func dnsAutomationDeliveries(deliveries []automation.Delivery, eventID string) []bridge.GatewayDelivery {
	converted := make([]bridge.GatewayDelivery, 0, len(deliveries))
	for _, delivery := range deliveries {
		method := delivery.Method
		if method == "" {
			method = http.MethodPost
		}
		attributes := map[string]string{"method": method, "event_id": eventID}
		if delivery.ExpectedStatus != 0 {
			attributes["expected_status"] = strconv.Itoa(delivery.ExpectedStatus)
		}
		if delivery.ExpectedBodySHA256 != "" {
			attributes["expected_body_sha256"] = delivery.ExpectedBodySHA256
		}
		converted = append(converted, bridge.GatewayDelivery{
			Protocol: bridge.ProtocolHTTP, Target: delivery.URL, Headers: delivery.Headers,
			Attributes: attributes, Payload: []byte(delivery.Body),
		})
	}
	return converted
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
	Type       string  `json:"type"`
	Value      string  `json:"value"`
	Preference *uint16 `json:"preference,omitempty"`
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
	nonAliases := 0
	nullMX := 0
	regularMX := 0
	for _, item := range items {
		header := mdns.RR_Header{Name: question.Name, Class: mdns.ClassINET, Ttl: ttl}
		kind := strings.ToLower(strings.TrimSpace(item.Type))
		if kind != "mx" && item.Preference != nil {
			return nil, fmt.Errorf("DNS preference is only valid for MX records")
		}
		if kind != "cname" {
			nonAliases++
		}
		switch kind {
		case "a":
			ip := net.ParseIP(strings.TrimSpace(item.Value))
			if ip == nil || ip.To4() == nil {
				return nil, fmt.Errorf("invalid A record value %q", item.Value)
			}
			if question.Qtype == mdns.TypeA {
				header.Rrtype = mdns.TypeA
				direct = append(direct, &mdns.A{Hdr: header, A: ip.To4()})
			}
		case "aaaa":
			ip := net.ParseIP(strings.TrimSpace(item.Value))
			if ip == nil || ip.To16() == nil || ip.To4() != nil {
				return nil, fmt.Errorf("invalid AAAA record value %q", item.Value)
			}
			if question.Qtype == mdns.TypeAAAA {
				header.Rrtype = mdns.TypeAAAA
				direct = append(direct, &mdns.AAAA{Hdr: header, AAAA: ip.To16()})
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
		case "mx":
			if item.Preference == nil {
				return nil, fmt.Errorf("MX record preference is required")
			}
			target := canonicalName(item.Value)
			if target == "." {
				if *item.Preference != 0 {
					return nil, fmt.Errorf("null MX preference must be zero")
				}
				nullMX++
			} else {
				if !validAuthorityName(target) {
					return nil, fmt.Errorf("invalid MX exchange %q", item.Value)
				}
				regularMX++
			}
			if question.Qtype == mdns.TypeMX {
				header.Rrtype = mdns.TypeMX
				direct = append(direct, &mdns.MX{Hdr: header, Preference: *item.Preference, Mx: target})
			}
		default:
			return nil, fmt.Errorf("unsupported DNS response type %q", item.Type)
		}
	}
	if len(aliases) > 0 && nonAliases > 0 {
		return nil, fmt.Errorf("CNAME cannot coexist with other DNS records")
	}
	if nullMX > 0 && (nullMX != 1 || regularMX > 0) {
		return nil, fmt.Errorf("null MX must be the only MX record")
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

func (s *Server) zoneFor(name string) *Zone {
	var selected *Zone
	for index := range s.zones {
		zone := &s.zones[index]
		if mdns.IsSubDomain(zone.Name, name) && (selected == nil || len(zone.Name) > len(selected.Name)) {
			copy := *zone
			copy.Nameservers = append([]string(nil), zone.Nameservers...)
			selected = &copy
		}
	}
	return selected
}

func normalizeZones(zones []Zone, defaultTTL uint32) ([]Zone, error) {
	normalized := make([]Zone, len(zones))
	copy(normalized, zones)
	seen := make(map[string]bool, len(normalized))
	for index := range normalized {
		zone := &normalized[index]
		zone.Nameservers = append([]string(nil), zone.Nameservers...)
		zone.Records = append([]ZoneRecord(nil), zone.Records...)
		if zone.TTL == 0 {
			zone.TTL = defaultTTL
		}
		if strings.TrimSpace(zone.Name) == "" {
			return nil, fmt.Errorf("DNS authoritative zone is required")
		}
		zone.Name = canonicalName(zone.Name)
		if !validAuthorityName(zone.Name) || seen[zone.Name] {
			return nil, fmt.Errorf("invalid or duplicate DNS authoritative zone %q", zone.Name)
		}
		seen[zone.Name] = true
		if len(zone.Nameservers) == 0 {
			return nil, fmt.Errorf("at least one authoritative DNS nameserver is required for %s", zone.Name)
		}
		for nameserver := range zone.Nameservers {
			zone.Nameservers[nameserver] = canonicalName(zone.Nameservers[nameserver])
			if !validAuthorityName(zone.Nameservers[nameserver]) {
				return nil, fmt.Errorf("invalid DNS nameserver %q", zone.Nameservers[nameserver])
			}
		}
		if len(zone.Records) > 256 {
			return nil, fmt.Errorf("too many DNS records for %s", zone.Name)
		}
		byName := make(map[string][]ZoneRecord)
		for recordIndex := range zone.Records {
			record := &zone.Records[recordIndex]
			record.Name = canonicalName(record.Name)
			record.Type = strings.ToUpper(record.Type)
			if !validRecordOwner(record.Name) || !mdns.IsSubDomain(zone.Name, record.Name) {
				return nil, fmt.Errorf("invalid DNS record owner %q", record.Name)
			}
			byName[record.Name] = append(byName[record.Name], *record)
		}
		for name, records := range byName {
			if name == zone.Name {
				for _, record := range records {
					if record.Type == "CNAME" {
						return nil, fmt.Errorf("DNS zone apex %s cannot be a CNAME", name)
					}
				}
			}
			payload, err := json.Marshal(answerItems(records))
			if err != nil {
				return nil, err
			}
			question := mdns.Question{Name: name, Qtype: mdns.TypeMX, Qclass: mdns.ClassINET}
			if _, err := parseAnswers(question, payload, zone.TTL, defaultMaxResponseItems); err != nil {
				return nil, fmt.Errorf("invalid DNS records at %s: %w", name, err)
			}
		}
		if zone.SOAEmail == "" {
			zone.SOAEmail = "hostmaster." + zone.Name
		}
		zone.SOAEmail = canonicalName(zone.SOAEmail)
		if !validAuthorityName(zone.SOAEmail) {
			return nil, fmt.Errorf("invalid DNS SOA email %q", zone.SOAEmail)
		}
	}
	return normalized, nil
}

func zoneRecords(zone Zone, owner string) ([]ZoneRecord, bool) {
	var records []ZoneRecord
	for _, record := range zone.Records {
		if record.Name == owner {
			records = append(records, record)
		}
	}
	return records, len(records) > 0
}

func answerItems(records []ZoneRecord) []answer {
	items := make([]answer, 0, len(records))
	for _, record := range records {
		items = append(items, answer{Type: record.Type, Value: record.Value, Preference: record.Preference})
	}
	return items
}

func validRecordOwner(name string) bool {
	if len(name) > 254 || !strings.HasSuffix(name, ".") {
		return false
	}
	labels := mdns.SplitDomainName(name)
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
				return false
			}
		}
	}
	return true
}

func (s *Server) answerAuthority(response *mdns.Msg, question mdns.Question, zone Zone) {
	switch question.Qtype {
	case mdns.TypeNS:
		for _, nameserver := range zone.Nameservers {
			response.Answer = append(response.Answer, &mdns.NS{
				Hdr: mdns.RR_Header{Name: zone.Name, Rrtype: mdns.TypeNS, Class: mdns.ClassINET, Ttl: zone.TTL},
				Ns:  nameserver,
			})
		}
	case mdns.TypeSOA:
		response.Answer = append(response.Answer, s.soa(zone))
	default:
		s.addSOA(response, zone)
	}
}

func (s *Server) addSOA(response *mdns.Msg, zone Zone) {
	response.Ns = append(response.Ns, s.soa(zone))
}

func (s *Server) soa(zone Zone) *mdns.SOA {
	return &mdns.SOA{
		Hdr:     mdns.RR_Header{Name: zone.Name, Rrtype: mdns.TypeSOA, Class: mdns.ClassINET, Ttl: zone.TTL},
		Ns:      zone.Nameservers[0],
		Mbox:    zone.SOAEmail,
		Serial:  uint32(time.Now().UTC().Unix() / 86400),
		Refresh: 300,
		Retry:   60,
		Expire:  86400,
		Minttl:  zone.TTL,
	}
}

func (s *Server) writeResponse(writer mdns.ResponseWriter, request, response *mdns.Msg) error {
	if writer == nil || response == nil {
		return nil
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
	return writer.WriteMsg(response)
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
}

func canonicalName(value string) string {
	return strings.ToLower(mdns.Fqdn(strings.TrimSpace(value)))
}

func validAuthorityName(name string) bool {
	name = strings.TrimSuffix(name, ".")
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := 0; index < len(label); index++ {
			character := label[index]
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
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
