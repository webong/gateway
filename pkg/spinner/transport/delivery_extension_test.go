package transport

import (
	"context"
	"testing"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
	"github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/forwarding"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
)

func TestBundledDeliveryQueueRunsRegisteredProviderAdapter(t *testing.T) {
	name := "provider.zorvia_fixture"
	received := make(chan bridge.GatewayDelivery, 1)
	err := RegisterDeliveryAdapterFactory(name, func(*config.Config) (bridge.GatewayDeliveryAdapter, error) {
		return bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, delivery bridge.GatewayDelivery) error {
			received <- delivery
			return nil
		}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := &config.Config{DeliverySpoolPath: t.TempDir(), DeliveryMaxAttempts: 2,
		DeliveryInitialBackoff: 10 * time.Millisecond, DeliveryMaxBackoff: time.Second,
		DeliveryPollInterval: 10 * time.Millisecond, RequestTimeout: time.Second}
	queue, err := NewDeliveryQueue(settings, forwarding.NewForwarder(settings, logging.NewLogger("error")), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = queue.Close(ctx)
	}()
	message := []byte("Subject: hello\r\n\r\nbody\r\n")
	if err := queue.EnqueueGateway(bridge.GatewayDelivery{Protocol: bridge.ProtocolSMTP, Adapter: name,
		Target: "support@example.test", Payload: message}); err != nil {
		t.Fatal(err)
	}
	select {
	case delivered := <-received:
		if delivered.Target != "support@example.test" || string(delivered.Payload) != string(message) {
			t.Fatalf("provider received the wrong delivery: %+v", delivered)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider adapter did not receive the queued message")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statuses, err := queue.List(deliveryqueue.StateDelivered, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(statuses) == 1 && statuses[0].Adapter == name {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("provider delivery had no completed receipt")
}
