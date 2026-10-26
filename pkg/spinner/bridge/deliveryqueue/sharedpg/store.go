// Package sharedpg provides the transactional storage primitive for a
// PostgreSQL-backed Gateway delivery queue. It is not the local file spool.
package sharedpg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var ErrConflict = errors.New("delivery ID already exists")
var ErrLeaseLost = errors.New("delivery lease is no longer owned by this worker")

type Record struct {
	ID                     string
	Payload                []byte
	State                  string
	EventID                string
	NextAttemptAt          time.Time
	IdempotencyFingerprint string
	IdempotencyExpiresAt   time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type Lease struct {
	Record
	Owner string
	Token int64
	Until time.Time
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("shared delivery store requires a database connection")
	}
	return &Store{db: db}, nil
}

// CheckSchema fails startup if the Planner migration has not been applied.
func (store *Store) CheckSchema(ctx context.Context) error {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, payload, state, event_id, next_attempt_at, lease_owner,
       lease_token, lease_until, idempotency_fingerprint,
       idempotency_expires_at, created_at, updated_at
FROM deliveries LIMIT 0`)
	if err != nil {
		return fmt.Errorf("shared delivery schema is unavailable: %w", err)
	}
	return rows.Close()
}

// Put persists a complete delivery before ingress acknowledges it. A duplicate
// ID is a conflict; callers must not silently replace a queued payload.
func (store *Store) Put(ctx context.Context, record Record) error {
	if !idPattern.MatchString(record.ID) || !json.Valid(record.Payload) ||
		(record.State != "queued" && record.State != "deferred") || record.NextAttemptAt.IsZero() ||
		(record.IdempotencyFingerprint == "") != record.IdempotencyExpiresAt.IsZero() ||
		(record.IdempotencyFingerprint != "" && (!fingerprintPattern.MatchString(record.IdempotencyFingerprint) || !record.IdempotencyExpiresAt.After(time.Now().UTC()))) {
		return errors.New("invalid shared delivery record")
	}
	var fingerprint, expiresAt any
	if record.IdempotencyFingerprint != "" {
		fingerprint, expiresAt = record.IdempotencyFingerprint, record.IdempotencyExpiresAt
	}
	result, err := store.db.ExecContext(ctx, `
INSERT INTO deliveries (id, payload, state, event_id, next_attempt_at,
                        idempotency_fingerprint, idempotency_expires_at,
                        lease_token, created_at, updated_at)
VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, 0, now(), now())
ON CONFLICT (id) DO NOTHING`, record.ID, string(record.Payload), record.State, record.EventID, record.NextAttemptAt, fingerprint, expiresAt)
	if err != nil {
		return fmt.Errorf("persist shared delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrConflict
	}
	return nil
}

// Claim uses PostgreSQL row locks to assign one due delivery to one worker.
// Database time, not a Spinner node's clock, determines lease expiry.
func (store *Store) Claim(ctx context.Context, owner string, duration time.Duration) (*Lease, error) {
	return store.claim(ctx, owner, duration, nil)
}

// ClaimCompatible only leases deliveries supported by this worker. Multiple
// Caddy modules can therefore share one table without a HTTP-only worker
// terminally failing a delivery that belongs to another adapter-capable worker.
func (store *Store) ClaimCompatible(ctx context.Context, owner string, duration time.Duration, adapters []string) (*Lease, error) {
	if len(adapters) == 0 {
		return nil, errors.New("shared delivery worker has no adapters")
	}
	for _, adapter := range adapters {
		if adapter == "" || len(adapter) > 64 {
			return nil, errors.New("invalid shared delivery adapter name")
		}
	}
	return store.claim(ctx, owner, duration, adapters)
}

func (store *Store) claim(ctx context.Context, owner string, duration time.Duration, adapters []string) (*Lease, error) {
	if !idPattern.MatchString(owner) || duration < time.Second || duration > 24*time.Hour {
		return nil, errors.New("invalid shared delivery lease owner or duration")
	}
	query := `
