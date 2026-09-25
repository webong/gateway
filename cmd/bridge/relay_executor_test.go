package gateway

import (
	"context"
	"testing"
	"time"

	relayconfig "github.com/webong/gateway/cmd/internal/config"
	"github.com/webong/gateway/cmd/internal/forwarding"
	"github.com/webong/gateway/cmd/internal/logging"
	"github.com/webong/gateway/cmd/internal/workers"
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
