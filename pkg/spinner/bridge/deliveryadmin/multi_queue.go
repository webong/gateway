package deliveryadmin

import (
	"errors"
	"fmt"
	"sort"

	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
)

// MultiQueue exposes protocol queues behind one private admin API. The queues
// may have separate local spools or share a PostgreSQL store while retaining
// disjoint worker adapter sets.
type MultiQueue struct {
	queues []queue
}

func NewMultiQueue(queues ...queue) *MultiQueue {
	active := make([]queue, 0, len(queues))
	for _, queue := range queues {
		if queue != nil {
			active = append(active, queue)
		}
	}
	return &MultiQueue{queues: active}
}

func (multi *MultiQueue) Status(id string) (deliveryqueue.Status, error) {
	for _, queue := range multi.queues {
		status, err := queue.Status(id)
		if err == nil {
			return status, nil
		}
		if !errors.Is(err, deliveryqueue.ErrNotFound) {
			return deliveryqueue.Status{}, err
		}
	}
	return deliveryqueue.Status{}, deliveryqueue.ErrNotFound
}

func (multi *MultiQueue) List(state deliveryqueue.State, limit int) ([]deliveryqueue.Status, error) {
	items := make([]deliveryqueue.Status, 0, limit*len(multi.queues))
	indices := make(map[string]int)
	for _, queue := range multi.queues {
		statuses, err := queue.List(state, limit)
		if err != nil {
			return nil, err
		}
		items = appendUnique(items, indices, statuses)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (multi *MultiQueue) ListEvent(eventID string, limit int) ([]deliveryqueue.Status, error) {
	items := make([]deliveryqueue.Status, 0, limit*len(multi.queues))
	indices := make(map[string]int)
	for _, queue := range multi.queues {
		statuses, err := queue.ListEvent(eventID, limit)
		if err != nil {
			return nil, err
		}
		items = appendUnique(items, indices, statuses)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (multi *MultiQueue) Retry(id string) error {
	foundAdapter := ""
	for _, queue := range multi.queues {
		status, err := queue.Status(id)
		if err == nil {
			foundAdapter = status.Adapter
			if capable, ok := queue.(interface{ SupportsAdapter(string) bool }); ok && !capable.SupportsAdapter(status.Adapter) {
				continue
			}
			return queue.Retry(id)
		}
		if !errors.Is(err, deliveryqueue.ErrNotFound) {
			return err
		}
	}
	if foundAdapter != "" {
		return fmt.Errorf("delivery adapter %q is not registered", foundAdapter)
	}
	return deliveryqueue.ErrNotFound
}

func appendUnique(items []deliveryqueue.Status, indices map[string]int, statuses []deliveryqueue.Status) []deliveryqueue.Status {
	for _, status := range statuses {
		if index, present := indices[status.ID]; present {
			if status.UpdatedAt.After(items[index].UpdatedAt) {
				items[index] = status
			}
			continue
		}
		indices[status.ID] = len(items)
		items = append(items, status)
	}
	return items
}
