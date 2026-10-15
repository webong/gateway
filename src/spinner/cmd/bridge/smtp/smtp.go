// Package smtp adapts the maintained emersion/go-smtp server to the gateway
// protocol boundary. It owns SMTP lifecycle and session state; PHP still
// decides what each message means and Go only executes resolved deliveries.
package smtp

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/emersion/go-sasl"
	smtpserver "github.com/emersion/go-smtp"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

var ErrServerClosed = errors.New("SMTP server closed")

type SigningDomainChecker interface {
	CheckSigningDomain(domain string) error
}

const (
	defaultHostname               = "gateway.local"
	defaultMaxMessageSize         = 10 * 1024 * 1024
	defaultMaxLineSize            = 16 * 1024
	defaultMaxRecipients          = 100
	defaultMaxConnections         = 1000
	defaultMessagesPerMinute      = 120
	defaultMessageBurst           = 20
	defaultReadTimeout            = 5 * time.Minute
	defaultWriteTimeout           = 30 * time.Second
	defaultPlannerTimeout         = 30 * time.Second
	defaultSPFTimeout             = 3 * time.Second
	defaultDKIMTimeout            = 3 * time.Second
	defaultDomainExistenceTimeout = 3 * time.Second
	defaultScanTimeout            = 10 * time.Second
)

// Config controls the SMTP listener and transaction limits.
type Config struct {
	Network                 string
	Address                 string
	Hostname                string
	MaxMessageSize          int64
	MaxLineSize             int
	MaxRecipients           int
	MaxConnections          int
	MessagesPerMinute       int
	MessageBurst            int
	ReadTimeout             time.Duration
	WriteTimeout            time.Duration
	PlannerTimeout          time.Duration
	SPFTimeout              time.Duration
	SPFEvaluator            SPFEvaluator
	DKIMTimeout             time.Duration
	DKIMVerifier            DKIMVerifier
	DomainExistenceTimeout  time.Duration
	DomainExistenceResolver DomainExistenceResolver
	SubmitTimeout           time.Duration
	Submitter               bridge.GatewayDeliveryAdapter
	SigningDomainChecker    SigningDomainChecker
	Scanner                 MalwareScanner
	ScanTimeout             time.Duration
	Quarantine              QuarantineStore
	QuarantineRetention     time.Duration
	LoopKey                 []byte
	TLSConfig               *tls.Config
	ImplicitTLS             bool
	AuthEnabled             bool
	RecipientPreflight      bool
}

// Server is the gateway-owned lifecycle wrapper around emersion/go-smtp.
// Keeping this wrapper small lets src/spinner/cmd/proxy and the Caddy app module share the
// same SMTP implementation while supplying different listeners.
type Server struct {
	config Config
	server *smtpserver.Server
}

