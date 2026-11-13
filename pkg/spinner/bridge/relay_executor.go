package gateway

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/webong/gateway/pkg/spinner/internal/forwarding"
	"github.com/webong/gateway/pkg/spinner/internal/workers"
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
	if delivery.EgressProtocol() != ProtocolHTTP || delivery.AdapterName() != string(ProtocolHTTP) {
		return Response{}, errors.New("synchronous reply delivery supports HTTP targets only")
	}
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
	method := delivery.Method
	if method == "" {
		method = http.MethodGet
	}
	if e.queue != nil {
		return e.queue.EnqueueGateway(GatewayDelivery{
			Protocol: delivery.EgressProtocol(), Adapter: delivery.Adapter, Target: delivery.URL, SubscriberID: delivery.SubscriberID,
			Headers: cloneRelayHeaders(delivery.Headers), Payload: append([]byte(nil), delivery.Body...),
			Attributes: deliveryAttributes(delivery, method),
			Transport:  delivery.Transport,
		}) == nil
	}
	if e.workerPool == nil {
		return false
	}
	if delivery.EgressProtocol() != ProtocolHTTP || delivery.AdapterName() != string(ProtocolHTTP) {
		adapter := e.adapters[delivery.AdapterName()]
		if adapter == nil {
			return false
		}
		gatewayDelivery := GatewayDelivery{
			Protocol: delivery.EgressProtocol(), Adapter: delivery.Adapter, Target: delivery.URL,
			SubscriberID: delivery.SubscriberID, Headers: cloneRelayHeaders(delivery.Headers),
			Attributes: deliveryAttributes(delivery, method), Payload: append([]byte(nil), delivery.Body...),
			Transport: delivery.Transport,
		}
		return e.workerPool.SubmitTask(workers.Task{
			Label: strings.ToLower(delivery.AdapterName()) + ":" + delivery.URL,
			Run: func() error {
				ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
				defer cancel()
				return adapter.DeliverGateway(ctx, gatewayDelivery)
			},
		})
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

func deliveryAttributes(delivery Delivery, method string) map[string]string {
	attributes := map[string]string{
		"method": method, "raw_query": delivery.RawQuery,
		"destination_id": delivery.ID, "event_id": delivery.EventID,
	}
	for name, values := range delivery.Headers {
		if strings.EqualFold(name, "Content-Type") && len(values) > 0 {
			attributes["content_type"] = values[0]
			break
		}
	}
	return attributes
}

func forwardTransport(transport *HTTPTransport) forwarding.HTTPTransport {
	return forwarding.TransportFromWire(transport)
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

func (e *RelayExecutor) EnqueueGatewayIdempotent(delivery GatewayDelivery, id, fingerprint string, expiresAt time.Time) (bool, error) {
	if e == nil || e.queue == nil {
		return false, errors.New("durable delivery queue is unavailable")
	}
	queue, ok := e.queue.(IdempotentGatewayExecutor)
	if !ok {
		return false, errors.New("durable delivery queue does not support reserved receipts")
	}
	return queue.EnqueueGatewayIdempotent(delivery, id, fingerprint, expiresAt)
}

func (e *RelayExecutor) SupportsAdapter(name string) bool {
	if e == nil || e.queue == nil {
		return false
	}
	support, ok := e.queue.(GatewayDeliveryAdapterSupport)
	return ok && support.SupportsAdapter(name)
}

// EnqueueGatewayBatch writes one protocol-neutral durable fanout manifest so
// a partial child enqueue cannot make an ingress adapter return a transient
// error after some destinations have already been scheduled.
func (e *RelayExecutor) EnqueueGatewayBatch(eventID string, deliveries []GatewayDelivery) bool {
	if e == nil || e.queue == nil || eventID == "" || len(deliveries) < 2 || len(deliveries) > 100 {
		return false
	}
	payload := deliveries[0].Payload
	if len(payload) == 0 {
		return false
	}
	children := make([]GatewayDelivery, len(deliveries))
	for index, delivery := range deliveries {
		if delivery.Validate() != nil || delivery.AdapterName() == "gateway.fanout" || !bytes.Equal(delivery.Payload, payload) {
			return false
		}
		children[index] = cloneGatewayDelivery(delivery)
		children[index].Payload = nil
	}
	batch := GatewayDelivery{Protocol: deliveries[0].Protocol, Adapter: "gateway.fanout", Target: eventID,
		Payload: append([]byte(nil), payload...), Batch: children,
		Attributes: map[string]string{"event_id": eventID}}
	return e.queue.EnqueueGateway(batch) == nil
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
