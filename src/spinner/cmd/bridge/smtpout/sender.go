// Package smtpout delivers accepted RFC 5322 messages through a configured
// SMTP relay. It deliberately does not own routing, alias lookup, retries,
// message storage, or mailbox access; those policies belong to the Gateway
// planner, durable mail queue, and optional external delivery adapters.
package smtpout

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	smtpclient "github.com/emersion/go-smtp"
)

type TLSMode string

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
	TLSMode    TLSMode
	TLSConfig  *tls.Config
	Timeout    time.Duration
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
	if (config.Username == "") != (config.Password == "") {
		return nil, errors.New("outbound SMTP username and password must be configured together")
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
	if message.EnvelopeFrom == "" {
		return errors.New("outbound SMTP envelope sender is required")
	}
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

	dialer := &net.Dialer{Timeout: s.config.Timeout}
	connection, err := dialer.DialContext(ctx, "tcp", s.config.Address)
	if err != nil {
		return fmt.Errorf("connect to outbound SMTP relay: %w", err)
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
			return fmt.Errorf("start outbound SMTP TLS: %w", err)
		}
	} else {
		client = smtpclient.NewClient(connection)
	}
	client.CommandTimeout = s.config.Timeout
	client.SubmissionTimeout = s.config.Timeout
	defer client.Close()

	if err := client.Hello(s.config.LocalName); err != nil {
		return fmt.Errorf("greet outbound SMTP relay: %w", err)
	}
	if s.config.Username != "" {
		auth := sasl.NewPlainClient("", s.config.Username, s.config.Password)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("authenticate to outbound SMTP relay: %w", err)
		}
	}
	if err := client.SendMail(message.EnvelopeFrom, message.Recipients, bytes.NewReader(message.Data)); err != nil {
		return fmt.Errorf("submit outbound SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("close outbound SMTP transaction: %w", err)
	}

	return nil
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
