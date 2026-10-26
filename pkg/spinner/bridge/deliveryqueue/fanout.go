package deliveryqueue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

type fanoutEnqueuer interface {
	EnqueueGatewayIdempotent(bridge.GatewayDelivery, string, string, time.Time) (bool, error)
}

// fanoutAdapter expands one protocol-neutral durable manifest into
// independently tracked child receipts.
type fanoutAdapter struct{ queue fanoutEnqueuer }

func (adapter *fanoutAdapter) bind(queue fanoutEnqueuer) { adapter.queue = queue }

func (adapter *fanoutAdapter) DeliverGateway(ctx context.Context, parent bridge.GatewayDelivery) error {
	if adapter == nil || parent.Adapter != "gateway.fanout" || parent.Validate() != nil {
		return fmt.Errorf("%w: invalid fanout manifest", bridge.ErrPermanentDelivery)
	}
	if adapter.queue == nil {
		return errors.New("fanout queue is unavailable")
	}
	for index, child := range parent.Batch {
		if err := ctx.Err(); err != nil {
			return err
		}
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
		if _, err := adapter.queue.EnqueueGatewayIdempotent(child, id, hex.EncodeToString(fingerprint[:]), time.Now().UTC().Add(30*24*time.Hour)); err != nil {
			return fmt.Errorf("enqueue fanout child %d: %w", index, err)
		}
	}
	return nil
}

func installFanout(config *Config) (*fanoutAdapter, error) {
	if config.Adapters["gateway.fanout"] != nil {
		return nil, errors.New("gateway.fanout is reserved by the delivery queue")
	}
	adapter := &fanoutAdapter{}
	config.Adapters["gateway.fanout"] = adapter
	return adapter, nil
}
