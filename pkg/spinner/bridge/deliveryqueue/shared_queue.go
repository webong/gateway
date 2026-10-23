package deliveryqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue/sharedpg"
)

// SharedQueue runs deliveries from PostgreSQL with expiring, fenced leases.
// All participating Spinner nodes must use the same migrated database.
type SharedQueue struct {
	config    Config
	store     *sharedpg.Store
	db        *sql.DB
	owner     string
	adapters  []string
	wake      chan struct{}
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

var _ Backend = (*SharedQueue)(nil)

func (queue *SharedQueue) SupportsAdapter(name string) bool {
	return queue != nil && queue.config.Adapters[name] != nil
}

func NewShared(config Config, databaseURL string) (*SharedQueue, error) {
	if strings.TrimSpace(databaseURL) == "" || strings.TrimSpace(config.Path) != "" {
		return nil, errors.New("shared delivery queue requires a database URL and no local spool path")
	}
	var err error
	config, err = normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	if len(config.Adapters) == 0 {
		return nil, errors.New("shared delivery queue requires at least one adapter")
	}
	owner, err := newID()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open shared delivery database: %w", err)
	}
	db.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = db.PingContext(ctx)
	cancel()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("connect shared delivery database: %w", err)
	}
	store, err := sharedpg.New(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	err = store.CheckSchema(ctx)
	cancel()
	if err != nil {
		db.Close()
		return nil, err
	}
	queue := &SharedQueue{
		config: config, store: store, db: db, owner: owner,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	for name := range config.Adapters {
		queue.adapters = append(queue.adapters, name)
	}
	sort.Strings(queue.adapters)
	go queue.run()
	return queue, nil
}

func (queue *SharedQueue) EnqueueGateway(delivery bridge.GatewayDelivery) error {
	_, err := queue.EnqueueGatewayWithID(delivery)
	return err
}

func (queue *SharedQueue) EnqueueGatewayWithID(delivery bridge.GatewayDelivery) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	_, err = queue.enqueue(delivery, id, "", time.Time{})
	if err != nil {
		return "", err
	}
	return id, nil
}

func (queue *SharedQueue) EnqueueGatewayIdempotent(delivery bridge.GatewayDelivery, id, fingerprint string, expiresAt time.Time) (bool, error) {
	if !validID(id) || len(fingerprint) != 64 || !validHex(fingerprint) || !expiresAt.After(time.Now().UTC()) {
		return false, errors.New("invalid idempotent delivery receipt")
	}
	return queue.enqueue(delivery, id, fingerprint, expiresAt)
}

func (queue *SharedQueue) enqueue(delivery bridge.GatewayDelivery, id, fingerprint string, expiresAt time.Time) (bool, error) {
	if queue == nil || queue.store == nil {
		return false, errors.New("shared delivery queue is not initialized")
	}
	if err := delivery.Validate(); err != nil {
		return false, err
	}
	if queue.config.Adapters[delivery.AdapterName()] == nil {
		return false, fmt.Errorf("delivery adapter %q is not registered", delivery.AdapterName())
	}
	if recipient := delivery.Attributes[bridge.FailureNoticeToAttribute]; recipient != "" {
		if delivery.Protocol != bridge.ProtocolSMTP || delivery.AdapterName() != string(bridge.ProtocolSMTP) ||
			queue.config.FailureNoticeDomain == "" || recipient != delivery.Attributes["mail_from"] || !validFailureNoticeAddress(recipient) {
			return false, errors.New("SMTP failure notice requires a configured domain and authenticated envelope sender")
		}
		if _, err := failureNoticeRecipients(delivery); err != nil {
			return false, err
		}
	}
	now := time.Now().UTC()
	queued := item{
		Version: spoolVersion, ID: id, EventID: delivery.Attributes["event_id"],
		Delivery: delivery, Protocol: delivery.Protocol, Adapter: delivery.AdapterName(),
		SubscriberID: delivery.SubscriberID, State: StateQueued,
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		IdempotencyFingerprint: fingerprint, IdempotencyExpiresAt: expiresAt,
	}
	if value := delivery.Attributes["expected_status"]; value != "" {
		queued.ExpectedStatus, _ = strconv.Atoi(value)
	}
	payload, err := json.Marshal(queued)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = queue.store.Put(ctx, sharedpg.Record{
		ID: id, Payload: payload, State: string(StateQueued), EventID: queued.EventID,
		NextAttemptAt: now, IdempotencyFingerprint: fingerprint, IdempotencyExpiresAt: expiresAt,
	})
	if errors.Is(err, sharedpg.ErrConflict) {
		existing, findErr := queue.store.Find(ctx, id)
		if findErr != nil {
			return false, findErr
		}
		if existing != nil && fingerprint != "" && existing.IdempotencyFingerprint == fingerprint {
			return true, nil
		}
		return false, ErrIdempotencyConflict
	}
	if err != nil {
		return false, err
	}
	queue.observe(queued, StateQueued)
	select {
	case queue.wake <- struct{}{}:
	default:
	}
	return false, nil
}

