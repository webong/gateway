// Package smtp adapts the maintained emersion/go-smtp server to the gateway
// protocol boundary. It owns SMTP lifecycle and session state; PHP still
// decides what each message means and Go only executes resolved deliveries.
package smtp

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	smtpserver "github.com/emersion/go-smtp"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

var ErrServerClosed = errors.New("SMTP server closed")

const (
	defaultHostname       = "gateway.local"
	defaultMaxMessageSize = 10 * 1024 * 1024
	defaultMaxLineSize    = 16 * 1024
	defaultMaxRecipients  = 100
	defaultReadTimeout    = 5 * time.Minute
	defaultWriteTimeout   = 30 * time.Second
	defaultPlannerTimeout = 30 * time.Second
)

// Config controls the SMTP listener and transaction limits.
type Config struct {
	Network        string
	Address        string
	Hostname       string
	MaxMessageSize int64
	MaxLineSize    int
	MaxRecipients  int
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	PlannerTimeout time.Duration
	TLSConfig      *tls.Config
	ImplicitTLS    bool
	AuthEnabled    bool
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
	if config.MaxMessageSize < 1 || config.MaxLineSize < 1 || config.MaxRecipients < 1 {
		return nil, fmt.Errorf("SMTP size and recipient limits must be positive")
	}
	if config.ImplicitTLS && config.TLSConfig == nil {
		return nil, fmt.Errorf("implicit SMTP TLS requires a certificate and key")
	}
	if config.AuthEnabled && config.TLSConfig == nil {
		return nil, fmt.Errorf("SMTP AUTH requires TLS configuration")
	}

	server := smtpserver.NewServer(&backend{
		planner:  planner,
		executor: executor,
		config:   config,
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
	if s.config.ImplicitTLS {
		return normalizeServerError(s.server.ListenAndServeTLS())
	}
	return normalizeServerError(s.server.ListenAndServe())
}

// ListenAndServeTLS exposes go-smtp's implicit TLS mode for deployments that
// need SMTPS. Plain ListenAndServe still supports STARTTLS when TLSConfig is
// configured.
func (s *Server) ListenAndServeTLS() error {
	if s == nil || s.server == nil {
		return fmt.Errorf("SMTP server is not initialized")
	}
	return normalizeServerError(s.server.ListenAndServeTLS())
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
	if s.config.ImplicitTLS {
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
	}, nil
}

type session struct {
	planner       bridge.ProtocolPlanner
	executor      bridge.GatewayExecutor
	config        Config
	sessionID     string
	helo          string
	remoteAddr    string
	from          string
	recipients    []string
	authenticated bool
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

	s.authenticated = true
	return nil
}

func (s *session) Mail(from string, _ *smtpserver.MailOptions) error {
	if s.config.AuthEnabled && !s.authenticated {
		return smtpserver.ErrAuthRequired
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

	event := bridge.GatewayEvent{
		ID:        transactionID(s.sessionID, s.from, s.recipients, message),
		Protocol:  bridge.ProtocolSMTP,
		Kind:      bridge.EventTransaction,
		SessionID: s.sessionID,
		Host:      s.config.Hostname,
		Route:     s.recipients[0],
		Headers:   messageHeaders(message),
		Attributes: map[string]string{
			"mail_from":   s.from,
			"recipients":  strings.Join(s.recipients, ","),
			"remote_addr": s.remoteAddr,
			"helo":        s.helo,
		},
		Payload: message,
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

	switch decision.Action {
	case bridge.GatewayAccept:
		return nil
	case bridge.GatewayDeliver:
		for index := range decision.Deliveries {
			delivery := decision.Deliveries[index]
			if len(delivery.Payload) == 0 {
				delivery.Payload = append([]byte(nil), message...)
			}
			if !s.executor.EnqueueGateway(delivery) {
				return smtpError(452, "gateway delivery queue unavailable")
			}
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

func (s *session) Logout() error {
	s.Reset()
	s.authenticated = false
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
	if config.ReadTimeout == 0 {
		config.ReadTimeout = defaultReadTimeout
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = defaultWriteTimeout
	}
	if config.PlannerTimeout == 0 {
		config.PlannerTimeout = defaultPlannerTimeout
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
