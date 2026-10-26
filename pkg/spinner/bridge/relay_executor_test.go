package gateway

import (
	"context"
	"testing"
	"time"

	relayconfig "github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/forwarding"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
	"github.com/webong/gateway/pkg/spinner/internal/workers"
)

type capturingQueue struct{ deliveries []GatewayDelivery }

func (queue *capturingQueue) EnqueueGateway(delivery GatewayDelivery) error {
	queue.deliveries = append(queue.deliveries, delivery)
	return nil
}

func TestRelayExecutorDurablyEnqueuesHTTPDeliveries(t *testing.T) {
	queue := &capturingQueue{}
	executor := NewRelayExecutor(nil, nil, WithGatewayDeliveryQueue(queue))
	if !executor.DurableGatewayDeliveries() || NewRelayExecutor(nil, nil).DurableGatewayDeliveries() {
		t.Fatal("durable delivery capability did not reflect the configured spool")
	}
	regular := Delivery{
		ID: "destination-1", EventID: "run-1", SubscriberID: "subscriber-1", URL: "https://example.test/callback",
		Method: "PUT", RawQuery: "source=ingress", Headers: map[string][]string{"X-Test": {"original"}},
		Body: []byte("payload"), Transport: &HTTPTransport{RequestTimeoutSeconds: 3},
	}
	if !executor.Enqueue(regular) {
		t.Fatal("regular HTTP delivery was not sent to the durable queue")
	}
	regular.Headers["X-Test"][0] = "changed"
	regular.Body[0] = 'X'
	if len(queue.deliveries) != 1 {
		t.Fatalf("expected one queued delivery, got %d", len(queue.deliveries))
	}
	queued := queue.deliveries[0]
	if queued.Protocol != ProtocolHTTP || queued.Target != "https://example.test/callback" || queued.SubscriberID != "subscriber-1" || queued.Attributes["destination_id"] != "destination-1" || queued.Attributes["event_id"] != "run-1" || queued.Attributes["method"] != "PUT" || queued.Attributes["raw_query"] != "source=ingress" || queued.Headers["X-Test"][0] != "original" || string(queued.Payload) != "payload" || queued.Transport.RequestTimeoutSeconds != 3 {
		t.Fatalf("HTTP delivery was not preserved: %+v", queued)
	}
	protocol := GatewayDelivery{Protocol: ProtocolHTTP, Target: "https://example.test/protocol", Attributes: map[string]string{"method": "PATCH"}}
	if !executor.EnqueueGateway(protocol) || len(queue.deliveries) != 2 || queue.deliveries[1].Target != protocol.Target {
		t.Fatal("protocol HTTP delivery bypassed the durable queue")
	}
}

