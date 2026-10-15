package transport

import (
	"context"
	"testing"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/cmd/bridge/deliveryqueue"
	"github.com/webong/gateway/src/spinner/internal/config"
	"github.com/webong/gateway/src/spinner/internal/forwarding"
	"github.com/webong/gateway/src/spinner/internal/logging"
)

func TestSMTPFanoutAcceptsOneDurableParentAndKeepsPerDestinationReceipts(t *testing.T) {
	called := make(chan string, 2)
	for _, name := range []string{"provider.fanout_a", "provider.fanout_b"} {
		name := name
		if err := RegisterDeliveryAdapterFactory(name, func(*config.Config) (bridge.GatewayDeliveryAdapter, error) {
			return bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error { called <- name; return nil }), nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	settings := &config.Config{DeliverySpoolPath: t.TempDir(), DeliveryMaxAttempts: 3,
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
	executor := bridge.NewRelayExecutor(nil, nil, bridge.WithGatewayDeliveryQueue(queue))
	message := []byte("Subject: fanout\r\n\r\nbody\r\n")
	var deliveries []bridge.GatewayDelivery
	for _, name := range []string{"provider.fanout_a", "provider.fanout_b"} {
		deliveries = append(deliveries, bridge.GatewayDelivery{Protocol: bridge.ProtocolSMTP,
			Adapter: name, Target: "support@example.test", Payload: message,
			Attributes: map[string]string{"event_id": "fanout-event"}})
	}
	if !executor.EnqueueGatewayBatch("fanout-event", deliveries) {
		t.Fatal("SMTP fanout was not durably accepted")
	}
	for range 2 {
		select {
		case <-called:
		case <-time.After(2 * time.Second):
			t.Fatal("one provider did not receive mail")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		statuses, err := queue.ListEvent("fanout-event", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(statuses) == 3 {
			var delivered int
			for _, status := range statuses {
				if status.State == deliveryqueue.StateDelivered {
					delivered++
				}
			}
			if delivered == 3 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fanout parent and children did not retain completed receipts")
}
