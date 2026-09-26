package smtpout

import (
	"context"
	"errors"
	"strings"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

// GatewayAdapter translates one resolved SMTP delivery into a relay
// submission. Routing and retry decisions remain outside the adapter.
type GatewayAdapter struct {
	sender *Sender
}

func NewGatewayAdapter(sender *Sender) (*GatewayAdapter, error) {
	if sender == nil {
		return nil, errors.New("outbound SMTP sender is required")
	}
	return &GatewayAdapter{sender: sender}, nil
}

func (adapter *GatewayAdapter) DeliverGateway(ctx context.Context, delivery bridge.GatewayDelivery) error {
	if delivery.Protocol != bridge.ProtocolSMTP {
		return errors.New("outbound SMTP adapter requires an SMTP delivery")
	}

	return adapter.sender.Send(ctx, Message{
		EnvelopeFrom: strings.TrimSpace(delivery.Attributes["mail_from"]),
		Recipients:   []string{strings.TrimSpace(delivery.Target)},
		Data:         append([]byte(nil), delivery.Payload...),
	})
}

var _ bridge.GatewayDeliveryAdapter = (*GatewayAdapter)(nil)
