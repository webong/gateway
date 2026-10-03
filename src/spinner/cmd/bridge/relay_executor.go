package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/webong/gateway/src/spinner/internal/forwarding"
	"github.com/webong/gateway/src/spinner/internal/workers"
)

// OpenWebstream opens a Planner-authorized long-lived HTTP source. Unlike a
// webhook reply, the response body is not buffered and follows ctx lifetime.
func (e *RelayExecutor) OpenWebstream(ctx context.Context, delivery GatewayDelivery) (*http.Response, error) {
	if e == nil || e.forwarder == nil {
		return nil, fmt.Errorf("webstream transport is not configured")
	}
	return e.forwarder.OpenStream(ctx, delivery.Target, streamHeaders(delivery), forwardTransport(delivery.Transport))
}

func streamHeaders(delivery GatewayDelivery) http.Header {
	headers := cloneRelayHeaders(delivery.Headers)
	if delivery.Transport != nil {
		for name, values := range delivery.Transport.StreamHeaders {
			headers[name] = append([]string(nil), values...)
		}
	}
	return headers
}

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
		Transport:   forwardTransport(delivery.Transport),
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
	if e == nil || delivery.Validate() != nil {
		return false
	}
	if e.queue != nil {
		method := delivery.Method
		if method == "" {
			method = http.MethodGet
		}
		return e.queue.EnqueueGateway(GatewayDelivery{
			Protocol: ProtocolHTTP, Target: delivery.URL, SubscriberID: delivery.SubscriberID,
			Headers: cloneRelayHeaders(delivery.Headers), Payload: append([]byte(nil), delivery.Body...),
			Attributes: map[string]string{"method": method, "raw_query": delivery.RawQuery, "destination_id": delivery.ID, "event_id": delivery.EventID},
			Transport:  delivery.Transport,
		}) == nil
	}
	if e.workerPool == nil {
		return false
	}
	return e.workerPool.Submit(forwarding.ForwardRequest{
		TargetURL:   delivery.URL,
		RequestBody: append([]byte(nil), delivery.Body...),
		Method:      delivery.Method,
		RawQuery:    delivery.RawQuery,
		Headers:     cloneRelayHeaders(delivery.Headers),
		Transport:   forwardTransport(delivery.Transport),
	})
}

func forwardTransport(transport *HTTPTransport) forwarding.HTTPTransport {
	if transport == nil {
		return forwarding.HTTPTransport{}
	}
	forward := forwarding.HTTPTransport{
		ConnectTimeout: secondsDuration(transport.ConnectTimeoutSeconds),
		RequestTimeout: secondsDuration(transport.RequestTimeoutSeconds),
	}
	if transport.Proxy != nil {
		forward.HTTPProxy = transport.Proxy.HTTP
		forward.HTTPSProxy = transport.Proxy.HTTPS
		forward.NoProxy = append([]string(nil), transport.Proxy.NoProxy...)
	}
	return forward
}

func secondsDuration(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func (e *RelayExecutor) EnqueueGateway(delivery GatewayDelivery) bool {
	if e == nil || delivery.Validate() != nil {
		return false
	}

	adapterName := delivery.AdapterName()
	queued := cloneGatewayDelivery(delivery)
	if e.queue != nil {
		return e.queue.EnqueueGateway(queued) == nil
	}
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
			Transport:   forwardTransport(delivery.Transport),
		})
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

func (e *RelayExecutor) DurableGatewayDeliveries() bool {
	return e != nil && e.queue != nil
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
