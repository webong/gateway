package integration

import (
	"context"
	"errors"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

const (
	OperationDNSExchange = "exchange"
	maxDNSMessageBytes   = 65535
)

var DNSContract = plugin.ContractRef{Name: "gateway.dns", Version: "v1"}

// DNSExchangeRequest carries one DNS wire query. Keeping the wire format makes
// the contract useful to SPF, DKIM, DMARC, MX and address lookups without
// teaching Arranger about record-type-specific fields.
type DNSExchangeRequest struct {
	Message []byte `json:"message"`
}

type DNSExchangeResponse struct {
	Message []byte `json:"message"`
}

type DNSResolver interface {
	Exchange(context.Context, []byte) ([]byte, error)
}

type dnsClient struct{ caller plugin.ReverseCaller }

func (h *RuntimeHost) DNS() DNSResolver { return dnsClient{caller: h.caller} }

func (d dnsClient) Exchange(ctx context.Context, message []byte) ([]byte, error) {
	if len(message) == 0 || len(message) > maxDNSMessageBytes {
		return nil, errors.New("DNS exchange requires one bounded wire message")
	}
	var result DNSExchangeResponse
	if err := call(ctx, d.caller, DNSContract, OperationDNSExchange, DNSExchangeRequest{Message: message}, &result); err != nil {
		return nil, err
	}
	if len(result.Message) == 0 || len(result.Message) > maxDNSMessageBytes {
		return nil, errors.New("DNS resolver returned an invalid wire message")
	}
	return result.Message, nil
}
