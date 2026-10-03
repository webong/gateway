package deliveryqueue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

type statusReportingAdapter struct {
	status        int
	bodyAssertion string
}

func (adapter statusReportingAdapter) DeliverGateway(context.Context, bridge.GatewayDelivery) error {
	return nil
}
func (adapter statusReportingAdapter) DeliverGatewayResult(context.Context, bridge.GatewayDelivery) (bridge.GatewayDeliveryResult, error) {
	return bridge.GatewayDeliveryResult{ResponseStatus: adapter.status, BodyAssertion: adapter.bodyAssertion}, nil
}

func TestQueueRetainsStatusOnlyOnDeliveredReceipt(t *testing.T) {
	queue := newTestQueue(t, statusReportingAdapter{status: 204, bodyAssertion: "matched"}, nil)
	delivery := testDelivery()
	delivery.Attributes["event_id"] = "run-1"
	delivery.Attributes["expected_status"] = "204"
	if err := queue.EnqueueGateway(delivery); err != nil {
		t.Fatal(err)
	}
	receipt := waitForState(t, queue, StateDelivered)
	if receipt.EventID != "run-1" || receipt.ExpectedStatus != 204 || receipt.ResponseStatus != 204 || receipt.BodyAssertion != "matched" {
		t.Fatalf("missing status assertion metadata: %+v", receipt)
	}
	stored, err := readItem(filepath.Join(queue.deliveredDir, receipt.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Delivery.Target != "" || len(stored.Delivery.Payload) != 0 || len(stored.Delivery.Headers) != 0 {
		t.Fatalf("delivered receipt retained callback content: %+v", stored.Delivery)
	}
}

func TestIdempotentReceiptSurvivesDeliveryPruningAndRestart(t *testing.T) {
	path := t.TempDir()
	var attempts atomic.Int32
	adapter := bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		attempts.Add(1)
		return nil
	})
	config := testConfig(path, adapter)
	config.ReceiptRetention = time.Nanosecond
	first, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	fingerprint := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	expires := time.Now().Add(time.Hour)
	if replayed, err := first.EnqueueGatewayIdempotent(testDelivery(), id, fingerprint, expires); err != nil || replayed {
		t.Fatalf("first enqueue: replayed=%v err=%v", replayed, err)
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		status, err := first.Status(id)
		if err == nil && status.State == StateDelivered {
			break
		}
	}
	if status, err := first.Status(id); err != nil || status.State != StateDelivered {
		t.Fatalf("missing delivered receipt: %+v %v", status, err)
	}
	if removed, err := first.PruneReceipts(); err != nil || removed != 0 {
		t.Fatalf("idempotency receipt was pruned: %d %v", removed, err)
	}
	closeQueue(t, first)
	second, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, second) })
	if replayed, err := second.EnqueueGatewayIdempotent(testDelivery(), id, fingerprint, expires); err != nil || !replayed {
		t.Fatalf("restart did not reuse receipt: replayed=%v err=%v", replayed, err)
	}
	if _, err := second.EnqueueGatewayIdempotent(testDelivery(), id, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", expires); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed message did not conflict: %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("duplicate relay attempt: %d", attempts.Load())
	}
}

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

func TestQueueDoesNotRetryPermanentDeliveryError(t *testing.T) {
	var attempts atomic.Int32
	queue := newTestQueue(t, bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		attempts.Add(1)
		return bridge.ErrPermanentDelivery
	}), nil)
	if err := queue.EnqueueGateway(testDelivery()); err != nil {
		t.Fatal(err)
	}
	waitForCounts(t, queue, 0, 1)
	if attempts.Load() != 1 {
		t.Fatalf("permanent failure should take one attempt, got %d", attempts.Load())
	}
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

