package gateway

import (
	"context"
	"testing"
	"time"

	relayconfig "github.com/webong/gateway/src/spinner/internal/config"
	"github.com/webong/gateway/src/spinner/internal/forwarding"
	"github.com/webong/gateway/src/spinner/internal/logging"
	"github.com/webong/gateway/src/spinner/internal/workers"
)

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

func TestWebstreamSourceHeadersPreferEndpointProfileCredentials(t *testing.T) {
	headers := streamHeaders(GatewayDelivery{
		Protocol: ProtocolHTTP, Target: "https://events.example.test/stream",
		Headers:   map[string][]string{"Last-Event-ID": {"event-42"}, "Authorization": {"Bearer client-token"}},
		Transport: &HTTPTransport{StreamHeaders: map[string][]string{"Authorization": {"Bearer source-secret"}, "X-Source-Scope": {"orders"}}},
	})
	if headers["Authorization"][0] != "Bearer source-secret" || headers["Last-Event-ID"][0] != "event-42" || headers["X-Source-Scope"][0] != "orders" {
		t.Fatalf("unexpected source headers: %v", headers)
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