func NewServer(config Config, planner bridge.ProtocolPlanner, executor bridge.GatewayExecutor) (*Server, error) {
	applyDefaults(&config)
	if strings.TrimSpace(config.Address) == "" {
		return nil, fmt.Errorf("SMTP listen address is required")
	}
	if planner == nil {
		return nil, fmt.Errorf("SMTP protocol planner is required")
	}
	if executor == nil {
		return nil, fmt.Errorf("SMTP gateway executor is required")
	}
	if config.MaxMessageSize < 1 || config.MaxLineSize < 1 || config.MaxRecipients < 1 || config.MaxConnections < 1 || config.MessagesPerMinute < 1 || config.MessageBurst < 1 {
		return nil, fmt.Errorf("SMTP size, recipient, connection, and rate limits must be positive")
	}
	if config.SubmitTimeout < 1 {
		return nil, fmt.Errorf("SMTP submit timeout must be positive")
	}
	if config.SPFTimeout < 1 {
		return nil, fmt.Errorf("SMTP SPF timeout must be positive")
	}
	if config.DKIMTimeout < 1 {
		return nil, fmt.Errorf("SMTP DKIM timeout must be positive")
	}
	if config.DomainExistenceResolver != nil && config.DomainExistenceTimeout < 1 {
		return nil, fmt.Errorf("SMTP author-domain lookup timeout must be positive")
	}
	if config.ScanTimeout < 1 {
		return nil, fmt.Errorf("SMTP malware scan timeout must be positive")
	}
	if config.QuarantineRetention < 0 {
		return nil, fmt.Errorf("SMTP quarantine retention cannot be negative")
	}
	if config.QuarantineRetention > 0 {
		if _, ok := config.Quarantine.(QuarantinePruner); !ok {
			return nil, fmt.Errorf("SMTP quarantine retention requires a prunable quarantine store")
		}
	}
	if config.Submitter != nil && len(config.LoopKey) == 0 {
		return nil, fmt.Errorf("outbound SMTP submission requires a forwarding-loop key")
	}
	if config.ImplicitTLS && config.TLSConfig == nil {
		return nil, fmt.Errorf("implicit SMTP TLS requires a certificate and key")
	}
	if config.AuthEnabled && config.TLSConfig == nil {
		return nil, fmt.Errorf("SMTP AUTH requires TLS configuration")
	}
	if config.QuarantineRetention > 0 {
		if _, err := config.Quarantine.(QuarantinePruner).PruneBefore(time.Now().UTC().Add(-config.QuarantineRetention)); err != nil {
			return nil, fmt.Errorf("prune SMTP quarantine before serving: %w", err)
		}
	}

	server := smtpserver.NewServer(&backend{
		planner:  planner,
		executor: executor,
		config:   config,
		limiter:  newSourceLimiter(config.MessagesPerMinute, config.MessageBurst),
	})
	server.Network = config.Network
	server.Addr = config.Address
	server.Domain = config.Hostname
	server.TLSConfig = config.TLSConfig
	server.MaxRecipients = config.MaxRecipients
	server.MaxMessageBytes = config.MaxMessageSize
	server.MaxLineLength = config.MaxLineSize
	server.ReadTimeout = config.ReadTimeout
	server.WriteTimeout = config.WriteTimeout

	return &Server{config: config, server: server}, nil
}

func (s *Server) ListenAndServe() error {
	if s == nil || s.server == nil {
		return fmt.Errorf("SMTP server is not initialized")
	}
	listener, err := net.Listen(s.config.Network, s.config.Address)
	if err != nil {
		return err
	}
	return s.Serve(listener)
}

// ListenAndServeTLS exposes go-smtp's implicit TLS mode for deployments that
// need SMTPS. Plain ListenAndServe still supports STARTTLS when TLSConfig is
// configured.
func (s *Server) ListenAndServeTLS() error {
	if s == nil || s.server == nil {
		return fmt.Errorf("SMTP server is not initialized")
	}
	if s.config.TLSConfig == nil {
		return fmt.Errorf("implicit SMTP TLS requires a certificate and key")
	}
	listener, err := net.Listen(s.config.Network, s.config.Address)
	if err != nil {
		return err
	}
	return s.serveListener(listener, true)
}

// Serve accepts a caller-provided listener. Caddy uses this to provide a
// reload-aware listener; the Spinner process uses ListenAndServe.
func (s *Server) Serve(listener net.Listener) error {
	if s == nil || s.server == nil {
		return fmt.Errorf("SMTP server is not initialized")
	}
	if listener == nil {
		return fmt.Errorf("SMTP listener is required")
	}
	return s.serveListener(listener, s.config.ImplicitTLS)
}

