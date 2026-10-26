package deliveryqueue

import (
	"context"
	"testing"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

func TestFanoutPersistsOneParentAndPerDestinationReceipts(t *testing.T) {
	called := make(chan string, 2)
	adapters := make(map[string]bridge.GatewayDeliveryAdapter)
	for _, name := range []string{"provider.fanout_a", "provider.fanout_b"} {
		name := name
		adapters[name] = bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
			called <- name
			return nil
		})
	}
	queue, err := New(Config{Path: t.TempDir(), Adapters: adapters,
		MaxAttempts: 3, InitialBackoff: 10 * time.Millisecond, MaxBackoff: time.Second,
		PollInterval: 10 * time.Millisecond, DeliveryTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, queue) })
	payload := []byte("shared payload")
	children := []bridge.GatewayDelivery{
		{Protocol: bridge.ProtocolHTTP, Adapter: "provider.fanout_a", Target: "https://first.example.test", Attributes: map[string]string{"event_id": "fanout-event"}},
		{Protocol: bridge.ProtocolHTTP, Adapter: "provider.fanout_b", Target: "https://second.example.test", Attributes: map[string]string{"event_id": "fanout-event"}},
	}
	parent := bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Adapter: "gateway.fanout", Target: "fanout-event", Payload: payload, Batch: children, Attributes: map[string]string{"event_id": "fanout-event"}}
	if err := queue.EnqueueGateway(parent); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-called:
		case <-time.After(2 * time.Second):
			t.Fatal("one fanout destination was not delivered")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statuses, err := queue.ListEvent("fanout-event", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(statuses) == 3 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fanout parent and children did not retain receipts")
}
