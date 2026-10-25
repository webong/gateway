// Package smtpout delivers accepted RFC 5322 messages through a configured
// SMTP relay. It deliberately does not own routing, alias lookup, retries,
// message storage, or mailbox access; those policies belong to the Gateway
// planner, durable mail queue, and optional external delivery adapters.
package smtpout

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	smtpclient "github.com/emersion/go-smtp"
	bridge "github.com/webong/gateway/arranger/protocol"
	"github.com/webong/gateway/pkg/email/spinner/mailnet"
)

type TLSMode string

var ErrPreAcceptConnection = errors.New("outbound SMTP connection failed before acceptance")

const (
	TLSNone     TLSMode = "none"
	TLSStartTLS TLSMode = "starttls"
	TLSImplicit TLSMode = "implicit"
)

type Config struct {
	Address    string
	LocalName  string
	ServerName string
	Username   string
	Password   string
	// OAuthToken is a short-lived access token supplied by the credential authority.
	// It must never be written into a durable delivery record.
	OAuthToken string
	TLSMode    TLSMode
	TLSConfig  *tls.Config
	Timeout    time.Duration
	// NetworkScope is set only for Planner-managed endpoints. Empty retains
	// the legacy deployment-configured relay dial path.
	NetworkScope string
}

// Message is one SMTP envelope and its unchanged RFC 5322 payload.
type Message struct {
	EnvelopeFrom string
	Recipients   []string
	Data         []byte
}

// Sender hides SMTP connection, TLS, authentication, and transaction
// lifecycle behind one operation.
type Sender struct {
	config Config
}