func (s *Server) serveListener(listener net.Listener, implicitTLS bool) error {
	if implicitTLS && s.config.TLSConfig == nil {
		return fmt.Errorf("implicit SMTP TLS requires a certificate and key")
	}
	if s.config.QuarantineRetention > 0 {
		pruner := s.config.Quarantine.(QuarantinePruner)
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					if _, err := pruner.PruneBefore(time.Now().UTC().Add(-s.config.QuarantineRetention)); err != nil {
						log.Printf("Gateway SMTP quarantine retention failed: %v", err)
					}
				}
			}
		}()
	}
	listener = newLimitedListener(listener, s.config.MaxConnections)
	if implicitTLS {
		listener = tls.NewListener(listener, s.config.TLSConfig)
	}
	return normalizeServerError(s.server.Serve(listener))
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	err := s.server.Shutdown(ctx)
	if errors.Is(err, smtpserver.ErrServerClosed) {
		return ErrServerClosed
	}
	return err
}

func normalizeServerError(err error) error {
	if errors.Is(err, smtpserver.ErrServerClosed) {
		return ErrServerClosed
	}
	return err
}

type backend struct {
	planner  bridge.ProtocolPlanner
	executor bridge.GatewayExecutor
	config   Config
	limiter  *sourceLimiter
}

func (b *backend) NewSession(connection *smtpserver.Conn) (smtpserver.Session, error) {
	remoteAddress := ""
	if connection != nil && connection.Conn() != nil && connection.Conn().RemoteAddr() != nil {
		remoteAddress = connection.Conn().RemoteAddr().String()
	}

	return &session{
		planner:    b.planner,
		executor:   b.executor,
		config:     b.config,
		sessionID:  newSessionID(remoteAddress),
		helo:       connection.Hostname(),
		remoteAddr: remoteAddress,
		limiter:    b.limiter,
	}, nil
}

type session struct {
	planner                  bridge.ProtocolPlanner
	executor                 bridge.GatewayExecutor
	config                   Config
	sessionID                string
	helo                     string
	remoteAddr               string
	limiter                  *sourceLimiter
	from                     string
	recipients               []string
	authenticated            bool
	authUsername             string
	authIdentity             string
	submissionDailyLimit     string
	submissionDailyByteLimit string
	authorizedSenders        map[string]struct{}
}

func (s *session) AuthMechanisms() []string {
	if !s.config.AuthEnabled {
		return nil
	}

	return []string{sasl.Plain}
}

func (s *session) Auth(mech string) (sasl.Server, error) {
	if !s.config.AuthEnabled || mech != sasl.Plain {
		return nil, smtpserver.ErrAuthUnknownMechanism
	}

	return sasl.NewPlainServer(func(identity, username, password string) error {
		if strings.TrimSpace(username) == "" {
			return errors.New("SMTP username is required")
		}

		if err := s.authenticate(identity, username, password); err != nil {
			return errors.New("SMTP authentication failed")
		}

		return nil
	}), nil
}

func (s *session) authenticate(identity, username, password string) error {
	s.Reset()
	s.authenticated = false
	s.authUsername = ""
	s.authIdentity = ""
	s.submissionDailyLimit = ""
	s.submissionDailyByteLimit = ""
	s.authorizedSenders = nil
	event := bridge.GatewayEvent{
		ID:        transactionID(s.sessionID, username, []string{identity}, []byte(password)),
		Protocol:  bridge.ProtocolSMTP,
		Kind:      bridge.EventAuthenticate,
		SessionID: s.sessionID,
		Host:      s.config.Hostname,
		Route:     username,
		Attributes: map[string]string{
			"identity":    identity,
			"username":    username,
			"remote_addr": s.remoteAddr,
			"helo":        s.helo,
			"mechanism":   sasl.Plain,
			"password":    password,
		},
	}
	if peerIP, ok := smtpPeerIP(s.remoteAddr); ok {
		event.Attributes["remote_ip"] = peerIP
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.config.PlannerTimeout)
	defer cancel()

	decision, err := s.planner.PlanEvent(ctx, event)
	if err != nil {
		return err
	}
	if err := decision.Validate(); err != nil || decision.Protocol != bridge.ProtocolSMTP {
		return errors.New("invalid SMTP authentication decision")
	}
	if decision.Action != bridge.GatewayAccept {
		return errors.New("SMTP authentication rejected")
	}
	canonicalIdentity := decision.Metadata["auth_identity"]
	if !validAuthenticationIdentity(canonicalIdentity) {
		return errors.New("SMTP authentication decision has no valid canonical identity")
	}
	senders, err := authorizedMailFrom(decision.Metadata["authorized_mail_from"])
	if err != nil {
		return err
	}
	dailyLimit := decision.Metadata["submission_daily_limit"]
	if dailyLimit != "" {
		parsed, err := strconv.Atoi(dailyLimit)
		if err != nil || parsed < 1 || parsed > 1_000_000 {
			return errors.New("SMTP authentication decision has an invalid submission quota")
		}
	}
	dailyByteLimit := decision.Metadata["submission_daily_byte_limit"]
	if dailyByteLimit != "" {
		parsed, err := strconv.ParseInt(dailyByteLimit, 10, 64)
		if err != nil || parsed < 1 || parsed > 1_000_000_000_000 {
			return errors.New("SMTP authentication decision has an invalid submission byte quota")
		}
	}

	s.authenticated = true
	s.authUsername = username
	s.authIdentity = canonicalIdentity
	s.submissionDailyLimit = dailyLimit
	s.submissionDailyByteLimit = dailyByteLimit
	s.authorizedSenders = senders
	return nil
}

