package smtpout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

// MailboxCredential is resolved at delivery time and must not enter a spool.
type MailboxCredential struct {
	UpstreamID string `json:"upstream_id"`
	Username   string `json:"username"`
	Secret     string `json:"secret"`
	Mechanism  string `json:"mechanism"`
	Address    string `json:"address"`
}

type MailboxCredentialResolver interface {
	Resolve(context.Context, string) (MailboxCredential, error)
}

type MailboxAdapter struct {
	resolver  MailboxCredentialResolver
	upstreams map[string]Config
}

func NewMailboxAdapter(resolver MailboxCredentialResolver, upstreams map[string]Config) (*MailboxAdapter, error) {
	if resolver == nil || len(upstreams) == 0 {
		return nil, errors.New("mailbox SMTP requires a credential resolver and approved upstreams")
	}
	approved := make(map[string]Config, len(upstreams))
	for id, config := range upstreams {
		if id == "" || len(id) > 64 || strings.ContainsAny(id, " \t\r\n/") || config.Address == "" || config.ServerName == "" || config.Username != "" || config.Password != "" || config.OAuthToken != "" {
			return nil, errors.New("mailbox SMTP upstream configuration is invalid")
		}
		if _, _, err := net.SplitHostPort(config.Address); err != nil {
			return nil, errors.New("mailbox SMTP upstream address is invalid")
		}
		if config.TLSMode != TLSImplicit && config.TLSMode != TLSStartTLS {
			return nil, errors.New("mailbox SMTP upstream requires TLS")
		}
		approved[id] = config
	}
	return &MailboxAdapter{resolver: resolver, upstreams: approved}, nil
}

func (a *MailboxAdapter) DeliverGateway(ctx context.Context, delivery bridge.GatewayDelivery) error {
	if a == nil || delivery.Protocol != bridge.ProtocolSMTP || delivery.AdapterName() != "mailbox.smtp" {
		return fmt.Errorf("%w: invalid mailbox SMTP delivery", bridge.ErrPermanentDelivery)
	}
	id := delivery.Attributes["mailbox_connection_id"]
	if !validConnectionID(id) {
		return fmt.Errorf("%w: invalid mailbox connection", bridge.ErrPermanentDelivery)
	}
	credential, err := a.resolver.Resolve(ctx, id)
	if err != nil {
		return errors.New("mailbox SMTP credential lookup is unavailable")
	}
	config, ok := a.upstreams[credential.UpstreamID]
	if !ok || credential.Address == "" || !strings.EqualFold(credential.Address, delivery.Attributes["mail_from"]) {
		return fmt.Errorf("%w: mailbox sender is not authorized", bridge.ErrPermanentDelivery)
	}
	config.Username = credential.Username
	switch credential.Mechanism {
	case "PLAIN":
		config.Password = credential.Secret
	case "XOAUTH2":
		config.OAuthToken = credential.Secret
	default:
		return fmt.Errorf("%w: mailbox authentication mechanism is invalid", bridge.ErrPermanentDelivery)
	}
	sender, err := NewSender(config)
	if err != nil {
		return fmt.Errorf("%w: mailbox SMTP configuration is invalid", bridge.ErrPermanentDelivery)
	}
	recipients := []string{delivery.Target}
	if raw := delivery.Attributes["recipients"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &recipients); err != nil || len(recipients) < 1 || len(recipients) > 100 || recipients[0] != delivery.Target {
			return fmt.Errorf("%w: mailbox recipient batch is invalid", bridge.ErrPermanentDelivery)
		}
	}
	return sender.Send(ctx, Message{EnvelopeFrom: delivery.Attributes["mail_from"], Recipients: recipients, Data: delivery.Payload})
}

func validConnectionID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, r := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// RetryableLookupTimeout is kept below the queue's delivery timeout.
const RetryableLookupTimeout = 5 * time.Second
