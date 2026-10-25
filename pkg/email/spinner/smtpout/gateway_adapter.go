package smtpout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	bridge "github.com/webong/gateway/arranger/integration"
)

// GatewayAdapter translates one resolved SMTP delivery into a relay
// submission. Routing and retry decisions remain outside the adapter.
type GatewayAdapter struct {
	sender *Sender
	signer DKIMSigning
}

type DKIMSigning interface {
	CheckSigningDomain(string) error
	Sign(string, []byte) ([]byte, error)
}

func NewGatewayAdapter(sender *Sender, signer DKIMSigning) (*GatewayAdapter, error) {
	if sender == nil {
		return nil, errors.New("outbound SMTP sender is required")
	}
	return &GatewayAdapter{sender: sender, signer: signer}, nil
}

// CheckSigningDomain lets SMTP ingress fail before acknowledging a queued
// submission that this adapter could never sign. No configured signer means
// signing is intentionally disabled.
func (adapter *GatewayAdapter) CheckSigningDomain(domain string) error {
	if adapter == nil {
		return errors.New("outbound SMTP adapter is unavailable")
	}
	if adapter.signer == nil {
		return nil
	}
	return adapter.signer.CheckSigningDomain(domain)
}

func (adapter *GatewayAdapter) DeliverGateway(ctx context.Context, delivery bridge.GatewayDelivery) error {
	if delivery.Protocol != bridge.ProtocolSMTP {
		return errors.New("outbound SMTP adapter requires an SMTP delivery")
	}

	recipients := []string{strings.TrimSpace(delivery.Target)}
	if raw := delivery.Attributes["recipients"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &recipients); err != nil || len(recipients) < 1 || len(recipients) > 100 || recipients[0] != delivery.Target {
			return fmt.Errorf("outbound SMTP recipient batch is invalid")
		}
	}
	data := append([]byte(nil), delivery.Payload...)
	if domain := delivery.Attributes[bridge.DKIMSigningDomainAttribute]; domain != "" && adapter.signer != nil {
		var err error
		data, err = adapter.signer.Sign(domain, data)
		if err != nil {
			return err
		}
	}
	return adapter.sender.Send(ctx, Message{
		EnvelopeFrom: strings.TrimSpace(delivery.Attributes["mail_from"]),
		Recipients:   recipients,
		Data:         data,
	})
}

var _ bridge.GatewayDeliveryAdapter = (*GatewayAdapter)(nil)
