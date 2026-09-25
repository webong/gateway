package deliveryqueue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	bridge "github.com/webong/gateway/cmd/bridge"
)

func TestQueuePersistsBeforeDeliveryAndRemovesAfterSuccess(t *testing.T) {
	delivered := make(chan bridge.GatewayDelivery, 1)
	release := make(chan struct{})
	events := make(chan Event, 4)
	queue := newTestQueue(t, bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, delivery bridge.GatewayDelivery) error {
		delivered <- delivery
		<-release
		return nil
	}), ObserverFunc(func(event Event) { events <- event }))

	if err := queue.EnqueueGateway(testDelivery()); err != nil {
		t.Fatal(err)
	}
	pending, failed, err := queue.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if pending != 1 || failed != 0 {
		t.Fatalf("delivery was not durably pending after enqueue: pending=%d failed=%d", pending, failed)
	}

	select {
	case delivery := <-delivered:
		if delivery.Target != "inbox@example.net" {
			t.Fatalf("unexpected delivery: %+v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivery")
	}
	close(release)
	waitForCounts(t, queue, 0, 0)

	states := []State{(<-events).State, (<-events).State}
	if states[0] != StateQueued || states[1] != StateDelivered {
		t.Fatalf("unexpected delivery states: %+v", states)
	}
}

func TestQueueRetriesAndEventuallyDelivers(t *testing.T) {
	var attempts atomic.Int32
	queue := newTestQueue(t, bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		if attempts.Add(1) < 3 {
			return errors.New("temporary relay failure")
		}
		return nil
	}), nil)

	if err := queue.EnqueueGateway(testDelivery()); err != nil {
		t.Fatal(err)
	}
	waitForCounts(t, queue, 0, 0)
	if attempts.Load() != 3 {
		t.Fatalf("expected three delivery attempts, got %d", attempts.Load())
	}
}

func TestQueueMovesTerminalFailureAside(t *testing.T) {
	queue := newTestQueue(t, bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		return errors.New("permanent relay failure")
	}), nil)
	queue.config.MaxAttempts = 2

	if err := queue.EnqueueGateway(testDelivery()); err != nil {
		t.Fatal(err)
	}
	waitForCounts(t, queue, 0, 1)
}

func TestQueueRecoversPendingDeliveryAfterRestart(t *testing.T) {
	path := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	first, err := New(testConfig(path, bridge.GatewayDeliveryAdapterFunc(func(ctx context.Context, _ bridge.GatewayDelivery) error {
		close(started)
		select {
		case <-release:
			return errors.New("shutdown")
		case <-ctx.Done():
			return ctx.Err()
		}
	})))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.EnqueueGateway(testDelivery()); err != nil {
		t.Fatal(err)
	}
	<-started
	close(release)
	closeQueue(t, first)

	delivered := make(chan struct{}, 1)
	second, err := New(testConfig(path, bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		delivered <- struct{}{}
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, second) })
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovered delivery")
	}
	waitForCounts(t, second, 0, 0)
}

func newTestQueue(t *testing.T, adapter bridge.GatewayDeliveryAdapter, observer Observer) *Queue {
	t.Helper()
	queue, err := New(Config{
		Path:            t.TempDir(),
		Adapters:        map[string]bridge.GatewayDeliveryAdapter{"smtp": adapter},
		Observer:        observer,
		MaxAttempts:     4,
		InitialBackoff:  5 * time.Millisecond,
		MaxBackoff:      10 * time.Millisecond,
		DeliveryTimeout: 100 * time.Millisecond,
		PollInterval:    5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, queue) })
	return queue
}

func testConfig(path string, adapter bridge.GatewayDeliveryAdapter) Config {
	return Config{
		Path:            path,
		Adapters:        map[string]bridge.GatewayDeliveryAdapter{"smtp": adapter},
		MaxAttempts:     4,
		InitialBackoff:  5 * time.Millisecond,
		MaxBackoff:      10 * time.Millisecond,
		DeliveryTimeout: 100 * time.Millisecond,
		PollInterval:    5 * time.Millisecond,
	}
}

func testDelivery() bridge.GatewayDelivery {
	return bridge.GatewayDelivery{
		Protocol:   bridge.ProtocolSMTP,
		Target:     "inbox@example.net",
		Attributes: map[string]string{"mail_from": "sender@example.test"},
		Payload:    []byte("Subject: durable\r\n\r\nmessage\r\n"),
	}
}

func waitForCounts(t *testing.T, queue *Queue, pending, failed int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		actualPending, actualFailed, err := queue.Counts()
		if err != nil {
			t.Fatal(err)
		}
		if actualPending == pending && actualFailed == failed {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	actualPending, actualFailed, _ := queue.Counts()
	t.Fatalf("timed out waiting for queue counts pending=%d failed=%d; got pending=%d failed=%d", pending, failed, actualPending, actualFailed)
}

func closeQueue(t *testing.T, queue *Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := queue.Close(ctx); err != nil {
		t.Errorf("close delivery queue: %v", err)
	}
}
