package deliveryqueue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

func failureFollowUpID(originalID string) string {
	digest := sha256.Sum256([]byte("gateway-delivery-follow-up:v1:" + originalID))
	return hex.EncodeToString(digest[:16])
}

func failureFollowUp(ctx context.Context, config Config, original item) (item, item, error) {
	if original.FollowUpHandled {
		return original, item{}, nil
	}
	resolver := config.FailureHandlerResolver
	if resolver == nil {
		original.FollowUpHandled = true
		return original, item{}, nil
	}
	handler, ok := resolver.ResolveDeliveryFailureHandler(original.Delivery.AdapterName())
	if !ok {
		original.FollowUpHandled = true
		return original, item{}, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, config.DeliveryTimeout)
	delivery, err := handler.FollowUp(callCtx, bridge.DeliveryFailureRequest{
		ID: original.ID, EventID: original.EventID, Delivery: original.Delivery,
		Attempts: original.Attempts, CreatedAt: original.CreatedAt, FailedAt: original.UpdatedAt,
	})
	cancel()
	if err != nil {
		return original, item{}, err
	}
	original.FollowUpHandled = true
	if delivery == nil {
		return original, item{}, nil
	}
	if err := delivery.Validate(); err != nil {
		return original, item{}, errors.New("delivery failure Extension returned an invalid follow-up")
	}
	if _, ok := resolveAdapter(config, delivery.AdapterName()); !ok {
		return original, item{}, errors.New("delivery failure follow-up adapter is unavailable")
	}
	id := failureFollowUpID(original.ID)
	now := time.Now().UTC()
	original.FollowUpID = id
	return original, item{
		Version: spoolVersion, ID: id, EventID: original.EventID, Delivery: *delivery,
		Protocol: delivery.Protocol, Adapter: delivery.AdapterName(), SubscriberID: delivery.SubscriberID,
		State: StateQueued, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (queue *Queue) failureFollowUpExists(id string) (bool, error) {
	for _, directory := range []string{queue.pendingDir, queue.failedDir, queue.deliveredDir} {
		_, err := os.Stat(filepath.Join(directory, id+".json"))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

// ensureFailureFollowUp is idempotent across a crash between enqueueing an
// Extension-produced follow-up and marking the original failed item.
func (queue *Queue) ensureFailureFollowUp(original item) error {
	if original.FollowUpHandled {
		return nil
	}
	queue.transitionMu.Lock()
	defer queue.transitionMu.Unlock()
	path := filepath.Join(queue.failedDir, original.ID+".json")
	current, err := readItem(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || current.State != StateFailed || current.FollowUpHandled {
		return err
	}
	id := failureFollowUpID(current.ID)
	exists, err := queue.failureFollowUpExists(id)
	if err != nil {
		return err
	}
	if exists {
		current.FollowUpHandled = true
		current.FollowUpID = id
		return writeItem(path, current)
	}
	current, followUp, err := failureFollowUp(context.Background(), queue.config, current)
	if err != nil {
		return err
	}
	if followUp.ID != "" {
		if err := writeItem(filepath.Join(queue.pendingDir, followUp.ID+".json"), followUp); err != nil {
			return err
		}
		queue.observe(followUp, StateQueued)
		select {
		case queue.wake <- struct{}{}:
		default:
		}
	}
	return writeItem(path, current)
}

func (queue *Queue) processFailureFollowUps() error {
	entries, err := os.ReadDir(queue.failedDir)
	if err != nil {
		return err
	}
	var firstError error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		original, err := readItem(filepath.Join(queue.failedDir, entry.Name()))
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		if err := queue.ensureFailureFollowUp(original); err != nil && firstError == nil {
			firstError = err
		}
	}
	return firstError
}
