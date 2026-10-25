package integration

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const ObservationVersion = "v1"

var observationType = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)

// Observation is an immutable, language-neutral fact emitted after an
// integration has selected an outcome. Attributes are deliberately bounded;
// protocol libraries and unbounded payloads do not cross this interface.
type Observation struct {
	Version    string            `json:"version"`
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	OccurredAt time.Time         `json:"occurred_at"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

func (o Observation) Validate() error {
	if o.Version != ObservationVersion {
		return fmt.Errorf("unsupported observation version %q", o.Version)
	}
	if strings.TrimSpace(o.ID) == "" || len(o.ID) > 200 {
		return fmt.Errorf("observation ID is required and must not exceed 200 bytes")
	}
	if !observationType.MatchString(o.Type) {
		return fmt.Errorf("invalid observation type %q", o.Type)
	}
	if o.OccurredAt.IsZero() {
		return fmt.Errorf("observation occurrence time is required")
	}
	if len(o.Attributes) > 64 {
		return fmt.Errorf("observation attributes exceed 64 entries")
	}
	for key, value := range o.Attributes {
		if strings.TrimSpace(key) == "" || len(key) > 64 || len(value) > 4096 {
			return fmt.Errorf("observation attribute %q exceeds its bounds", key)
		}
	}
	return nil
}

// ObservationSink is the narrow host interface through which integrations
// submit already-selected outcomes to Planner-owned observers.
type ObservationSink interface {
	RecordObservation(context.Context, Observation) error
}

type ObservationQueueConfig struct {
	Capacity        int
	DeliveryTimeout time.Duration
}

type ObservationQueueStats struct {
	Accepted  uint64
	Dropped   uint64
	Failed    uint64
	Delivered uint64
	Depth     int
}

// ObservationQueue separates answer production from observation delivery. Its
// Enqueue method never waits for the sink; overload is represented as a drop.
type ObservationQueue struct {
	sink    ObservationSink
	input   chan Observation
	timeout time.Duration
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once

	accepted  atomic.Uint64
	dropped   atomic.Uint64
	failed    atomic.Uint64
	delivered atomic.Uint64
	closed    atomic.Bool
}

func NewObservationQueue(parent context.Context, sink ObservationSink, config ObservationQueueConfig) (*ObservationQueue, error) {
	if sink == nil {
		return nil, fmt.Errorf("observation sink is required")
	}
	if config.Capacity < 1 {
		return nil, fmt.Errorf("observation queue capacity must be positive")
	}
	if config.DeliveryTimeout <= 0 {
		return nil, fmt.Errorf("observation delivery timeout must be positive")
	}
	ctx, cancel := context.WithCancel(parent)
	queue := &ObservationQueue{
		sink: sink, input: make(chan Observation, config.Capacity), timeout: config.DeliveryTimeout,
		cancel: cancel, done: make(chan struct{}),
	}
	go queue.run(ctx)
	return queue, nil
}

func (q *ObservationQueue) Enqueue(observation Observation) bool {
	if q == nil || q.closed.Load() || observation.Validate() != nil {
		if q != nil {
			q.dropped.Add(1)
		}
		return false
	}
	observation.Attributes = cloneObservationAttributes(observation.Attributes)
	select {
	case q.input <- observation:
		q.accepted.Add(1)
		return true
	default:
		q.dropped.Add(1)
		return false
	}
}

func (q *ObservationQueue) Stats() ObservationQueueStats {
	if q == nil {
		return ObservationQueueStats{}
	}
	return ObservationQueueStats{
		Accepted: q.accepted.Load(), Dropped: q.dropped.Load(), Failed: q.failed.Load(),
		Delivered: q.delivered.Load(), Depth: len(q.input),
	}
}

func (q *ObservationQueue) Close() {
	if q == nil {
		return
	}
	q.once.Do(func() {
		q.closed.Store(true)
		q.cancel()
	})
	<-q.done
}

func (q *ObservationQueue) run(ctx context.Context) {
	defer close(q.done)
	for {
		select {
		case <-ctx.Done():
			return
		case observation := <-q.input:
			delivery, cancel := context.WithTimeout(ctx, q.timeout)
			err := q.sink.RecordObservation(delivery, observation)
			cancel()
			if err != nil {
				q.failed.Add(1)
			} else {
				q.delivered.Add(1)
			}
		}
	}
}

func cloneObservationAttributes(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	return cloned
}
