package main

import (
	"context"

	bridge "github.com/webong/web-relay/cmd/bridge"
	"github.com/webong/web-relay/cmd/internal/forwarding"
	"github.com/webong/web-relay/cmd/internal/workers"
)

// relayExecutor adapts the Go transport implementation to the Go/PHP bridge.
// It contains no registry or route-binding logic.
type relayExecutor struct {
	forwarder  *forwarding.Forwarder
	workerPool *workers.WorkerPool
}

func newRelayExecutor(forwarder *forwarding.Forwarder, workerPool *workers.WorkerPool) *relayExecutor {
	return &relayExecutor{forwarder: forwarder, workerPool: workerPool}
}

func (e *relayExecutor) Deliver(ctx context.Context, delivery bridge.Delivery) (bridge.Response, error) {
	response, err := e.forwarder.ForwardSync(forwarding.ForwardRequest{
		TargetURL:   delivery.URL,
		RequestBody: append([]byte(nil), delivery.Body...),
		Method:      delivery.Method,
		RawQuery:    delivery.RawQuery,
		Headers:     cloneRelayHeaders(delivery.Headers),
		Context:     ctx,
	})
	if err != nil {
		return bridge.Response{}, err
	}

	return bridge.Response{
		StatusCode: response.StatusCode,
		Headers:    cloneRelayHeaders(response.Header),
		Body:       append([]byte(nil), response.Body...),
	}, nil
}

func (e *relayExecutor) Enqueue(delivery bridge.Delivery) bool {
	return e.workerPool.Submit(forwarding.ForwardRequest{
		TargetURL:   delivery.URL,
		RequestBody: append([]byte(nil), delivery.Body...),
		Method:      delivery.Method,
		RawQuery:    delivery.RawQuery,
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

var _ bridge.Executor = (*relayExecutor)(nil)
