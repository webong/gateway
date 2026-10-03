package sharedpg

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"
)

func TestConcurrentClaimsAndLeaseFencing(t *testing.T) {
	dsn := os.Getenv("GATEWAY_SHARED_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("set GATEWAY_SHARED_PG_TEST_DSN for the PostgreSQL integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE deliveries (
    id char(32) PRIMARY KEY, payload jsonb NOT NULL, state varchar(16) NOT NULL,
    event_id varchar(255), next_attempt_at timestamptz NOT NULL,
    lease_owner char(32), lease_token bigint NOT NULL DEFAULT 0,
    lease_until timestamptz, idempotency_fingerprint char(64),
    idempotency_expires_at timestamptz,
    created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
)`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS deliveries`) })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	record := Record{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Payload: []byte(`{"body":"private"}`),
		State: "queued", EventID: "event-1", NextAttemptAt: time.Now().Add(-time.Second),
	}
	if err := store.Put(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, record); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate delivery was accepted: %v", err)
	}
	owners := []string{"11111111111111111111111111111111", "22222222222222222222222222222222"}
	var leases [2]*Lease
	var failures [2]error
	var group sync.WaitGroup
	for index := range owners {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			leases[index], failures[index] = store.Claim(ctx, owners[index], time.Second)
		}(index)
	}
	group.Wait()
	if failures[0] != nil || failures[1] != nil {
		t.Fatalf("concurrent claims failed: %v, %v", failures[0], failures[1])
	}
	first := leases[0]
	if first == nil {
		first = leases[1]
	}
	if first == nil || (leases[0] != nil && leases[1] != nil) {
		t.Fatalf("delivery was claimed by zero or two workers: %+v, %+v", leases[0], leases[1])
	}
	if string(first.Payload) != `{"body": "private"}` && string(first.Payload) != `{"body":"private"}` {
		t.Fatalf("claimed payload changed: %s", first.Payload)
	}
	if second, err := store.Claim(ctx, owners[1], time.Second); err != nil || second != nil {
		t.Fatalf("active lease was claimed again: %+v, %v", second, err)
	}
	time.Sleep(1100 * time.Millisecond)
	otherOwner := owners[0]
	if first.Owner == otherOwner {
		otherOwner = owners[1]
	}
	second, err := store.Claim(ctx, otherOwner, 3*time.Second)
	if err != nil || second == nil || second.Token <= first.Token {
		t.Fatalf("expired lease was not reclaimed with a new fence: %+v, %v", second, err)
	}
	if err := store.Settle(ctx, *first, "delivered", []byte(`{}`), time.Time{}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale worker settled a reassigned delivery: %v", err)
	}
	notice := Record{
		ID: "cccccccccccccccccccccccccccccccc", Payload: []byte(`{"notice":true}`),
		State: "queued", NextAttemptAt: time.Now().UTC(),
	}
	if err := store.SettleWithNotice(ctx, *first, []byte(`{"failed":true}`), notice); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale worker created a failure notice: %v", err)
	}
	if record, err := store.Find(ctx, notice.ID); err != nil || record != nil {
		t.Fatalf("stale failure notice transaction left a row: %+v, %v", record, err)
	}
	if err := store.Put(ctx, Record{
		ID: notice.ID, Payload: []byte(`{"occupied":true}`),
		State: "queued", NextAttemptAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SettleWithNotice(ctx, *second, []byte(`{"failed":true}`), notice); err == nil {
		t.Fatal("notice ID collision did not roll back original settlement")
	}
	if current, err := store.Find(ctx, first.ID); err != nil || current == nil || current.State != "queued" {
		t.Fatalf("failed notice transaction changed original delivery: %+v, %v", current, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM deliveries WHERE id = $1`, notice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Renew(ctx, *first, time.Second); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale worker renewed a reassigned delivery: %v", err)
	}
	if _, err := store.Renew(ctx, *second, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := store.Settle(ctx, *second, "delivered", []byte(`{}`), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if next, err := store.Claim(ctx, first.Owner, time.Second); err != nil || next != nil {
		t.Fatalf("delivered record returned to the queue: %+v, %v", next, err)
	}
	var state string
	var payload []byte
	if err := db.QueryRowContext(ctx, `SELECT state, payload FROM deliveries WHERE id = $1`, record.ID).Scan(&state, &payload); err != nil {
		t.Fatal(err)
	}
	if state != "delivered" || string(payload) != "{}" {
		t.Fatalf("delivery did not settle or redact: state=%s payload=%s", state, payload)
	}

	retry := Record{
		ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Payload: []byte(`{"body":"retry"}`),
		State: "queued", NextAttemptAt: time.Now().Add(-time.Second),
	}
	if err := store.Put(ctx, retry); err != nil {
		t.Fatal(err)
	}
	retryLease, err := store.Claim(ctx, owners[0], 3*time.Second)
	if err != nil || retryLease == nil || retryLease.ID != retry.ID {
		t.Fatalf("retry record was not claimed: %+v, %v", retryLease, err)
	}
	if err := store.Settle(ctx, *retryLease, "deferred", retry.Payload, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if next, err := store.Claim(ctx, owners[1], time.Second); err != nil || next != nil {
		t.Fatalf("future deferred delivery was claimed: %+v, %v", next, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE deliveries SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, retry.ID); err != nil {
		t.Fatal(err)
	}
	retryLease, err = store.Claim(ctx, owners[1], 3*time.Second)
	if err != nil || retryLease == nil || retryLease.Token != 2 {
		t.Fatalf("due deferred delivery was not reclaimed: %+v, %v", retryLease, err)
	}
	if err := store.Settle(ctx, *retryLease, "failed", retry.Payload, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if next, err := store.Claim(ctx, owners[0], time.Second); err != nil || next != nil {
		t.Fatalf("terminal failed delivery returned to the queue: %+v, %v", next, err)
	}
}
