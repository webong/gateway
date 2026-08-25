package gateway

import (
	"context"
	"strings"

	"github.com/webong/gateway/cmd/internal/forwarding"
	"github.com/webong/gateway/cmd/internal/workers"
)

// RelayExecutor adapts the Go transport implementation to the Go/PHP bridge.
// It contains no registry or route-binding logic.
type RelayExecutor struct {
	forwarder  *forwarding.Forwarder
	workerPool *workers.WorkerPool
}

func NewRelayExecutor(forwarder *forwarding.Forwarder, workerPool *workers.WorkerPool) *RelayExecutor {
	return &RelayExecutor{forwarder: forwarder, workerPool: workerPool}
}

func (e *RelayExecutor) Deliver(ctx context.Context, delivery Delivery) (Response, error) {
	response, err := e.forwarder.ForwardSync(forwarding.ForwardRequest{
		TargetURL:   delivery.URL,
		RequestBody: append([]byte(nil), delivery.Body...),
		Method:      delivery.Method,
		RawQuery:    delivery.RawQuery,
		Headers:     cloneRelayHeaders(delivery.Headers),
		Context:     ctx,
	})
	if err != nil {
		return Response{}, err
	}

	return Response{
		StatusCode: response.StatusCode,
		Headers:    cloneRelayHeaders(response.Header),
		Body:       append([]byte(nil), response.Body...),
	}, nil
}

func (e *RelayExecutor) Enqueue(delivery Delivery) bool {
	return e.workerPool.Submit(forwarding.ForwardRequest{
		TargetURL:   delivery.URL,
		RequestBody: append([]byte(nil), delivery.Body...),
		Method:      delivery.Method,
		RawQuery:    delivery.RawQuery,
		Headers:     cloneRelayHeaders(delivery.Headers),
	})
}

func (e *RelayExecutor) EnqueueGateway(delivery GatewayDelivery) bool {
	if e == nil || e.workerPool == nil || delivery.Protocol != ProtocolHTTP || strings.TrimSpace(delivery.Target) == "" {
		return false
	}

	method := delivery.Attributes["method"]
	if method == "" {
		method = "POST"
	}

	return e.workerPool.Submit(forwarding.ForwardRequest{
		TargetURL:   delivery.Target,
		RequestBody: append([]byte(nil), delivery.Payload...),
		Method:      method,
		RawQuery:    delivery.Attributes["raw_query"],
		Headers:     cloneRelayHeaders(delivery.Headers),
	})
}

func cloneRelayHeaders(headers map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

var _ Executor = (*RelayExecutor)(nil)