WITH picked AS (
    SELECT id FROM deliveries
    WHERE state IN ('queued', 'deferred')
      AND next_attempt_at <= now()
      AND (lease_until IS NULL OR lease_until <= now())`
	args := []any{owner, duration.Microseconds()}
	if len(adapters) > 0 {
		placeholders := make([]string, 0, len(adapters))
		for _, adapter := range adapters {
			args = append(args, adapter)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		query += "\n      AND (payload->>'adapter' IN (" + strings.Join(placeholders, ", ") + ") OR NULLIF(payload->>'adapter', '') IS NULL)"
	}
	query += `
    ORDER BY next_attempt_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE deliveries AS delivery
SET lease_owner = $1,
    lease_token = delivery.lease_token + 1,
    lease_until = now() + ($2::bigint * interval '1 microsecond'),
    updated_at = now()
FROM picked
WHERE delivery.id = picked.id
RETURNING delivery.id, delivery.payload, delivery.state,
          COALESCE(delivery.event_id, ''), delivery.next_attempt_at,
          delivery.created_at, delivery.updated_at,
          delivery.lease_token, delivery.lease_until`
	row := store.db.QueryRowContext(ctx, query, args...)
	var lease Lease
	if err := row.Scan(&lease.ID, &lease.Payload, &lease.State, &lease.EventID,
		&lease.NextAttemptAt, &lease.CreatedAt, &lease.UpdatedAt,
		&lease.Token, &lease.Until); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("claim shared delivery: %w", err)
	}
	lease.Owner = owner
	return &lease, nil
}

// Renew extends only the current, unexpired lease. A stalled worker cannot
// revive a lease after another worker has claimed the same delivery.
func (store *Store) Renew(ctx context.Context, lease Lease, duration time.Duration) (time.Time, error) {
	if !validLease(lease) || duration < time.Second || duration > 24*time.Hour {
		return time.Time{}, errors.New("invalid shared delivery lease renewal")
	}
	var until time.Time
	err := store.db.QueryRowContext(ctx, `
UPDATE deliveries
SET lease_until = now() + ($4::bigint * interval '1 microsecond'), updated_at = now()
WHERE id = $1 AND lease_owner = $2 AND lease_token = $3 AND lease_until > now()
  AND state IN ('queued', 'deferred')
RETURNING lease_until`, lease.ID, lease.Owner, lease.Token, duration.Microseconds()).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrLeaseLost
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("renew shared delivery lease: %w", err)
	}
	return until, nil
}

// Settle applies a worker's result only while its fencing token is current.
// The full payload is replaced so a delivered receipt can be redacted.
func (store *Store) Settle(ctx context.Context, lease Lease, state string, payload []byte, nextAttemptAt time.Time) error {
	if !validLease(lease) || !json.Valid(payload) ||
		(state != "delivered" && state != "failed" && state != "deferred") ||
		(state == "deferred" && nextAttemptAt.IsZero()) {
		return errors.New("invalid shared delivery settlement")
	}
	var next any
	if state == "deferred" {
		next = nextAttemptAt
	}
	result, err := store.db.ExecContext(ctx, `
UPDATE deliveries
SET state = $4, payload = $5, next_attempt_at = COALESCE($6, next_attempt_at),
    lease_owner = NULL, lease_until = NULL, updated_at = now()
WHERE id = $1 AND lease_owner = $2 AND lease_token = $3 AND lease_until > now()
  AND state IN ('queued', 'deferred')`, lease.ID, lease.Owner, lease.Token, state, string(payload), next)
	if err != nil {
		return fmt.Errorf("settle shared delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrLeaseLost
	}
	return nil
}

// SettleWithFollowUp atomically fails an original delivery and queues the
// deterministic follow-up returned by its owning Extension. A crash cannot
// leave one without the other, and the lease fence rejects stale workers.
func (store *Store) SettleWithFollowUp(ctx context.Context, lease Lease, failedPayload []byte, followUp Record) error {
	if !validLease(lease) || !json.Valid(failedPayload) ||
		!idPattern.MatchString(followUp.ID) || followUp.ID == lease.ID ||
		!json.Valid(followUp.Payload) || followUp.State != "queued" || followUp.NextAttemptAt.IsZero() ||
		followUp.IdempotencyFingerprint != "" || !followUp.IdempotencyExpiresAt.IsZero() {
		return errors.New("invalid shared delivery failure follow-up")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin shared failure follow-up transaction: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE deliveries
SET state = 'failed', payload = $4, lease_owner = NULL,
    lease_until = NULL, updated_at = now()
WHERE id = $1 AND lease_owner = $2 AND lease_token = $3 AND lease_until > now()
  AND state IN ('queued', 'deferred')`, lease.ID, lease.Owner, lease.Token, string(failedPayload))
	if err != nil {
		return fmt.Errorf("settle shared delivery before failure follow-up: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrLeaseLost
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO deliveries (id, payload, state, event_id, next_attempt_at,
                        lease_token, created_at, updated_at)
VALUES ($1, $2, 'queued', NULLIF($3, ''), $4, 0, now(), now())`,
		followUp.ID, string(followUp.Payload), followUp.EventID, followUp.NextAttemptAt)
	if err != nil {
		return fmt.Errorf("persist shared failure follow-up: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit shared failure follow-up: %w", err)
	}
	return nil
}

func validLease(lease Lease) bool {
	return idPattern.MatchString(lease.ID) && idPattern.MatchString(lease.Owner) && lease.Token > 0
}

// Find returns a private record, including its payload. It must not be used
// directly by a public management API.
func (store *Store) Find(ctx context.Context, id string) (*Record, error) {
	if !idPattern.MatchString(id) {
		return nil, nil
	}
	row := store.db.QueryRowContext(ctx, `
SELECT id, payload, state, COALESCE(event_id, ''), next_attempt_at,
       COALESCE(idempotency_fingerprint, ''), idempotency_expires_at,
       created_at, updated_at
FROM deliveries WHERE id = $1`, id)
	var record Record
	var expiresAt sql.NullTime
	if err := row.Scan(&record.ID, &record.Payload, &record.State, &record.EventID,
		&record.NextAttemptAt, &record.IdempotencyFingerprint, &expiresAt,
		&record.CreatedAt, &record.UpdatedAt); errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("find shared delivery: %w", err)
	}
	if expiresAt.Valid {
		record.IdempotencyExpiresAt = expiresAt.Time
	}
	return &record, nil
}

// List returns recent private records. The caller must convert each payload
// to a metadata-only status before exposing it outside the trusted process.
func (store *Store) List(ctx context.Context, state, eventID string, limit int) ([]Record, error) {
	if limit < 1 || limit > 100 || (eventID == "" && !validState(state)) {
		return nil, errors.New("invalid shared delivery list filter")
	}
	query := `SELECT id, payload, state, COALESCE(event_id, ''), next_attempt_at, created_at, updated_at
FROM deliveries WHERE state = $1 ORDER BY updated_at DESC, id DESC LIMIT $2`
	var args []any = []any{state, limit}
	if eventID != "" {
		query = `SELECT id, payload, state, COALESCE(event_id, ''), next_attempt_at, created_at, updated_at
FROM deliveries WHERE event_id = $1 ORDER BY updated_at DESC, id DESC LIMIT $2`
		args[0] = eventID
	}
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list shared deliveries: %w", err)
	}
	defer rows.Close()
	records := make([]Record, 0)
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.ID, &record.Payload, &record.State, &record.EventID,
			&record.NextAttemptAt, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (store *Store) Counts(ctx context.Context) (pending, failed int, err error) {
	err = store.db.QueryRowContext(ctx, `
SELECT count(*) FILTER (WHERE state IN ('queued', 'deferred')),
       count(*) FILTER (WHERE state = 'failed') FROM deliveries`).Scan(&pending, &failed)
	return
}

// Retry moves an unleased failed delivery back to the due queue. A concurrent
// retry succeeds only once; callers should re-read the state on zero rows.
func (store *Store) Retry(ctx context.Context, id string, payload []byte) (bool, error) {
	if !idPattern.MatchString(id) || !json.Valid(payload) {
		return false, errors.New("invalid shared delivery retry")
	}
	result, err := store.db.ExecContext(ctx, `
UPDATE deliveries SET state = 'queued', payload = $2, next_attempt_at = now(),
    lease_owner = NULL, lease_until = NULL, updated_at = now()
WHERE id = $1 AND state = 'failed' AND lease_owner IS NULL`, id, string(payload))
	if err != nil {
		return false, fmt.Errorf("retry shared delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// PruneReceipts removes only expired delivered receipts. An idempotency
// reservation remains until its own expiry even if ordinary retention elapsed.
func (store *Store) PruneReceipts(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := store.db.ExecContext(ctx, `
DELETE FROM deliveries WHERE state = 'delivered' AND updated_at < $1
AND (idempotency_expires_at IS NULL OR idempotency_expires_at < now())`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("prune shared delivery receipts: %w", err)
	}
	return result.RowsAffected()
}

func validState(state string) bool {
	return state == "queued" || state == "deferred" || state == "failed" || state == "delivered"
}
