// Package deliveryqueue provides a single-process durable spool for resolved
// Gateway deliveries. Each accepted item is fsynced as an individual file so
// process restarts cannot silently discard deliveries that ingress acknowledged.
package deliveryqueue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

const spoolVersion = "v1"

var ErrNotFound = errors.New("delivery not found")
var ErrAlreadyPending = errors.New("delivery is already pending")
var ErrInvalidState = errors.New("invalid delivery state")
var ErrSpoolInUse = errors.New("delivery spool is already active in this process")
var ErrIdempotencyConflict = bridge.ErrIdempotencyConflict

var activeSpools sync.Map

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

// Status is the metadata-only view of a spool item. It deliberately omits
// payload, headers, transport credentials, destination URL, and error text.
type Status struct {
	ID              string    `json:"id"`
	EventID         string    `json:"event_id,omitempty"`
	State           State     `json:"state"`
	Protocol        string    `json:"protocol"`
	Adapter         string    `json:"adapter"`
	SubscriberID    string    `json:"subscriber_id,omitempty"`
	FailureNoticeID string    `json:"failure_notice_id,omitempty"`
	Attempts        int       `json:"attempts"`
	ExpectedStatus  int       `json:"expected_status,omitempty"`
	ResponseStatus  int       `json:"response_status,omitempty"`
	BodyAssertion   string    `json:"body_assertion,omitempty"`
	NextAttemptAt   time.Time `json:"next_attempt_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SupportsAdapter reports whether this worker can redrive a delivery of the
// named kind. Shared stores may be visible to several workers with disjoint
// adapter sets, so a private admin aggregator must select the right one.
func (queue *Queue) SupportsAdapter(name string) bool {
	return queue != nil && queue.config.Adapters[name] != nil
}

type Observer interface {
	RecordDeliveryEvent(Event)
}

type ObserverFunc func(Event)

func (function ObserverFunc) RecordDeliveryEvent(event Event) { function(event) }

type Config struct {
	Path                string
	Adapters            map[string]bridge.GatewayDeliveryAdapter
	Observer            Observer
	MaxAttempts         int
	InitialBackoff      time.Duration
	MaxBackoff          time.Duration
	DeliveryTimeout     time.Duration
	PollInterval        time.Duration
	ReceiptRetention    time.Duration
	FailureNoticeDomain string
}

type Queue struct {
	config       Config
	lockFile     *os.File
	pendingDir   string
	failedDir    string
	deliveredDir string
	wake         chan struct{}
	stop         chan struct{}
	done         chan struct{}
	closeOnce    sync.Once
	transitionMu sync.Mutex
}

type item struct {
	Version                string                 `json:"version"`
	ID                     string                 `json:"id"`
	EventID                string                 `json:"event_id,omitempty"`
	ExpectedStatus         int                    `json:"expected_status,omitempty"`
	ResponseStatus         int                    `json:"response_status,omitempty"`
	BodyAssertion          string                 `json:"body_assertion,omitempty"`
	Delivery               bridge.GatewayDelivery `json:"delivery"`
	Protocol               bridge.Protocol        `json:"protocol,omitempty"`
	Adapter                string                 `json:"adapter,omitempty"`
	SubscriberID           string                 `json:"subscriber_id,omitempty"`
	FailureNoticeID        string                 `json:"failure_notice_id,omitempty"`
	IdempotencyFingerprint string                 `json:"idempotency_fingerprint,omitempty"`
	IdempotencyExpiresAt   time.Time              `json:"idempotency_expires_at,omitempty"`
	State                  State                  `json:"state,omitempty"`
	Attempts               int                    `json:"attempts"`
	NextAttemptAt          time.Time              `json:"next_attempt_at"`
	LastError              string                 `json:"last_error,omitempty"`
	CreatedAt              time.Time              `json:"created_at"`
	UpdatedAt              time.Time              `json:"updated_at"`
}

func New(config Config) (*Queue, error) {
	config.Path = strings.TrimSpace(config.Path)
	if config.Path == "" {
		return nil, errors.New("delivery spool path is required")
	}
	var err error
	config, err = normalizeConfig(config)
	if err != nil {
		return nil, err
	}

	queue := &Queue{
		config:       config,
		pendingDir:   filepath.Join(config.Path, "pending"),
		failedDir:    filepath.Join(config.Path, "failed"),
		deliveredDir: filepath.Join(config.Path, "delivered"),
		wake:         make(chan struct{}, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	for _, directory := range []string{queue.pendingDir, queue.failedDir, queue.deliveredDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, fmt.Errorf("create delivery spool directory: %w", err)
		}
	}
	canonical, err := filepath.EvalSymlinks(config.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve delivery spool directory: %w", err)
	}
	if _, loaded := activeSpools.LoadOrStore(canonical, struct{}{}); loaded {
		return nil, ErrSpoolInUse
	}
	lockFile, err := acquireSpoolLock(filepath.Join(canonical, "spool.lock"))
	if err != nil {
		activeSpools.Delete(canonical)
		return nil, err
	}
	queue.config.Path = canonical
	queue.lockFile = lockFile

	go queue.run()
	return queue, nil
}

func normalizeConfig(config Config) (Config, error) {
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
	if config.MaxAttempts < 1 || config.InitialBackoff < 1 || config.MaxBackoff < config.InitialBackoff || config.DeliveryTimeout < 1 || config.PollInterval < 1 || config.ReceiptRetention < 0 {
		return Config{}, errors.New("delivery spool attempts and durations must be positive")
	}
	if config.FailureNoticeDomain != "" && !validFailureNoticeDomain(config.FailureNoticeDomain) {
		return Config{}, errors.New("failure notice domain must be a valid DNS hostname")
	}
	adapters := make(map[string]bridge.GatewayDeliveryAdapter, len(config.Adapters))
	for name, adapter := range config.Adapters {
		if strings.TrimSpace(name) == "" || adapter == nil {
			return Config{}, errors.New("delivery spool adapters require a name and implementation")
		}
		adapters[name] = adapter
	}
	config.Adapters = adapters
	return config, nil
}

func (queue *Queue) EnqueueGateway(delivery bridge.GatewayDelivery) error {
	_, err := queue.EnqueueGatewayWithID(delivery)
	return err
}

// EnqueueGatewayWithID returns the receipt only after the delivery is durable.
func (queue *Queue) EnqueueGatewayWithID(delivery bridge.GatewayDelivery) (string, error) {
	id, err := newID()
	if err != nil {
		return "", fmt.Errorf("create delivery spool ID: %w", err)
	}
	_, err = queue.enqueueGateway(delivery, id, "", time.Time{})
	if err != nil {
		return "", err
	}
	return id, nil
}

// EnqueueGatewayIdempotent uses a Planner-reserved receipt ID. Repeating the
// same fingerprint returns that receipt without a second outbound attempt.
// The receipt remains pinned through expiresAt even if normal receipt
// retention is shorter.
func (queue *Queue) EnqueueGatewayIdempotent(delivery bridge.GatewayDelivery, id, fingerprint string, expiresAt time.Time) (bool, error) {
	if !validID(id) || len(fingerprint) != 64 || !validHex(fingerprint) || !expiresAt.After(time.Now().UTC()) {
		return false, errors.New("invalid idempotent delivery receipt")
	}
	return queue.enqueueGateway(delivery, id, fingerprint, expiresAt)
}

func (queue *Queue) enqueueGateway(delivery bridge.GatewayDelivery, id, fingerprint string, expiresAt time.Time) (bool, error) {
	if queue == nil {
		return false, errors.New("delivery spool is not initialized")
	}
	if err := delivery.Validate(); err != nil {
		return false, err
	}
	if queue.config.Adapters[delivery.AdapterName()] == nil {
		return false, fmt.Errorf("delivery adapter %q is not registered", delivery.AdapterName())
	}
	if recipient := delivery.Attributes[bridge.FailureNoticeToAttribute]; recipient != "" {
		if delivery.Protocol != bridge.ProtocolSMTP || delivery.AdapterName() != string(bridge.ProtocolSMTP) || queue.config.FailureNoticeDomain == "" || recipient != delivery.Attributes["mail_from"] || !validFailureNoticeAddress(recipient) {
			return false, errors.New("SMTP failure notice requires a configured domain and authenticated envelope sender")
		}
		if _, err := failureNoticeRecipients(delivery); err != nil {
			return false, err
		}
	}
	queue.transitionMu.Lock()
	defer queue.transitionMu.Unlock()
	for _, directory := range []string{queue.pendingDir, queue.failedDir, queue.deliveredDir} {
		existing, err := readItem(filepath.Join(directory, id+".json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if fingerprint == "" || existing.IdempotencyFingerprint != fingerprint || existing.ID != id {
			return false, ErrIdempotencyConflict
		}
		return true, nil
	}
	now := time.Now().UTC()
	queued := item{
		Version:                spoolVersion,
		ID:                     id,
		EventID:                delivery.Attributes["event_id"],
		Delivery:               delivery,
		Protocol:               delivery.Protocol,
		Adapter:                delivery.AdapterName(),
		SubscriberID:           delivery.SubscriberID,
		State:                  StateQueued,
		NextAttemptAt:          now,
		CreatedAt:              now,
		UpdatedAt:              now,
		IdempotencyFingerprint: fingerprint,
		IdempotencyExpiresAt:   expiresAt,
	}
	if value := delivery.Attributes["expected_status"]; value != "" {
		queued.ExpectedStatus, _ = strconv.Atoi(value)
	}
	if err := writeItem(filepath.Join(queue.pendingDir, id+".json"), queued); err != nil {
		return false, fmt.Errorf("persist delivery: %w", err)
	}
	queue.observe(queued, StateQueued)
	select {
	case queue.wake <- struct{}{}:
	default:
	}
	return false, nil
}

func validHex(value string) bool {
	for _, character := range value {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
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

// Status reads the durable state of one delivery without returning its body or
// credentials. A completed receipt remains available in the delivered spool.
func (queue *Queue) Status(id string) (Status, error) {
	if queue == nil || !validID(id) {
		return Status{}, ErrNotFound
	}
	for _, candidate := range []struct {
		directory string
		state     State
	}{{queue.pendingDir, StateQueued}, {queue.failedDir, StateFailed}, {queue.deliveredDir, StateDelivered}} {
		queued, err := readItem(filepath.Join(candidate.directory, id+".json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Status{}, err
		}
		if queued.ID != id {
			return Status{}, errors.New("delivery spool ID does not match filename")
		}
		state := candidate.state
		if candidate.directory == queue.pendingDir {
			switch queued.State {
			case StateDelivered, StateFailed, StateDeferred:
				state = queued.State
			}
		}
		return statusOf(queued, state), nil
	}
	return Status{}, ErrNotFound
}

// List returns a bounded number of recent metadata records for one state.
// It scans that state's directory; callers should avoid polling large spools.
func (queue *Queue) List(state State, limit int) ([]Status, error) {
	if queue == nil || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("delivery status limit must be between 1 and 100")
	}
	directory := queue.pendingDir
	switch state {
	case StateQueued, StateDeferred:
	case StateFailed:
		directory = queue.failedDir
	case StateDelivered:
		directory = queue.deliveredDir
	default:
		return nil, ErrInvalidState
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	statuses := make([]Status, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validID(id) {
			continue
		}
		status, err := queue.Status(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if status.State == state {
			statuses = append(statuses, status)
		}
	}
	sort.Slice(statuses, func(left, right int) bool {
		return statuses[left].UpdatedAt.After(statuses[right].UpdatedAt)
	})
	if len(statuses) > limit {
		statuses = statuses[:limit]
	}
	return statuses, nil
}

// ListEvent returns recent queued, failed, and delivered receipts originating
// from one ingress event. Unlike List, it must scan all spool states; callers
// should use it for troubleshooting rather than continuous polling.
func (queue *Queue) ListEvent(eventID string, limit int) ([]Status, error) {
	if queue == nil || eventID == "" || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("invalid delivery event filter or limit")
	}
	statuses := make([]Status, 0)
	for _, directory := range []string{queue.pendingDir, queue.failedDir, queue.deliveredDir} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			if !validID(id) {
				continue
			}
			status, err := queue.Status(id)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if status.EventID == eventID {
				statuses = append(statuses, status)
			}
		}
	}
	sort.Slice(statuses, func(left, right int) bool {
		if statuses[left].UpdatedAt.Equal(statuses[right].UpdatedAt) {
			return statuses[left].ID > statuses[right].ID
		}
		return statuses[left].UpdatedAt.After(statuses[right].UpdatedAt)
	})
	if len(statuses) > limit {
		statuses = statuses[:limit]
	}
	return statuses, nil
}

// Retry explicitly redrives a failed delivery. It never replays a successful
// receipt, and only a still-registered adapter may receive the delivery.
func (queue *Queue) Retry(id string) error {
	if queue == nil || !validID(id) {
		return ErrNotFound
	}
	queue.transitionMu.Lock()
	defer queue.transitionMu.Unlock()
	pendingPath := filepath.Join(queue.pendingDir, id+".json")
	if _, err := os.Stat(pendingPath); err == nil {
		return ErrAlreadyPending
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	path := filepath.Join(queue.failedDir, id+".json")
	queued, err := readItem(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if queued.ID != id {
		return errors.New("delivery spool ID does not match filename")
	}
	if queued.FailureNoticeID != "" {
		return ErrInvalidState
	}
	if queued.Delivery.Attributes[bridge.FailureNoticeToAttribute] != "" {
		exists, err := queue.failureNoticeExists(failureNoticeID(queued.ID))
		if err != nil {
			return err
		}
		if exists {
			return ErrInvalidState
		}
	}
	if err := queued.Delivery.Validate(); err != nil {
		return fmt.Errorf("failed delivery cannot be retried: %w", err)
	}
	if queue.config.Adapters[queued.Delivery.AdapterName()] == nil {
		return fmt.Errorf("delivery adapter %q is not registered", queued.Delivery.AdapterName())
	}
	queued.State = StateQueued
	queued.Attempts = 0
	queued.ResponseStatus = 0
	queued.BodyAssertion = ""
	queued.LastError = ""
	queued.NextAttemptAt = time.Now().UTC()
	queued.UpdatedAt = queued.NextAttemptAt
	if err := writeItem(path, queued); err != nil {
		return err
	}
	if err := os.Rename(path, pendingPath); err != nil {
		return err
	}
	if err := syncDirectory(queue.failedDir); err != nil {
		return err
	}
	if err := syncDirectory(queue.pendingDir); err != nil {
		return err
	}
	queue.observe(queued, StateQueued)
	select {
	case queue.wake <- struct{}{}:
	default:
	}
	return nil
}

func statusOf(queued item, state State) Status {
	protocol := queued.Protocol
	if protocol == "" {
		protocol = queued.Delivery.Protocol
	}
	adapter := queued.Adapter
	if adapter == "" {
		adapter = queued.Delivery.AdapterName()
	}
	subscriberID := queued.SubscriberID
	if subscriberID == "" {
		subscriberID = queued.Delivery.SubscriberID
	}
	eventID := queued.EventID
	if eventID == "" {
		eventID = queued.Delivery.Attributes["event_id"]
	}
	status := Status{
		ID: queued.ID, State: state, Protocol: string(protocol),
		EventID: eventID,
		Adapter: adapter, SubscriberID: subscriberID, FailureNoticeID: queued.FailureNoticeID,
		Attempts: queued.Attempts, CreatedAt: queued.CreatedAt, UpdatedAt: queued.UpdatedAt,
		ExpectedStatus: queued.ExpectedStatus, ResponseStatus: queued.ResponseStatus,
		BodyAssertion: queued.BodyAssertion,
	}
	if state == StateQueued || state == StateDeferred {
		status.NextAttemptAt = queued.NextAttemptAt
	}
	return status
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, character := range id {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}

func (queue *Queue) run() {
	defer func() {
		_ = releaseSpoolLock(queue.lockFile)
		activeSpools.Delete(queue.config.Path)
		close(queue.done)
	}()
	ticker := time.NewTicker(queue.config.PollInterval)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(time.Hour)
	defer pruneTicker.Stop()
	noticeTicker := time.NewTicker(time.Minute)
	defer noticeTicker.Stop()
	_, _ = queue.PruneReceipts()
	if err := queue.processFailureNotices(); err != nil {
		log.Printf("Gateway delivery failure notice recovery failed: %v", err)
	}
	for {
		queue.processDue()
		select {
		case <-queue.stop:
			return
		case <-queue.wake:
		case <-ticker.C:
		case <-pruneTicker.C:
			_, _ = queue.PruneReceipts()
		case <-noticeTicker.C:
			if err := queue.processFailureNotices(); err != nil {
				log.Printf("Gateway delivery failure notice recovery failed: %v", err)
			}
		}
	}
}

// PruneReceipts removes only expired, already-delivered metadata receipts.
// Pending and failed deliveries are never removed by retention. Zero disables
// automatic pruning so an operator can supply an external retention policy.
func (queue *Queue) PruneReceipts() (int, error) {
	if queue == nil || queue.config.ReceiptRetention == 0 {
		return 0, nil
	}
	queue.transitionMu.Lock()
	defer queue.transitionMu.Unlock()
	entries, err := os.ReadDir(queue.deliveredDir)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().UTC().Add(-queue.config.ReceiptRetention)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || !validID(strings.TrimSuffix(entry.Name(), ".json")) {
			continue
		}
		path := filepath.Join(queue.deliveredDir, entry.Name())
		receipt, err := readItem(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, err
		}
		if receipt.ID != strings.TrimSuffix(entry.Name(), ".json") || receipt.State != StateDelivered || !receipt.UpdatedAt.Before(cutoff) ||
			(!receipt.IdempotencyExpiresAt.IsZero() && !receipt.IdempotencyExpiresAt.Before(time.Now().UTC())) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		removed++
	}
	if removed > 0 {
		if err := syncDirectory(queue.deliveredDir); err != nil {
			return removed, err
		}
	}
	return removed, nil
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
		if queued.State == StateDelivered || queued.State == StateFailed {
			queue.transitionMu.Lock()
			destination := queue.deliveredDir
			if queued.State == StateFailed {
				destination = queue.failedDir
			}
			if os.Rename(path, filepath.Join(destination, entry.Name())) == nil {
				_ = syncDirectory(queue.pendingDir)
				_ = syncDirectory(destination)
			}
			queue.transitionMu.Unlock()
			continue
		}
		if queued.NextAttemptAt.After(time.Now().UTC()) {
			continue
		}
		queue.deliver(path, entry.Name(), queued)
	}
}

func (queue *Queue) deliver(path, name string, queued item) {
	if queued.Delivery.AdapterName() == "managed.smtp" {
		if queued.Delivery.Attributes == nil {
			queued.Delivery.Attributes = make(map[string]string)
		}
		queued.Delivery.Attributes["gateway_delivery_id"] = queued.ID
		queued.Delivery.Attributes["gateway_attempt_index"] = strconv.Itoa(queued.Attempts)
	}
	adapter := queue.config.Adapters[queued.Delivery.AdapterName()]
	var err error
	var result bridge.GatewayDeliveryResult
	if adapter == nil {
		err = fmt.Errorf("delivery adapter %q is not registered", queued.Delivery.AdapterName())
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), queue.config.DeliveryTimeout)
		if reporting, ok := adapter.(bridge.GatewayDeliveryResultAdapter); ok {
			result, err = reporting.DeliverGatewayResult(ctx, queued.Delivery)
		} else {
			err = adapter.DeliverGateway(ctx, queued.Delivery)
		}
		cancel()
	}
	queued.ResponseStatus = result.ResponseStatus
	queued.BodyAssertion = result.BodyAssertion
	queued.Attempts++
	queued.UpdatedAt = time.Now().UTC()
	if err == nil {
		queued.LastError = ""
		observed := queued
		queued.State = StateDelivered
		queued.Delivery = bridge.GatewayDelivery{}
		queued.NextAttemptAt = time.Time{}
		queued.LastError = ""
		queue.transitionMu.Lock()
		if writeItem(path, queued) == nil {
			if os.Rename(path, filepath.Join(queue.deliveredDir, name)) == nil {
				_ = syncDirectory(queue.pendingDir)
				_ = syncDirectory(queue.deliveredDir)
				queue.observe(observed, StateDelivered)
			}
		}
		queue.transitionMu.Unlock()
		return
	}

	uncertain := errors.Is(err, bridge.ErrUncertainDelivery)
	queued.LastError = err.Error()
	if uncertain {
		queued.BodyAssertion = "smtp_acceptance_unknown"
	}
	if queued.Attempts >= queue.config.MaxAttempts || adapter == nil || errors.Is(err, bridge.ErrPermanentDelivery) || uncertain {
		queued.State = StateFailed
		queue.transitionMu.Lock()
		failed := false
		if writeItem(path, queued) == nil {
			if os.Rename(path, filepath.Join(queue.failedDir, name)) == nil {
				_ = syncDirectory(queue.pendingDir)
				_ = syncDirectory(queue.failedDir)
				queue.observe(queued, StateFailed)
				failed = true
			}
		}
		queue.transitionMu.Unlock()
		if failed && !uncertain {
			if err := queue.ensureFailureNotice(queued); err != nil {
				log.Printf("Gateway delivery failure notice enqueue failed for %s: %v", queued.ID, err)
			}
		}
		return
	}
	queued.NextAttemptAt = queued.UpdatedAt.Add(queue.backoff(queued.Attempts))
	queued.State = StateDeferred
	queue.transitionMu.Lock()
	if writeItem(path, queued) == nil {
		queue.observe(queued, StateDeferred)
	}
	queue.transitionMu.Unlock()
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
