package deliveryqueue

import (
	"context"
	"errors"
	"testing"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

type failureResolver map[string]bridge.DeliveryFailureHandler

func (resolver failureResolver) ResolveDeliveryFailureHandler(name string) (bridge.DeliveryFailureHandler, bool) {
	handler, ok := resolver[name]
	return handler, ok
}

type failureHandlerFunc func(context.Context, bridge.DeliveryFailureRequest) (*bridge.GatewayDelivery, error)

func (function failureHandlerFunc) FollowUp(ctx context.Context, request bridge.DeliveryFailureRequest) (*bridge.GatewayDelivery, error) {
	return function(ctx, request)
}

func TestExtensionOwnsTerminalFailureFollowUp(t *testing.T) {
	followUps := make(chan bridge.GatewayDelivery, 1)
	queue, err := New(Config{
		Path: t.TempDir(), MaxAttempts: 1, PollInterval: 5 * time.Millisecond,
		DeliveryTimeout: time.Second,
		Adapters: map[string]bridge.GatewayDeliveryAdapter{
			"example.primary": bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
				return bridge.ErrPermanentDelivery
			}),
			"example.follow-up": bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, delivery bridge.GatewayDelivery) error {
				followUps <- delivery
				return nil
			}),
		},
		FailureHandlerResolver: failureResolver{"example.primary": failureHandlerFunc(func(_ context.Context, request bridge.DeliveryFailureRequest) (*bridge.GatewayDelivery, error) {
			if request.Attempts != 1 || request.Delivery.Target != "primary.example" || request.ID == "" {
				t.Fatalf("unexpected failure request: %+v", request)
			}
			return &bridge.GatewayDelivery{
				Protocol: bridge.ProtocolHTTP, Adapter: "example.follow-up", Target: "follow-up.example",
				Payload: []byte("extension-owned"),
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, queue) })
	id, err := queue.EnqueueGatewayWithID(bridge.GatewayDelivery{
		Protocol: bridge.ProtocolHTTP, Adapter: "example.primary", Target: "primary.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var failed Status
	for time.Now().Before(deadline) {
		failed, err = queue.Status(id)
		if err == nil && failed.State == StateFailed && failed.FollowUpID != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if failed.State != StateFailed || failed.FollowUpID != failureFollowUpID(id) {
		t.Fatalf("terminal failure did not persist its follow-up: %+v", failed)
	}
	select {
	case followUp := <-followUps:
		if string(followUp.Payload) != "extension-owned" {
			t.Fatalf("unexpected follow-up: %+v", followUp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up was not delivered")
	}
}

func TestMissingFailureHandlerCompletesWithoutProductFallback(t *testing.T) {
	queue, err := New(Config{
		Path: t.TempDir(), MaxAttempts: 1, PollInterval: 5 * time.Millisecond,
		Adapters: map[string]bridge.GatewayDeliveryAdapter{
			"example.primary": bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
				return errors.New("failed")
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, queue) })
	id, err := queue.EnqueueGatewayWithID(bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Adapter: "example.primary", Target: "primary.example"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := queue.Status(id)
		if statusErr == nil && status.State == StateFailed {
			if status.FollowUpID != "" {
				t.Fatalf("generic queue invented a product follow-up: %+v", status)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("delivery did not fail")
}
