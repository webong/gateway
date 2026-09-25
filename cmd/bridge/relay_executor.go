package gateway

import (
	"context"
	"strings"
	"time"

	"github.com/webong/gateway/cmd/internal/forwarding"
	"github.com/webong/gateway/cmd/internal/workers"
)

// RelayExecutor adapts the Go transport implementation to the Go/PHP bridge.
// It contains no registry or route-binding logic.
type RelayExecutor struct {
	forwarder  *forwarding.Forwarder
	workerPool *workers.WorkerPool
	adapters   map[string]GatewayDeliveryAdapter
	queue      GatewayDeliveryQueue
	timeout    time.Duration
}

type RelayExecutorOption func(*RelayExecutor)

func WithGatewayDeliveryAdapter(name string, adapter GatewayDeliveryAdapter) RelayExecutorOption {
	return func(executor *RelayExecutor) {
		if adapter != nil && validAdapterName(name) {
			executor.adapters[name] = adapter
		}
	}
}

func WithGatewayDeliveryTimeout(timeout time.Duration) RelayExecutorOption {
	return func(executor *RelayExecutor) {
		if timeout > 0 {
			executor.timeout = timeout
		}
	}
}

func WithGatewayDeliveryQueue(queue GatewayDeliveryQueue) RelayExecutorOption {
	return func(executor *RelayExecutor) {
		executor.queue = queue
	}
}

func NewRelayExecutor(forwarder *forwarding.Forwarder, workerPool *workers.WorkerPool, options ...RelayExecutorOption) *RelayExecutor {
	executor := &RelayExecutor{
		forwarder:  forwarder,
		workerPool: workerPool,
		adapters:   make(map[string]GatewayDeliveryAdapter),
		timeout:    30 * time.Second,
	}
	for _, option := range options {
		if option != nil {
			option(executor)
		}
	}
	return executor
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
	if e == nil || delivery.Validate() != nil {
		return false
	}

	adapterName := delivery.AdapterName()
	if adapterName == string(ProtocolHTTP) {
		if e.workerPool == nil {
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
	queued := cloneGatewayDelivery(delivery)
	if e.queue != nil {
		return e.queue.EnqueueGateway(queued) == nil
	}

	adapter := e.adapters[adapterName]
	if adapter == nil || e.workerPool == nil {
		return false
	}
	return e.workerPool.SubmitTask(workers.Task{
		Label: strings.ToLower(adapterName) + ":" + delivery.Target,
		Run: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
			defer cancel()
			return adapter.DeliverGateway(ctx, queued)
		},
	})
}

func cloneGatewayDelivery(delivery GatewayDelivery) GatewayDelivery {
	delivery.Headers = cloneRelayHeaders(delivery.Headers)
	attributes := delivery.Attributes
	delivery.Attributes = make(map[string]string, len(delivery.Attributes))
	for key, value := range attributes {
		delivery.Attributes[key] = value
	}
	delivery.Payload = append([]byte(nil), delivery.Payload...)
	return delivery
}

func cloneRelayHeaders(headers map[string][]string) map[string][]string {
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

var _ Executor = (*RelayExecutor)(nil)
