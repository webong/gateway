package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

type interruptedFanoutQueue struct {
	seen     map[string]string
	failOnce bool
}

func (queue *interruptedFanoutQueue) EnqueueGatewayIdempotent(delivery bridge.GatewayDelivery, id, fingerprint string, _ time.Time) (bool, error) {
	if delivery.Target == "second@example.test" && queue.failOnce {
		queue.failOnce = false
		return false, errors.New("temporary spool failure")
	}
	if previous, ok := queue.seen[id]; ok {
		if previous != fingerprint {
			return false, errors.New("child receipt conflict")
		}
		return true, nil
	}
	queue.seen[id] = fingerprint
	return false, nil
}

func TestFanoutRetryResumesAfterOneChildWasEnqueued(t *testing.T) {
	queue := &interruptedFanoutQueue{seen: make(map[string]string), failOnce: true}
	adapter := newFanoutAdapter()
	adapter.bind(queue)
	parent := bridge.GatewayDelivery{Protocol: bridge.ProtocolSMTP, Adapter: "gateway.fanout", Target: "event-1",
		Payload: []byte("Subject: hello\r\n\r\nbody\r\n"), Batch: []bridge.GatewayDelivery{
			{Protocol: bridge.ProtocolHTTP, Target: "first@example.test"},
			{Protocol: bridge.ProtocolHTTP, Target: "second@example.test"},
		}}
	if err := adapter.DeliverGateway(context.Background(), parent); err == nil {
		t.Fatal("expected first expansion to fail")
	}
	if len(queue.seen) != 1 {
		t.Fatalf("first attempt should enqueue one child, got %d", len(queue.seen))
	}
	if err := adapter.DeliverGateway(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if len(queue.seen) != 2 {
		t.Fatalf("retry should leave exactly two child receipts, got %d", len(queue.seen))
	}
}
