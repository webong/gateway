package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

type fanoutEnqueuer interface {
	EnqueueGatewayIdempotent(bridge.GatewayDelivery, string, string, time.Time) (bool, error)
}

// fanoutAdapter expands one durable SMTP manifest into independently tracked
// child receipts. Deterministic child IDs make a partial expansion safe to
// retry after a process crash or transient queue failure.
type fanoutAdapter struct {
	ready chan struct{}
	queue fanoutEnqueuer
}

func newFanoutAdapter() *fanoutAdapter { return &fanoutAdapter{ready: make(chan struct{})} }

func (adapter *fanoutAdapter) bind(queue fanoutEnqueuer) {
	adapter.queue = queue
	close(adapter.ready)
}

func (adapter *fanoutAdapter) DeliverGateway(ctx context.Context, parent bridge.GatewayDelivery) error {
	if adapter == nil || parent.Adapter != "gateway.fanout" || parent.Validate() != nil {
		return fmt.Errorf("%w: invalid fanout manifest", bridge.ErrPermanentDelivery)
	}
	select {
	case <-adapter.ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	if adapter.queue == nil {
		return errors.New("fanout queue is unavailable")
	}
	for index, child := range parent.Batch {
		child.Payload = append([]byte(nil), parent.Payload...)
		if child.Attributes == nil {
			child.Attributes = make(map[string]string)
		}
		if child.Attributes["event_id"] == "" {
			child.Attributes["event_id"] = parent.Target
		}
		fingerprintBody, err := json.Marshal(child)
		if err != nil {
			return fmt.Errorf("%w: invalid fanout child", bridge.ErrPermanentDelivery)
		}
		fingerprint := sha256.Sum256(fingerprintBody)
		key := sha256.Sum256([]byte(parent.Target + "\x00" + strconv.Itoa(index)))
		id := hex.EncodeToString(key[:16])
		_, err = adapter.queue.EnqueueGatewayIdempotent(child, id, hex.EncodeToString(fingerprint[:]), time.Now().UTC().Add(30*24*time.Hour))
		if err != nil {
			return fmt.Errorf("enqueue fanout child %d: %w", index, err)
		}
	}
	return nil
}

var _ bridge.GatewayDeliveryAdapter = (*fanoutAdapter)(nil)