func TestRelayExecutorDispatchesRegisteredProtocolAdapter(t *testing.T) {
	logger := logging.NewLogger("error")
	forwarder := forwarding.NewForwarder(&relayconfig.Config{
		RequestTimeout:  time.Second,
		MaxBodySize:     1024,
		MaxIdleConns:    1,
		MaxConnsPerHost: 1,
		IdleConnTimeout: time.Second,
	}, logger)
	pool := workers.NewWorkerPool(1, 1, forwarder, logger)
	t.Cleanup(pool.Shutdown)

	received := make(chan GatewayDelivery, 1)
	executor := NewRelayExecutor(
		forwarder,
		pool,
		WithGatewayDeliveryAdapter(string(ProtocolSMTP), GatewayDeliveryAdapterFunc(func(_ context.Context, delivery GatewayDelivery) error {
			received <- delivery
			return nil
		})),
	)
	delivery := GatewayDelivery{
		Protocol:   ProtocolSMTP,
		Target:     "inbox@example.net",
		Attributes: map[string]string{"mail_from": "sender@example.test"},
		Payload:    []byte("Subject: hello\r\n\r\nmessage\r\n"),
	}
	if !executor.EnqueueGateway(delivery) {
		t.Fatal("expected SMTP delivery to be queued")
	}
	delivery.Attributes["mail_from"] = "changed@example.test"
	delivery.Payload[0] = 'X'

	select {
	case delivered := <-received:
		if delivered.Attributes["mail_from"] != "sender@example.test" {
			t.Fatalf("queued delivery attributes were not isolated: %+v", delivered.Attributes)
		}
		if string(delivered.Payload) != "Subject: hello\r\n\r\nmessage\r\n" {
			t.Fatalf("queued delivery payload was not isolated: %q", delivered.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SMTP delivery")
	}
}

func TestRelayExecutorDispatchesNamedExtensionAdapter(t *testing.T) {
	logger := logging.NewLogger("error")
	forwarder := forwarding.NewForwarder(&relayconfig.Config{
		RequestTimeout:  time.Second,
		MaxBodySize:     1024,
		MaxIdleConns:    1,
		MaxConnsPerHost: 1,
		IdleConnTimeout: time.Second,
	}, logger)
	pool := workers.NewWorkerPool(1, 1, forwarder, logger)
	t.Cleanup(pool.Shutdown)

	received := make(chan GatewayDelivery, 1)
	executor := NewRelayExecutor(forwarder, pool,
		WithGatewayDeliveryAdapter("example.mailbox", GatewayDeliveryAdapterFunc(func(_ context.Context, delivery GatewayDelivery) error {
			received <- delivery
			return nil
		})),
	)
	if !executor.EnqueueGateway(GatewayDelivery{
		Protocol: ProtocolSMTP,
		Adapter:  "example.mailbox",
		Target:   "account-42",
		Payload:  []byte("Subject: stored\r\n\r\nmessage\r\n"),
	}) {
		t.Fatal("expected named mailbox adapter delivery to be queued")
	}

	select {
	case delivery := <-received:
		if delivery.Adapter != "example.mailbox" || delivery.Target != "account-42" {
			t.Fatalf("unexpected extension delivery: %+v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for extension delivery")
	}
}

func TestRelayExecutorRejectsUnregisteredProtocolAdapter(t *testing.T) {
	executor := NewRelayExecutor(nil, nil)
	if executor.EnqueueGateway(GatewayDelivery{Protocol: ProtocolSMTP, Target: "inbox@example.net"}) {
		t.Fatal("expected unconfigured SMTP delivery to be rejected")
	}
}

func TestForwardTransportCopiesNormalizedProfileSettings(t *testing.T) {
	transport := &HTTPTransport{
		Proxy: &HTTPProxy{
			HTTP:    "http://proxy.example.test:8080",
			HTTPS:   "socks5h://proxy.example.test:1080",
			NoProxy: []string{".internal.example.test"},
		},
		ConnectTimeoutSeconds: 2.5,
		RequestTimeoutSeconds: 12,
	}

	forward := forwardTransport(transport)
	transport.Proxy.NoProxy[0] = "changed.example.test"
	if forward.HTTPProxy != "http://proxy.example.test:8080" || forward.HTTPSProxy != "socks5h://proxy.example.test:1080" {
		t.Fatalf("unexpected proxy mapping: %+v", forward)
	}
	if len(forward.NoProxy) != 1 || forward.NoProxy[0] != ".internal.example.test" {
		t.Fatalf("no-proxy values were not copied: %+v", forward.NoProxy)
	}
	if forward.ConnectTimeout != 2500*time.Millisecond || forward.RequestTimeout != 12*time.Second {
		t.Fatalf("unexpected timeout mapping: %+v", forward)
	}
}

func TestDeliveryRejectsMalformedHTTPTransport(t *testing.T) {
	delivery := Delivery{
		URL: "https://subscriber.example.test/hook",
		Transport: &HTTPTransport{
			Proxy: &HTTPProxy{HTTPS: "ftp://proxy.example.test"},
		},
	}
	if err := delivery.Validate(); err == nil {
		t.Fatal("expected unsupported proxy scheme to be rejected")
	}
}