func TestQueueRejectsConcurrentUseOfTheSameSpool(t *testing.T) {
	configuration := testConfig(t.TempDir(), bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		return nil
	}))
	first, err := New(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(configuration); !errors.Is(err, ErrSpoolInUse) {
		t.Fatalf("expected concurrent spool ownership to fail, got %v", err)
	}
	closeQueue(t, first)
	second, err := New(configuration)
	if err != nil {
		t.Fatalf("spool could not be reopened after shutdown: %v", err)
	}
	closeQueue(t, second)
}

func TestQueueRetainsRedactedDeliveredReceipt(t *testing.T) {
	queue := newTestQueue(t, bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		return nil
	}), nil)
	delivery := testDelivery()
	delivery.SubscriberID = "subscriber-42"
	delivery.Headers = map[string][]string{"Authorization": {"Bearer secret"}}
	if err := queue.EnqueueGateway(delivery); err != nil {
		t.Fatal(err)
	}
	receipt := waitForState(t, queue, StateDelivered)
	if receipt.Protocol != "smtp" || receipt.Adapter != "smtp" || receipt.SubscriberID != "subscriber-42" || receipt.Attempts != 1 {
		t.Fatalf("unexpected delivered receipt: %+v", receipt)
	}
	stored, err := readItem(filepath.Join(queue.deliveredDir, receipt.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Delivery.Target != "" || len(stored.Delivery.Payload) != 0 || len(stored.Delivery.Headers) != 0 {
		t.Fatalf("delivered receipt retained delivery secrets: %+v", stored.Delivery)
	}
	listed, err := queue.List(StateDelivered, 10)
	if err != nil || len(listed) != 1 || listed[0].ID != receipt.ID {
		t.Fatalf("delivered receipt was not listable: %+v, %v", listed, err)
	}
	if _, err := queue.List(StateDelivered, 101); err == nil {
		t.Fatal("unbounded receipt list was accepted")
	}
	if err := queue.Retry(receipt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("successful delivery must not be replayed: %v", err)
	}
}

func TestQueueRetriesFailedDeliveryOnlyOnExplicitRequest(t *testing.T) {
	var attempts atomic.Int32
	queue := newTestQueue(t, bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		if attempts.Add(1) == 1 {
			return bridge.ErrPermanentDelivery
		}
		return nil
	}), nil)
	if err := queue.EnqueueGateway(testDelivery()); err != nil {
		t.Fatal(err)
	}
	failed := waitForState(t, queue, StateFailed)
	if failed.Attempts != 1 || attempts.Load() != 1 {
		t.Fatalf("expected one failed attempt: %+v, attempts=%d", failed, attempts.Load())
	}
	listed, err := queue.List(StateFailed, 10)
	if err != nil || len(listed) != 1 || listed[0].ID != failed.ID {
		t.Fatalf("failed delivery was not listable: %+v, %v", listed, err)
	}
	if err := queue.Retry("../" + failed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsafe delivery ID must be rejected: %v", err)
	}
	if err := queue.Retry(failed.ID); err != nil {
		t.Fatal(err)
	}
	delivered := waitForState(t, queue, StateDelivered)
	if delivered.ID != failed.ID || delivered.Attempts != 1 || attempts.Load() != 2 {
		t.Fatalf("expected one explicit redrive: %+v, attempts=%d", delivered, attempts.Load())
	}
}