func NewSender(config Config) (*Sender, error) {
	config.Address = strings.TrimSpace(config.Address)
	config.LocalName = strings.TrimSpace(config.LocalName)
	config.ServerName = strings.TrimSpace(config.ServerName)
	if config.Address == "" {
		return nil, errors.New("outbound SMTP relay address is required")
	}
	if config.ServerName == "" {
		host, _, err := net.SplitHostPort(config.Address)
		if err != nil || strings.TrimSpace(host) == "" {
			return nil, errors.New("outbound SMTP server name is required")
		}
		config.ServerName = host
	}
	if config.LocalName == "" {
		config.LocalName = "gateway.local"
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.TLSMode == "" {
		config.TLSMode = TLSStartTLS
	}
	if config.TLSMode != TLSNone && config.TLSMode != TLSStartTLS && config.TLSMode != TLSImplicit {
		return nil, fmt.Errorf("unsupported outbound SMTP TLS mode %q", config.TLSMode)
	}
	if config.OAuthToken == "" && (config.Username == "") != (config.Password == "") {
		return nil, errors.New("outbound SMTP username and password must be configured together")
	}
	if config.OAuthToken != "" && (config.Username == "" || config.Password != "") {
		return nil, errors.New("outbound SMTP OAuth requires a username and excludes a password")
	}
	if config.OAuthToken != "" && (len(config.OAuthToken) > 4096 || strings.ContainsAny(config.OAuthToken, "\r\n\x00\x01")) {
		return nil, errors.New("outbound SMTP OAuth token is invalid")
	}
	if config.Username != "" && config.TLSMode == TLSNone {
		return nil, errors.New("outbound SMTP authentication requires TLS")
	}

	return &Sender{config: config}, nil
}

func (s *Sender) Send(ctx context.Context, message Message) error {
	if s == nil {
		return errors.New("outbound SMTP sender is not initialized")
	}
	message.EnvelopeFrom = strings.TrimSpace(message.EnvelopeFrom)
	// An empty reverse-path is valid for delivery status notifications. The
	// SMTP client encodes it as MAIL FROM:<> rather than inventing a sender.
	if len(message.Recipients) == 0 {
		return errors.New("outbound SMTP recipient is required")
	}
	for index := range message.Recipients {
		message.Recipients[index] = strings.TrimSpace(message.Recipients[index])
		if message.Recipients[index] == "" {
			return errors.New("outbound SMTP recipients cannot be empty")
		}
	}
	if len(message.Data) == 0 {
		return errors.New("outbound SMTP message data is required")
	}

	var connection net.Conn
	var err error
	if s.config.NetworkScope != "" {
		connection, err = mailnet.DialMailUpstream(ctx, s.config.Address, s.config.NetworkScope, s.config.Timeout)
	} else {
		connection, err = (&net.Dialer{Timeout: s.config.Timeout}).DialContext(ctx, "tcp", s.config.Address)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPreAcceptConnection, err)
	}
	defer connection.Close()

	deadline := time.Now().Add(s.config.Timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set outbound SMTP deadline: %w", err)
	}

	tlsConfig := s.tlsConfig()
	if s.config.TLSMode == TLSImplicit {
		connection = tls.Client(connection, tlsConfig)
	}

	var client *smtpclient.Client
	if s.config.TLSMode == TLSStartTLS {
		client, err = smtpclient.NewClientStartTLS(connection, tlsConfig)
		if err != nil {
			return classifySMTPResponse("start outbound SMTP TLS", err)
		}
	} else {
		client = smtpclient.NewClient(connection)
	}
	client.CommandTimeout = s.config.Timeout
	client.SubmissionTimeout = s.config.Timeout
	defer client.Close()

	if err := client.Hello(s.config.LocalName); err != nil {
		return classifySMTPResponse("greet outbound SMTP relay", err)
	}
	if s.config.Username != "" {
		var auth sasl.Client = sasl.NewPlainClient("", s.config.Username, s.config.Password)
		if s.config.OAuthToken != "" {
			auth = xoauth2Client{username: s.config.Username, token: s.config.OAuthToken}
		}
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("%w: authenticate to outbound SMTP relay", bridge.ErrPermanentDelivery)
		}
	}
	if err := client.Mail(message.EnvelopeFrom, nil); err != nil {
		return classifySMTPResponse("submit outbound SMTP sender", err)
	}
	for _, recipient := range message.Recipients {
		if err := client.Rcpt(recipient, nil); err != nil {
			return classifySMTPResponse("submit outbound SMTP recipient", err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return classifySMTPResponse("start outbound SMTP data", err)
	}
	if _, err := writer.Write(message.Data); err != nil {
		return fmt.Errorf("%w: outbound SMTP DATA write failed", bridge.ErrUncertainDelivery)
	}
	if err := writer.Close(); err != nil {
		var rejection *smtpclient.SMTPError
		if !errors.As(err, &rejection) {
			return fmt.Errorf("%w: outbound SMTP DATA acknowledgement was lost", bridge.ErrUncertainDelivery)
		}
		return classifySMTPResponse("submit outbound SMTP message", err)
	}
	// A successful DATA completion transfers responsibility to the relay.
	// QUIT is a session courtesy; an interrupted QUIT must not make the
	// caller retry a message that the relay already accepted.
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	_ = client.Quit()

	return nil
}

func classifySMTPResponse(stage string, err error) error {
	var reply *smtpclient.SMTPError
	if errors.As(err, &reply) && reply.Code >= 500 && reply.Code < 600 {
		return fmt.Errorf("%w: %s: %w", bridge.ErrPermanentDelivery, stage, err)
	}
	return fmt.Errorf("%s: %w", stage, err)
}

type xoauth2Client struct{ username, token string }

func (c xoauth2Client) Start() (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + c.username + "\x01auth=Bearer " + c.token + "\x01\x01"), nil
}

func (xoauth2Client) Next([]byte) ([]byte, error) {
	return nil, errors.New("outbound SMTP OAuth authentication was rejected")
}

func (s *Sender) tlsConfig() *tls.Config {
	if s.config.TLSConfig != nil {
		config := s.config.TLSConfig.Clone()
		if config.ServerName == "" {
			config.ServerName = s.config.ServerName
		}
		return config
	}

	return &tls.Config{ServerName: s.config.ServerName, MinVersion: tls.VersionTLS12}
}