func (queue *SharedQueue) Close(ctx context.Context) error {
	if queue == nil {
		return nil
	}
	queue.closeOnce.Do(func() { close(queue.stop) })
	select {
	case <-queue.done:
		return queue.db.Close()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (queue *SharedQueue) Counts() (int, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return queue.store.Counts(ctx)
}

func (queue *SharedQueue) Status(id string) (Status, error) {
	if queue == nil || !validID(id) {
		return Status{}, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	record, err := queue.store.Find(ctx, id)
	if err != nil {
		return Status{}, err
	}
	if record == nil {
		return Status{}, ErrNotFound
	}
	return sharedStatus(*record)
}

func (queue *SharedQueue) List(state State, limit int) ([]Status, error) {
	if queue == nil || limit < 1 || limit > 100 {
		return nil, errors.New("delivery status limit must be between 1 and 100")
	}
	if state != StateQueued && state != StateDeferred && state != StateFailed && state != StateDelivered {
		return nil, ErrInvalidState
	}
	return queue.list(string(state), "", limit)
}

func (queue *SharedQueue) ListEvent(eventID string, limit int) ([]Status, error) {
	if queue == nil || eventID == "" || limit < 1 || limit > 100 {
		return nil, errors.New("invalid delivery event filter or limit")
	}
	return queue.list("", eventID, limit)
}

func (queue *SharedQueue) list(state, eventID string, limit int) ([]Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	records, err := queue.store.List(ctx, state, eventID, limit)
	if err != nil {
		return nil, err
	}
	statuses := make([]Status, 0, len(records))
	for _, record := range records {
		status, err := sharedStatus(record)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func sharedStatus(record sharedpg.Record) (Status, error) {
	var queued item
	if err := json.Unmarshal(record.Payload, &queued); err != nil || queued.Version != spoolVersion || queued.ID != record.ID {
		return Status{}, errors.New("invalid shared delivery record")
	}
	queued.CreatedAt = record.CreatedAt
	queued.UpdatedAt = record.UpdatedAt
	queued.NextAttemptAt = record.NextAttemptAt
	return statusOf(queued, State(record.State)), nil
}

func (queue *SharedQueue) Retry(id string) error {
	if queue == nil || !validID(id) {
		return ErrNotFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	record, err := queue.store.Find(ctx, id)
	if err != nil {
		return err
	}
	if record == nil {
		return ErrNotFound
	}
	if record.State == string(StateQueued) || record.State == string(StateDeferred) {
		return ErrAlreadyPending
	}
	if record.State != string(StateFailed) {
		return ErrInvalidState
	}
	var queued item
	if err := json.Unmarshal(record.Payload, &queued); err != nil || queued.ID != id {
		return errors.New("invalid shared delivery record")
	}
	if queued.FailureNoticeID != "" || queued.Delivery.Attributes[bridge.FailureNoticeToAttribute] != "" {
		return ErrInvalidState
	}
	if err := queued.Delivery.Validate(); err != nil {
		return fmt.Errorf("failed delivery cannot be retried: %w", err)
	}
	if queue.config.Adapters[queued.Delivery.AdapterName()] == nil {
		return fmt.Errorf("delivery adapter %q is not registered", queued.Delivery.AdapterName())
	}
	queued.State, queued.Attempts, queued.ResponseStatus = StateQueued, 0, 0
	queued.BodyAssertion, queued.LastError = "", ""
	queued.NextAttemptAt, queued.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	payload, err := json.Marshal(queued)
	if err != nil {
		return err
	}
	retried, err := queue.store.Retry(ctx, id, payload)
	if err != nil {
		return err
	}
	if !retried {
		return ErrAlreadyPending
	}
	queue.observe(queued, StateQueued)
	select {
	case queue.wake <- struct{}{}:
	default:
	}
	return nil
}

func (queue *SharedQueue) run() {
	defer close(queue.done)
	ticker := time.NewTicker(queue.config.PollInterval)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(time.Hour)
	defer pruneTicker.Stop()
	_, _ = queue.PruneReceipts()
	for {
		queue.processDue()
		select {
		case <-queue.stop:
			return
		case <-queue.wake:
		case <-ticker.C:
		case <-pruneTicker.C:
			_, _ = queue.PruneReceipts()
		}
	}
}

func (queue *SharedQueue) processDue() {
	for range 100 {
		select {
		case <-queue.stop:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		lease, err := queue.store.ClaimCompatible(ctx, queue.owner, queue.leaseDuration(), queue.adapters)
		cancel()
		if err != nil {
			log.Printf("Gateway shared delivery claim failed: %v", err)
			return
		}
		if lease == nil {
			return
		}
		queue.deliver(*lease)
	}
}

func (queue *SharedQueue) deliver(lease sharedpg.Lease) {
	var queued item
	if err := json.Unmarshal(lease.Payload, &queued); err != nil || queued.Version != spoolVersion || queued.ID != lease.ID || queued.Adapter == "" || queued.Adapter != queued.Delivery.AdapterName() {
		log.Printf("Gateway shared delivery %s has invalid payload", lease.ID)
		// Keep an inspectable receipt, but never retry or expose an untrusted
		// payload. Otherwise every lease expiry would reclaim this row forever.
		failed := item{
			Version: spoolVersion, ID: lease.ID, EventID: lease.EventID,
			State: StateFailed, Attempts: 1,
			LastError: "invalid persisted delivery payload",
			CreatedAt: lease.CreatedAt, UpdatedAt: time.Now().UTC(),
		}
		payload, marshalErr := json.Marshal(failed)
		if marshalErr != nil {
			log.Printf("Gateway shared delivery %s could not encode failed receipt: %v", lease.ID, marshalErr)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := queue.store.Settle(ctx, lease, string(StateFailed), payload, time.Time{})
		cancel()
		if err != nil {
			log.Printf("Gateway shared delivery %s could not settle invalid payload: %v", lease.ID, err)
		}
		return
	}
	adapter := queue.config.Adapters[queued.Delivery.AdapterName()]
	if queued.Delivery.AdapterName() == "managed.smtp" {
		if queued.Delivery.Attributes == nil {
			queued.Delivery.Attributes = make(map[string]string)
		}
		queued.Delivery.Attributes["gateway_delivery_id"] = queued.ID
		queued.Delivery.Attributes["gateway_attempt_index"] = strconv.Itoa(queued.Attempts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), queue.config.DeliveryTimeout)
	renewDone := make(chan error, 1)
	stopRenew := make(chan struct{})
	go queue.renewLease(lease, cancel, stopRenew, renewDone)
	var result bridge.GatewayDeliveryResult
	var err error
	if adapter == nil {
		err = fmt.Errorf("delivery adapter %q is not registered", queued.Delivery.AdapterName())
	} else if reporting, ok := adapter.(bridge.GatewayDeliveryResultAdapter); ok {
		result, err = reporting.DeliverGatewayResult(ctx, queued.Delivery)
	} else {
		err = adapter.DeliverGateway(ctx, queued.Delivery)
	}
	close(stopRenew)
	renewErr := <-renewDone
	cancel()
	if renewErr != nil {
		log.Printf("Gateway shared delivery %s lost lease: %v", lease.ID, renewErr)
		return
	}
	select {
	case <-queue.stop:
		return // Leave the item for another worker after its lease expires.
	default:
	}
	queued.ResponseStatus, queued.BodyAssertion = result.ResponseStatus, result.BodyAssertion
	queued.Attempts++
	queued.UpdatedAt = time.Now().UTC()
	observed := queued
	if err == nil {
		queued.State = StateDelivered
		queued.Delivery = bridge.GatewayDelivery{}
		queued.NextAttemptAt = time.Time{}
		queued.LastError = ""
	} else {
		queued.LastError = err.Error()
		if errors.Is(err, bridge.ErrUncertainDelivery) {
			queued.BodyAssertion = "smtp_acceptance_unknown"
		}
		if queued.Attempts >= queue.config.MaxAttempts || adapter == nil || errors.Is(err, bridge.ErrPermanentDelivery) || errors.Is(err, bridge.ErrUncertainDelivery) {
			queued.State = StateFailed
		} else {
			queued.State = StateDeferred
			queued.NextAttemptAt = queued.UpdatedAt.Add(queue.backoff(queued.Attempts))
		}
	}
	var notice item
	if queued.State == StateFailed && !errors.Is(err, bridge.ErrUncertainDelivery) && queued.Delivery.Attributes[bridge.FailureNoticeToAttribute] != "" {
		delivery, noticeErr := failureNoticeDelivery(queued, queue.config.FailureNoticeDomain)
		if noticeErr != nil {
			log.Printf("Gateway shared delivery %s could not construct failure notice: %v", lease.ID, noticeErr)
			return
		}
		id := failureNoticeID(queued.ID)
		now := time.Now().UTC()
		notice = item{
			Version: spoolVersion, ID: id, EventID: queued.EventID, Delivery: delivery,
			Protocol: bridge.ProtocolSMTP, Adapter: string(bridge.ProtocolSMTP),
			SubscriberID: delivery.SubscriberID, State: StateQueued,
			NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}
		queued.FailureNoticeID = id
	}
	payload, marshalErr := json.Marshal(queued)
	if marshalErr != nil {
		log.Printf("Gateway shared delivery %s could not encode result: %v", lease.ID, marshalErr)
		return
	}
	settleCtx, settleCancel := context.WithTimeout(context.Background(), 10*time.Second)
	var settleErr error
	if notice.ID != "" {
		noticePayload, noticeErr := json.Marshal(notice)
		if noticeErr != nil {
			settleCancel()
			log.Printf("Gateway shared delivery %s could not encode failure notice: %v", lease.ID, noticeErr)
			return
		}
		settleErr = queue.store.SettleWithNotice(settleCtx, lease, payload, sharedpg.Record{
			ID: notice.ID, Payload: noticePayload, State: string(StateQueued),
			EventID: notice.EventID, NextAttemptAt: notice.NextAttemptAt,
		})
	} else {
		settleErr = queue.store.Settle(settleCtx, lease, string(queued.State), payload, queued.NextAttemptAt)
	}
	settleCancel()
	if settleErr != nil {
		log.Printf("Gateway shared delivery %s could not settle: %v", lease.ID, settleErr)
		return
	}
	queue.observe(observed, queued.State)
	if notice.ID != "" {
		queue.observe(notice, StateQueued)
		select {
		case queue.wake <- struct{}{}:
		default:
		}
	}
}

func (queue *SharedQueue) renewLease(lease sharedpg.Lease, cancel context.CancelFunc, stop <-chan struct{}, done chan<- error) {
	ticker := time.NewTicker(queue.leaseDuration() / 3)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			done <- nil
			return
		case <-queue.stop:
			cancel()
			done <- sharedpg.ErrLeaseLost
			return
		case <-ticker.C:
			ctx, end := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := queue.store.Renew(ctx, lease, queue.leaseDuration())
			end()
			if err != nil {
				cancel()
				done <- err
				return
			}
		}
	}
}

func (queue *SharedQueue) leaseDuration() time.Duration {
	if queue.config.DeliveryTimeout >= 12*time.Hour {
		return 24 * time.Hour
	}
	value := queue.config.DeliveryTimeout * 2
	if value < 10*time.Second {
		return 10 * time.Second
	}
	return value
}

func (queue *SharedQueue) backoff(attempt int) time.Duration {
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

func (queue *SharedQueue) PruneReceipts() (int, error) {
	if queue == nil || queue.config.ReceiptRetention == 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	removed, err := queue.store.PruneReceipts(ctx, time.Now().UTC().Add(-queue.config.ReceiptRetention))
	return int(removed), err
}

func (queue *SharedQueue) observe(queued item, state State) {
	if queue.config.Observer == nil {
		return
	}
	queue.config.Observer.RecordDeliveryEvent(Event{
		ID: queued.ID, State: state, Adapter: queued.Delivery.AdapterName(),
		Target: queued.Delivery.Target, Attempts: queued.Attempts, Error: queued.LastError,
		CreatedAt: queued.CreatedAt, UpdatedAt: queued.UpdatedAt,
	})
}