func TestQueueRepairsTerminalPendingReceiptWithoutRedelivery(t *testing.T) {
	path := t.TempDir()
	if err := os.MkdirAll(filepath.Join(path, "pending"), 0o700); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	if err := writeItem(filepath.Join(path, "pending", id+".json"), item{
		Version: spoolVersion, ID: id, Protocol: bridge.ProtocolSMTP,
		Adapter: "smtp", State: StateDelivered, Attempts: 1,
	}); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	queue, err := New(testConfig(path, bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		attempts.Add(1)
		return nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, queue) })
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(queue.deliveredDir, id+".json")); err == nil {
			if attempts.Load() != 0 {
				t.Fatal("terminal receipt was delivered again")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("terminal pending receipt was not repaired")
}

func TestQueuePrunesOnlyExpiredDeliveredReceipts(t *testing.T) {
	configuration := testConfig(t.TempDir(), bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		return nil
	}))
	configuration.ReceiptRetention = time.Hour
	queue, err := New(configuration)
	if err != nil {
		t.Fatal(err)
	}
	closeQueue(t, queue)
	oldID := "0123456789abcdef0123456789abcdef"
	freshID := "fedcba9876543210fedcba9876543210"
	for id, updated := range map[string]time.Time{
		oldID:   time.Now().UTC().Add(-2 * time.Hour),
		freshID: time.Now().UTC(),
	} {
		if err := writeItem(filepath.Join(queue.deliveredDir, id+".json"), item{
			Version: spoolVersion, ID: id, State: StateDelivered, UpdatedAt: updated,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeItem(filepath.Join(queue.failedDir, oldID+".json"), item{
		Version: spoolVersion, ID: oldID, State: StateFailed, UpdatedAt: time.Now().UTC().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	removed, err := queue.PruneReceipts()
	if err != nil || removed != 1 {
		t.Fatalf("expected one expired receipt pruned, got %d: %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(queue.deliveredDir, oldID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old delivered receipt remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(queue.deliveredDir, freshID+".json")); err != nil {
		t.Fatalf("fresh receipt was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(queue.failedDir, oldID+".json")); err != nil {
		t.Fatalf("failed delivery was removed: %v", err)
	}
}

func TestQueueListsDeliveryByOriginEventAcrossStates(t *testing.T) {
	queue := newTestQueue(t, bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		return bridge.ErrPermanentDelivery
	}), nil)
	delivery := testDelivery()
	delivery.Attributes["event_id"] = "receipt-123"
	if err := queue.EnqueueGateway(delivery); err != nil {
		t.Fatal(err)
	}
	failed := waitForState(t, queue, StateFailed)
	statuses, err := queue.ListEvent("receipt-123", 10)
	if err != nil || len(statuses) != 1 || statuses[0].ID != failed.ID || statuses[0].EventID != "receipt-123" {
		t.Fatalf("expected correlated failed delivery: %+v, %v", statuses, err)
	}
	other, err := queue.ListEvent("other-receipt", 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("unrelated event returned statuses: %+v, %v", other, err)
	}
}

func TestDeliveredReceiptKeepsEventIDWithoutPayloadOrTarget(t *testing.T) {
	queue := newTestQueue(t, bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		return nil
	}), nil)
	delivery := testDelivery()
	delivery.Attributes["event_id"] = "receipt-456"
	if err := queue.EnqueueGateway(delivery); err != nil {
		t.Fatal(err)
	}
	delivered := waitForState(t, queue, StateDelivered)
	statuses, err := queue.ListEvent("receipt-456", 10)
	if err != nil || len(statuses) != 1 || statuses[0].ID != delivered.ID || statuses[0].EventID != "receipt-456" {
		t.Fatalf("delivered receipt lost its event ID: %+v, %v", statuses, err)
	}
	stored, err := readItem(filepath.Join(queue.deliveredDir, delivered.ID+".json"))
	if err != nil || stored.EventID != "receipt-456" || stored.Delivery.Target != "" || len(stored.Delivery.Payload) != 0 || len(stored.Delivery.Attributes) != 0 {
		t.Fatalf("delivered receipt retained private delivery data or lost event ID: %+v, %v", stored, err)
	}
}

func waitForState(t *testing.T, queue *Queue, state State) Status {
	t.Helper()
	directory := queue.pendingDir
	if state == StateFailed {
		directory = queue.failedDir
	} else if state == StateDelivered {
		directory = queue.deliveredDir
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		files, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if filepath.Ext(file.Name()) != ".json" {
				continue
			}
			status, err := queue.Status(file.Name()[:len(file.Name())-5])
			if err == nil && status.State == state {
				return status
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for delivery state %s", state)
	return Status{}
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
