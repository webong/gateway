// Package deliveryqueue provides a single-process durable spool for resolved
// Gateway deliveries. Each accepted item is fsynced as an individual file so
// process restarts cannot silently discard mail that ingress acknowledged.
package deliveryqueue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

const spoolVersion = "v1"

type State string

const (
	StateQueued    State = "queued"
	StateDelivered State = "delivered"
	StateDeferred  State = "deferred"
	StateFailed    State = "failed"
)

type Event struct {
	ID        string
	State     State
	Adapter   string
	Target    string
	Attempts  int
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Observer interface {
	RecordDeliveryEvent(Event)
}

type ObserverFunc func(Event)

func (function ObserverFunc) RecordDeliveryEvent(event Event) { function(event) }

type Config struct {
	Path            string
	Adapters        map[string]bridge.GatewayDeliveryAdapter
	Observer        Observer
	MaxAttempts     int
	InitialBackoff  time.Duration
	MaxBackoff      time.Duration
	DeliveryTimeout time.Duration
	PollInterval    time.Duration
}

type Queue struct {
	config     Config
	pendingDir string
	failedDir  string
	wake       chan struct{}
	stop       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
}

type item struct {
	Version       string                 `json:"version"`
	ID            string                 `json:"id"`
	Delivery      bridge.GatewayDelivery `json:"delivery"`
	Attempts      int                    `json:"attempts"`
	NextAttemptAt time.Time              `json:"next_attempt_at"`
	LastError     string                 `json:"last_error,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

func New(config Config) (*Queue, error) {
	config.Path = strings.TrimSpace(config.Path)
	if config.Path == "" {
		return nil, errors.New("delivery spool path is required")
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = 8
	}
	if config.InitialBackoff == 0 {
		config.InitialBackoff = 5 * time.Second
	}
	if config.MaxBackoff == 0 {
		config.MaxBackoff = time.Hour
	}
	if config.DeliveryTimeout == 0 {
		config.DeliveryTimeout = 30 * time.Second
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.MaxAttempts < 1 || config.InitialBackoff < 1 || config.MaxBackoff < config.InitialBackoff || config.DeliveryTimeout < 1 || config.PollInterval < 1 {
		return nil, errors.New("delivery spool attempts and durations must be positive")
	}
	adapters := make(map[string]bridge.GatewayDeliveryAdapter, len(config.Adapters))
	for name, adapter := range config.Adapters {
		if strings.TrimSpace(name) == "" || adapter == nil {
			return nil, errors.New("delivery spool adapters require a name and implementation")
		}
		adapters[name] = adapter
	}
	config.Adapters = adapters

	queue := &Queue{
		config:     config,
		pendingDir: filepath.Join(config.Path, "pending"),
		failedDir:  filepath.Join(config.Path, "failed"),
		wake:       make(chan struct{}, 1),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	for _, directory := range []string{queue.pendingDir, queue.failedDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create delivery spool directory: %w", err)
		}
	}

	go queue.run()
	return queue, nil
}

func (queue *Queue) EnqueueGateway(delivery bridge.GatewayDelivery) error {
	if queue == nil {
		return errors.New("delivery spool is not initialized")
	}
	if err := delivery.Validate(); err != nil {
		return err
	}
	if queue.config.Adapters[delivery.AdapterName()] == nil {
		return fmt.Errorf("delivery adapter %q is not registered", delivery.AdapterName())
	}
	id, err := newID()
	if err != nil {
		return fmt.Errorf("create delivery spool ID: %w", err)
	}
	now := time.Now().UTC()
	queued := item{
		Version:       spoolVersion,
		ID:            id,
		Delivery:      delivery,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := writeItem(filepath.Join(queue.pendingDir, id+".json"), queued); err != nil {
		return fmt.Errorf("persist delivery: %w", err)
	}
	queue.observe(queued, StateQueued)
	select {
	case queue.wake <- struct{}{}:
	default:
	}
	return nil
}

func (queue *Queue) Close(ctx context.Context) error {
	if queue == nil {
		return nil
	}
	queue.closeOnce.Do(func() { close(queue.stop) })
	select {
	case <-queue.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (queue *Queue) Counts() (pending int, failed int, err error) {
	pendingEntries, err := os.ReadDir(queue.pendingDir)
	if err != nil {
		return 0, 0, err
	}
	failedEntries, err := os.ReadDir(queue.failedDir)
	if err != nil {
		return 0, 0, err
	}
	return countJSON(pendingEntries), countJSON(failedEntries), nil
}

func (queue *Queue) run() {
	defer close(queue.done)
	ticker := time.NewTicker(queue.config.PollInterval)
	defer ticker.Stop()
	for {
		queue.processDue()
		select {
		case <-queue.stop:
			return
		case <-queue.wake:
		case <-ticker.C:
		}
	}
}

func (queue *Queue) processDue() {
	entries, err := os.ReadDir(queue.pendingDir)
	if err != nil {
		return
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	for _, entry := range entries {
		select {
		case <-queue.stop:
			return
		default:
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(queue.pendingDir, entry.Name())
		queued, readErr := readItem(path)
		if readErr != nil {
			_ = os.Rename(path, filepath.Join(queue.failedDir, entry.Name()))
			continue
		}
		if queued.NextAttemptAt.After(time.Now().UTC()) {
			continue
		}
		queue.deliver(path, entry.Name(), queued)
	}
}

func (queue *Queue) deliver(path, name string, queued item) {
	adapter := queue.config.Adapters[queued.Delivery.AdapterName()]
	var err error
	if adapter == nil {
		err = fmt.Errorf("delivery adapter %q is not registered", queued.Delivery.AdapterName())
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), queue.config.DeliveryTimeout)
		err = adapter.DeliverGateway(ctx, queued.Delivery)
		cancel()
	}
	queued.Attempts++
	queued.UpdatedAt = time.Now().UTC()
	if err == nil {
		if removeErr := os.Remove(path); removeErr == nil {
			_ = syncDirectory(queue.pendingDir)
			queue.observe(queued, StateDelivered)
		}
		return
	}

	queued.LastError = err.Error()
	if queued.Attempts >= queue.config.MaxAttempts || adapter == nil {
		if writeErr := writeItem(filepath.Join(queue.failedDir, name), queued); writeErr == nil {
			if removeErr := os.Remove(path); removeErr == nil {
				_ = syncDirectory(queue.pendingDir)
				queue.observe(queued, StateFailed)
			}
		}
		return
	}
	queued.NextAttemptAt = queued.UpdatedAt.Add(queue.backoff(queued.Attempts))
	if writeItem(path, queued) == nil {
		queue.observe(queued, StateDeferred)
	}
}

func (queue *Queue) backoff(attempt int) time.Duration {
	backoff := queue.config.InitialBackoff
	for index := 1; index < attempt && backoff < queue.config.MaxBackoff; index++ {
		if backoff > queue.config.MaxBackoff/2 {
			return queue.config.MaxBackoff
		}
		backoff *= 2
	}
	if backoff > queue.config.MaxBackoff {
		return queue.config.MaxBackoff
	}
	return backoff
}

func (queue *Queue) observe(queued item, state State) {
	if queue.config.Observer == nil {
		return
	}
	queue.config.Observer.RecordDeliveryEvent(Event{
		ID:        queued.ID,
		State:     state,
		Adapter:   queued.Delivery.AdapterName(),
		Target:    queued.Delivery.Target,
		Attempts:  queued.Attempts,
		Error:     queued.LastError,
		CreatedAt: queued.CreatedAt,
		UpdatedAt: queued.UpdatedAt,
	})
}

func writeItem(path string, value item) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".delivery-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func readItem(path string) (item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return item{}, err
	}
	var value item
	if err := json.Unmarshal(data, &value); err != nil {
		return item{}, err
	}
	if value.Version != spoolVersion || value.ID == "" {
		return item{}, errors.New("unsupported delivery spool item")
	}
	return value, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func newID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func countJSON(entries []os.DirEntry) int {
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	return count
}

var _ bridge.GatewayDeliveryQueue = (*Queue)(nil)