func validAuthenticationIdentity(identity string) bool {
	if identity == "" || len(identity) > 255 || strings.TrimSpace(identity) != identity || !utf8.ValidString(identity) {
		return false
	}
	for _, character := range identity {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

// An AUTH decision must explicitly grant envelope senders. A successful
// credential check alone cannot grant permission to impersonate a domain.
func authorizedMailFrom(raw string) (map[string]struct{}, error) {
	var addresses []string
	if err := json.Unmarshal([]byte(raw), &addresses); err != nil || len(addresses) == 0 || len(addresses) > 100 {
		return nil, errors.New("SMTP authentication decision has no valid authorized senders")
	}
	senders := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Address != address || len(address) > 254 || strings.Contains(address, "*") {
			return nil, errors.New("SMTP authentication decision has an invalid sender")
		}
		senders[address] = struct{}{}
	}
	return senders, nil
}

func (s *session) Mail(from string, _ *smtpserver.MailOptions) error {
	s.Reset()
	if s.config.AuthEnabled && !s.authenticated {
		return smtpserver.ErrAuthRequired
	}
	if s.config.AuthEnabled {
		if _, allowed := s.authorizedSenders[from]; !allowed {
			return smtpError(553, "envelope sender is not authorized")
		}
	}
	if s.limiter != nil && !s.limiter.allow(s.remoteAddr) {
		return &smtpserver.SMTPError{
			Code:         451,
			EnhancedCode: smtpserver.EnhancedCode{4, 7, 1},
			Message:      "SMTP source rate limit exceeded",
		}
	}
	s.from = from
	s.recipients = nil
	return nil
}

func (s *session) Rcpt(to string, _ *smtpserver.RcptOptions) error {
	if s.config.AuthEnabled && !s.authenticated {
		return smtpserver.ErrAuthRequired
	}
	if len(s.recipients) >= s.config.MaxRecipients {
		return &smtpserver.SMTPError{
			Code:         452,
			EnhancedCode: smtpserver.EnhancedCode{4, 5, 3},
			Message:      "Maximum recipient limit reached",
		}
	}
	if s.config.RecipientPreflight {
		ctx, cancel := context.WithTimeout(context.Background(), s.config.PlannerTimeout)
		defer cancel()
		attributes := s.transactionAttributes()
		if len(s.recipients) > 0 {
			accepted, _ := json.Marshal(s.recipients)
			attributes["accepted_recipients"] = string(accepted)
		}
		decision, err := s.planner.PlanEvent(ctx, bridge.GatewayEvent{
			ID:         newSessionID(s.sessionID + ":recipient:" + to),
			Protocol:   bridge.ProtocolSMTP,
			Kind:       bridge.EventRecipient,
			SessionID:  s.sessionID,
			Host:       s.config.Hostname,
			Route:      to,
			Attributes: attributes,
		})
		if err != nil || decision.Validate() != nil || decision.Protocol != bridge.ProtocolSMTP {
			return smtpError(451, "gateway recipient planning failed")
		}
		switch decision.Action {
		case bridge.GatewayAccept:
		case bridge.GatewayReject, bridge.GatewayClose:
			return smtpError(smtpStatusCode(decision.StatusCode, 550), decision.Message)
		default:
			return smtpError(451, "gateway returned an invalid recipient decision")
		}
	}
	s.recipients = append(s.recipients, to)
	return nil
}

func (s *session) Data(reader io.Reader) error {
	if s.config.AuthEnabled && !s.authenticated {
		return smtpserver.ErrAuthRequired
	}
	message, err := io.ReadAll(reader)
	if err != nil {
		if errors.Is(err, smtpserver.ErrDataTooLarge) {
			return smtpserver.ErrDataTooLarge
		}
		return smtpError(451, "message read failed")
	}
	if int64(len(message)) > s.config.MaxMessageSize {
		return smtpserver.ErrDataTooLarge
	}
	if len(s.recipients) == 0 {
		return smtpError(554, "message has no recipients")
	}

	attributes := s.transactionAttributes()
	attributes["recipients"] = strings.Join(s.recipients, ",")
	for key, value := range authorAttributes(message) {
		attributes[key] = value
	}
	signingDomain := ""
	if s.config.AuthEnabled {
		author := authorAddress(message)
		if _, allowed := s.authorizedSenders[author]; !allowed || author == "" || attributes["rfc5322_from_domain"] == "" {
			return smtpError(553, "message author is not authorized")
		}
		signingDomain = attributes["rfc5322_from_domain"]
	}
	if domain := attributes["rfc5322_from_domain"]; domain != "" && s.config.DomainExistenceResolver != nil {
		lookupCtx, lookupCancel := context.WithTimeout(context.Background(), s.config.DomainExistenceTimeout)
		existence, lookupErr := s.config.DomainExistenceResolver.Lookup(lookupCtx, domain)
		lookupCancel()
		if lookupErr != nil || (existence != "exists" && existence != "nxdomain") {
			existence = "temperror"
		}
		attributes["rfc5322_from_existence"] = existence
	}
	spfCtx, spfCancel := context.WithTimeout(context.Background(), s.config.SPFTimeout)
	for key, value := range s.spfAttributes(spfCtx) {
		attributes[key] = value
	}
	spfCancel()
	dkimCtx, dkimCancel := context.WithTimeout(context.Background(), s.config.DKIMTimeout)
	for key, value := range s.dkimAttributes(dkimCtx, message) {
		attributes[key] = value
	}
	dkimCancel()
	event := bridge.GatewayEvent{
		ID:         transactionID(s.sessionID, s.from, s.recipients, message),
		Protocol:   bridge.ProtocolSMTP,
		Kind:       bridge.EventTransaction,
		SessionID:  s.sessionID,
		Host:       s.config.Hostname,
		Route:      s.recipients[0],
		Headers:    messageHeaders(message),
		Attributes: attributes,
		Payload:    message,
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.config.PlannerTimeout)
	defer cancel()

	decision, err := s.planner.PlanEvent(ctx, event)
	if err != nil {
		return smtpError(451, "gateway planning failed")
	}
	if err := decision.Validate(); err != nil {
		return smtpError(451, "gateway returned an invalid decision")
	}
	if decision.Protocol != bridge.ProtocolSMTP {
		return smtpError(451, "gateway returned a decision for the wrong protocol")
	}
	if s.config.Scanner != nil && (decision.Action == bridge.GatewaySubmit || decision.Action == bridge.GatewayDeliver) {
		scanCtx, scanCancel := context.WithTimeout(context.Background(), s.config.ScanTimeout)
		verdict, scanErr := s.config.Scanner.Scan(scanCtx, message)
		scanCancel()
		if scanErr != nil {
			return smtpError(451, "malware scanner is temporarily unavailable")
		}
		if verdict.Infected {
			if s.config.Quarantine == nil {
				return smtpError(554, "message blocked by malware scanner")
			}
			storeCtx, storeCancel := context.WithTimeout(context.Background(), s.config.ScanTimeout)
			_, storeErr := s.config.Quarantine.Store(storeCtx, QuarantinedMessage{
				TransactionID: event.ID,
				EnvelopeFrom:  s.from,
				Recipients:    s.recipients,
				Reason:        verdict.Signature,
				Payload:       message,
			})
			storeCancel()
			if storeErr != nil {
				return smtpError(451, "malware quarantine is temporarily unavailable")
			}
			return nil
		}
	}

	switch decision.Action {
	case bridge.GatewayAccept:
		return nil
	case bridge.GatewayQuarantine:
		if s.config.Quarantine == nil {
			return smtpError(451, "SMTP quarantine storage is unavailable")
		}
		storeCtx, storeCancel := context.WithTimeout(context.Background(), s.config.ScanTimeout)
		defer storeCancel()
		if _, err := s.config.Quarantine.Store(storeCtx, QuarantinedMessage{
			TransactionID: event.ID,
			EnvelopeFrom:  s.from,
			Recipients:    s.recipients,
			Reason:        "DMARC policy",
			Payload:       message,
		}); err != nil {
			return smtpError(451, "SMTP quarantine storage is temporarily unavailable")
		}
		return nil
	case bridge.GatewayDeliver:
		// Once DATA is acknowledged, a sender may discard its only copy.
		// Volatile worker submission cannot satisfy that handoff.
		durable, ok := s.executor.(bridge.DurableGatewayExecutor)
		if !ok || !durable.DurableGatewayDeliveries() {
			return smtpError(451, "durable gateway delivery is unavailable")
		}
		deliveries := decision.Deliveries
		if decision.Reply != nil {
			deliveries = append(append([]bridge.GatewayDelivery(nil), deliveries...), *decision.Reply)
		}
		if signingDomain != "" && s.config.SigningDomainChecker != nil {
			for _, delivery := range deliveries {
				if delivery.Protocol == bridge.ProtocolSMTP {
					if err := s.config.SigningDomainChecker.CheckSigningDomain(signingDomain); err != nil {
						return smtpError(451, "DKIM signing key is unavailable for the message author")
					}
					break
				}
			}
		}
		failureNoticeTo := decision.Metadata["failure_notice_to"]
		if failureNoticeTo != "" && (!s.config.AuthEnabled || !s.authenticated || s.from == "" || failureNoticeTo != s.from) {
			return smtpError(451, "failure notice is not authorized for this SMTP transaction")
		}
		eligibleNotices := 0
		for _, delivery := range deliveries {
			if delivery.Protocol != bridge.ProtocolSMTP || failureNoticeTo == "" {
				continue
			}
			if delivery.Attributes["mail_from"] != s.from {
				return smtpError(451, "failure notice requires the original SMTP reverse path")
			}
			eligibleNotices++
		}
		if failureNoticeTo != "" && eligibleNotices == 0 {
			return smtpError(451, "failure notice requires an SMTP delivery")
		}
		prepared := make([]bridge.GatewayDelivery, 0, len(deliveries))
		for index := range deliveries {
			delivery := deliveries[index]
			if delivery.Attributes != nil {
				attributes := make(map[string]string, len(delivery.Attributes))
				for key, value := range delivery.Attributes {
					if key != bridge.FailureNoticeToAttribute && key != bridge.DKIMSigningDomainAttribute {
						attributes[key] = value
					}
				}
				delivery.Attributes = attributes
			}
			if failureNoticeTo != "" && delivery.Protocol == bridge.ProtocolSMTP {
				if delivery.Attributes == nil {
					delivery.Attributes = make(map[string]string)
				}
				delivery.Attributes[bridge.FailureNoticeToAttribute] = failureNoticeTo
			}
			if signingDomain != "" && delivery.Protocol == bridge.ProtocolSMTP {
				if delivery.Attributes == nil {
					delivery.Attributes = make(map[string]string)
				}
				delivery.Attributes[bridge.DKIMSigningDomainAttribute] = signingDomain
			}
			if len(delivery.Payload) == 0 {
				delivery.Payload = append([]byte(nil), message...)
			}
			prepared = append(prepared, delivery)
		}
		if len(prepared) > 1 {
			batch, ok := s.executor.(bridge.DurableGatewayBatchExecutor)
			if !ok || !batch.EnqueueGatewayBatch(event.ID, prepared) {
				return smtpError(452, "gateway fanout queue unavailable")
			}
		} else if !s.executor.EnqueueGateway(prepared[0]) {
			return smtpError(452, "gateway delivery queue unavailable")
		}
		return nil
	case bridge.GatewaySubmit:
		if s.config.Submitter == nil {
			return smtpError(451, "outbound SMTP relay is unavailable")
		}
		if signingDomain != "" && s.config.SigningDomainChecker != nil {
			if err := s.config.SigningDomainChecker.CheckSigningDomain(signingDomain); err != nil {
				return smtpError(451, "DKIM signing key is unavailable for the message author")
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.config.SubmitTimeout)
		defer cancel()
		delivery := decision.Deliveries[0]
		if delivery.Attributes != nil {
			attributes := make(map[string]string, len(delivery.Attributes))
			for key, value := range delivery.Attributes {
				if key != bridge.DKIMSigningDomainAttribute {
					attributes[key] = value
				}
			}
			delivery.Attributes = attributes
		}
		if signingDomain != "" {
			if delivery.Attributes == nil {
				delivery.Attributes = make(map[string]string)
			}
			delivery.Attributes[bridge.DKIMSigningDomainAttribute] = signingDomain
		}
		if len(delivery.Payload) == 0 {
			delivery.Payload = append([]byte(nil), message...)
		}
		if forwardingHops(delivery.Payload, s.config.LoopKey) >= maxForwardingHops || receivedHops(delivery.Payload) >= maxReceivedHops {
			return smtpError(554, "gateway forwarding loop detected")
		}
		delivery.Payload, err = markForwarding(delivery.Payload, s.config.LoopKey)
		if err != nil {
			return smtpError(451, "gateway forwarding trace is unavailable")
		}
		if err := s.config.Submitter.DeliverGateway(ctx, delivery); err != nil {
			return smtpError(451, "outbound SMTP relay did not accept the message")
		}
		return nil
	case bridge.GatewayRespond:
		if decision.StatusCode >= 400 && decision.StatusCode <= 599 {
			return smtpError(decision.StatusCode, decision.Message)
		}
		return nil
	case bridge.GatewayReject:
		return smtpError(smtpStatusCode(decision.StatusCode, 550), decision.Message)
	case bridge.GatewayClose:
		return smtpError(smtpStatusCode(decision.StatusCode, 550), decision.Message)
	default:
		return smtpError(451, "gateway returned an unsupported action")
	}
}

func (s *session) Reset() {
	s.from = ""
	s.recipients = nil
}

func (s *session) transactionAttributes() map[string]string {
	attributes := make(map[string]string, 4)
	if s.from != "" {
		attributes["mail_from"] = s.from
	}
	if s.remoteAddr != "" {
		attributes["remote_addr"] = s.remoteAddr
		if peerIP, ok := smtpPeerIP(s.remoteAddr); ok {
			attributes["remote_ip"] = peerIP
		}
	}
	if s.helo != "" {
		attributes["helo"] = s.helo
	}
	if s.authenticated && s.authUsername != "" {
		attributes["auth_username"] = s.authUsername
		attributes["auth_identity"] = s.authIdentity
		if s.submissionDailyLimit != "" {
			attributes["submission_daily_limit"] = s.submissionDailyLimit
		}
		if s.submissionDailyByteLimit != "" {
			attributes["submission_daily_byte_limit"] = s.submissionDailyByteLimit
		}
	}
	return attributes
}

func (s *session) Logout() error {
	s.Reset()
	s.authenticated = false
	s.authUsername = ""
	s.authIdentity = ""
	s.submissionDailyLimit = ""
	s.submissionDailyByteLimit = ""
	s.authorizedSenders = nil
	return nil
}

func smtpError(code int, message string) *smtpserver.SMTPError {
	if code < 400 || code > 599 {
		code = 451
	}
	message = strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.TrimSpace(message))
	if message == "" {
		message = "gateway transaction failed"
	}
	return &smtpserver.SMTPError{
		Code:         code,
		EnhancedCode: smtpserver.EnhancedCode{code / 100, 0, 0},
		Message:      message,
	}
}

func smtpStatusCode(status, fallback int) int {
	if status >= 400 && status <= 599 {
		return status
	}
	return fallback
}

func applyDefaults(config *Config) {
	if config.Network == "" {
		config.Network = "tcp"
	}
	if config.Hostname == "" {
		config.Hostname = defaultHostname
	}
	if config.MaxMessageSize == 0 {
		config.MaxMessageSize = defaultMaxMessageSize
	}
	if config.MaxLineSize == 0 {
		config.MaxLineSize = defaultMaxLineSize
	}
	if config.MaxRecipients == 0 {
		config.MaxRecipients = defaultMaxRecipients
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = defaultMaxConnections
	}
	if config.MessagesPerMinute == 0 {
		config.MessagesPerMinute = defaultMessagesPerMinute
	}
	if config.MessageBurst == 0 {
		config.MessageBurst = defaultMessageBurst
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = defaultReadTimeout
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = defaultWriteTimeout
	}
	if config.PlannerTimeout == 0 {
		config.PlannerTimeout = defaultPlannerTimeout
	}
	if config.SPFTimeout == 0 {
		config.SPFTimeout = defaultSPFTimeout
	}
	if config.DKIMTimeout == 0 {
		config.DKIMTimeout = defaultDKIMTimeout
	}
	if config.DomainExistenceTimeout == 0 {
		config.DomainExistenceTimeout = defaultDomainExistenceTimeout
	}
	if config.ScanTimeout == 0 {
		config.ScanTimeout = defaultScanTimeout
	}
	if config.SubmitTimeout == 0 {
		config.SubmitTimeout = 30 * time.Second
	}
}

func messageHeaders(message []byte) map[string][]string {
	result := make(map[string][]string)
	lines := strings.Split(strings.ReplaceAll(string(message), "\r\n", "\n"), "\n")
	lastName := ""
	for _, line := range lines {
		if line == "" {
			break
		}
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && lastName != "" {
			values := result[lastName]
			values[len(values)-1] += " " + strings.TrimSpace(line)
			result[lastName] = values
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		lastName = strings.TrimSpace(name)
		result[lastName] = append(result[lastName], strings.TrimSpace(value))
	}
	return result
}

func newSessionID(remoteAddress string) string {
	sum := sha256.Sum256([]byte(remoteAddress + "\n" + time.Now().UTC().Format(time.RFC3339Nano)))
	return fmt.Sprintf("smtp-%x", sum[:12])
}

func transactionID(sessionID, from string, recipients []string, message []byte) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, sessionID+"\n"+from+"\n"+strings.Join(recipients, ",")+"\n")
	_, _ = hash.Write(message)
	return fmt.Sprintf("smtp-%x", hash.Sum(nil)[:16])
}

var _ smtpserver.Backend = (*backend)(nil)
var _ smtpserver.Session = (*session)(nil)
