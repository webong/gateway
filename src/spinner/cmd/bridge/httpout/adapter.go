// Package httpout delivers queued HTTP callbacks using Spinner's outbound policy.
package httpout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/internal/forwarding"
)

type forwarder interface {
	ForwardStatus(forwarding.ForwardRequest) (int, error)
	ForwardSync(forwarding.ForwardRequest) (forwarding.ForwardResponse, error)
}

type Adapter struct{ forwarder forwarder }

func NewAdapter(forwarder forwarder) (*Adapter, error) {
	if forwarder == nil {
		return nil, fmt.Errorf("HTTP delivery forwarder is required")
	}
	return &Adapter{forwarder: forwarder}, nil
}

func (adapter *Adapter) DeliverGateway(ctx context.Context, delivery bridge.GatewayDelivery) error {
	_, err := adapter.DeliverGatewayResult(ctx, delivery)
	return err
}

func (adapter *Adapter) DeliverGatewayResult(ctx context.Context, delivery bridge.GatewayDelivery) (bridge.GatewayDeliveryResult, error) {
	if adapter == nil || adapter.forwarder == nil {
		return bridge.GatewayDeliveryResult{}, fmt.Errorf("HTTP delivery adapter is not configured")
	}
	if delivery.Protocol != bridge.ProtocolHTTP {
		return bridge.GatewayDeliveryResult{}, fmt.Errorf("HTTP delivery adapter received %q protocol", delivery.Protocol)
	}
	if err := delivery.Validate(); err != nil {
		return bridge.GatewayDeliveryResult{}, err
	}
	expected := 0
	if value := delivery.Attributes["expected_status"]; value != "" {
		var err error
		expected, err = strconv.Atoi(value)
		if err != nil || expected < 100 || expected > 599 {
			return bridge.GatewayDeliveryResult{}, fmt.Errorf("%w: invalid expected HTTP status", bridge.ErrPermanentDelivery)
		}
	}
	expectedBody := delivery.Attributes["expected_body_sha256"]
	if expectedBody != "" {
		decoded, err := hex.DecodeString(expectedBody)
		if err != nil || len(decoded) != sha256.Size || expectedBody != strings.ToLower(expectedBody) {
			return bridge.GatewayDeliveryResult{}, fmt.Errorf("%w: invalid expected HTTP response body digest", bridge.ErrPermanentDelivery)
		}
	}
	method := delivery.Attributes["method"]
	if method == "" {
		method = http.MethodPost
	}
	request := forwarding.ForwardRequest{
		TargetURL:   delivery.Target,
		RequestBody: delivery.Payload,
		Method:      method,
		RawQuery:    delivery.Attributes["raw_query"],
		Headers:     http.Header(delivery.Headers).Clone(),
		Context:     ctx,
	}
	if transport := delivery.Transport; transport != nil {
		request.Transport.ConnectTimeout = seconds(transport.ConnectTimeoutSeconds)
		request.Transport.RequestTimeout = seconds(transport.RequestTimeoutSeconds)
		if transport.Proxy != nil {
			request.Transport.HTTPProxy = transport.Proxy.HTTP
			request.Transport.HTTPSProxy = transport.Proxy.HTTPS
			request.Transport.NoProxy = append([]string(nil), transport.Proxy.NoProxy...)
		}
	}
	var status int
	var body []byte
	var err error
	if expectedBody == "" {
		status, err = adapter.forwarder.ForwardStatus(request)
	} else {
		var response forwarding.ForwardResponse
		response, err = adapter.forwarder.ForwardSync(request)
		status, body = response.StatusCode, response.Body
	}
	if err != nil {
		if errors.Is(err, forwarding.ErrResponseTooLarge) {
			return bridge.GatewayDeliveryResult{}, fmt.Errorf("%w: bounded HTTP response body exceeded the limit", bridge.ErrPermanentDelivery)
		}
		return bridge.GatewayDeliveryResult{}, err
	}
	result := bridge.GatewayDeliveryResult{ResponseStatus: status}
	if expected != 0 {
		if status != expected {
			return result, fmt.Errorf("%w: HTTP delivery returned status %d, expected %d", bridge.ErrPermanentDelivery, status, expected)
		}
	} else if status < 200 || status >= 300 {
		if status >= 300 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooEarly && status != http.StatusTooManyRequests {
			return result, fmt.Errorf("%w: HTTP delivery returned status %d", bridge.ErrPermanentDelivery, status)
		}
		return result, fmt.Errorf("HTTP delivery returned status %d", status)
	}
	if expectedBody != "" {
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) != expectedBody {
			result.BodyAssertion = "mismatched"
			return result, fmt.Errorf("%w: HTTP response body digest did not match", bridge.ErrPermanentDelivery)
		}
		result.BodyAssertion = "matched"
	}
	return result, nil
}

func seconds(value float64) time.Duration { return time.Duration(value * float64(time.Second)) }

var _ bridge.GatewayDeliveryAdapter = (*Adapter)(nil)
var _ bridge.GatewayDeliveryResultAdapter = (*Adapter)(nil)
