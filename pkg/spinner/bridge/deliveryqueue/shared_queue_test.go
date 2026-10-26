package deliveryqueue

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

func TestSharedQueueAcrossTwoWorkers(t *testing.T) {
	dsn := os.Getenv("GATEWAY_SHARED_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("set GATEWAY_SHARED_PG_TEST_DSN for the shared queue integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE deliveries (
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
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS deliveries`) })
	var fail atomic.Bool
	var attempts atomic.Int32
	notices := make(chan bridge.GatewayDelivery, 1)
	adapter := bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, delivery bridge.GatewayDelivery) error {
		if delivery.Attributes["mail_from"] == "" {
			notices <- delivery
			return nil
		}
		attempts.Add(1)
		if fail.Load() {
			return bridge.ErrPermanentDelivery
		}
		return nil
	})
	config := Config{
		Adapters: map[string]bridge.GatewayDeliveryAdapter{"smtp": adapter},
		FailureHandlerResolver: failureResolver{"smtp": failureHandlerFunc(func(_ context.Context, request bridge.DeliveryFailureRequest) (*bridge.GatewayDelivery, error) {
			if request.Delivery.Attributes["request_follow_up"] != "yes" {
				return nil, nil
			}
			return &bridge.GatewayDelivery{
				Protocol: bridge.ProtocolSMTP, Adapter: "smtp", Target: "sender@example.test",
				Attributes: map[string]string{"mail_from": ""}, Payload: []byte("extension-owned failure follow-up"),
			}, nil
		})},
		MaxAttempts: 1, InitialBackoff: 10 * time.Millisecond,
		MaxBackoff: time.Second, DeliveryTimeout: time.Second,
		PollInterval: 10 * time.Millisecond, ReceiptRetention: time.Hour,
	}
	first, err := NewShared(config, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeSharedQueue(t, first) })
	second, err := NewShared(config, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeSharedQueue(t, second) })
	delivery := testDelivery()
	delivery.Attributes["event_id"] = "shared-event"
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fingerprint := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if replayed, err := first.EnqueueGatewayIdempotent(delivery, id, fingerprint, time.Now().Add(time.Hour)); err != nil || replayed {
		t.Fatalf("initial shared enqueue: replayed=%v err=%v", replayed, err)
	}
	waitSharedState(t, second, id, StateDelivered)
	if attempts.Load() != 1 {
		t.Fatalf("two workers delivered one item %d times", attempts.Load())
	}
	if replayed, err := second.EnqueueGatewayIdempotent(delivery, id, fingerprint, time.Now().Add(time.Hour)); err != nil || !replayed {
		t.Fatalf("idempotent replay failed: replayed=%v err=%v", replayed, err)
	}
	if statuses, err := first.ListEvent("shared-event", 10); err != nil || len(statuses) != 1 || statuses[0].State != StateDelivered {
		t.Fatalf("shared event status missing: %+v %v", statuses, err)
	}
	if pending, failed, err := second.Counts(); err != nil || pending != 0 || failed != 0 {
		t.Fatalf("shared counts are wrong: pending=%d failed=%d err=%v", pending, failed, err)
	}

	fail.Store(true)
	failedID, err := first.EnqueueGatewayWithID(testDelivery())
	if err != nil {
		t.Fatal(err)
	}
	waitSharedState(t, second, failedID, StateFailed)
	fail.Store(false)
	if err := first.Retry(failedID); err != nil {
		t.Fatal(err)
	}
	waitSharedState(t, first, failedID, StateDelivered)
	if err := first.Retry(id); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("delivered receipt was retried: %v", err)
	}

	fail.Store(true)
	bounced := testDelivery()
	bounced.Attributes["request_follow_up"] = "yes"
	bouncedID, err := first.EnqueueGatewayWithID(bounced)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitSharedState(t, second, bouncedID, StateFailed)
	if failed.FollowUpID != failureFollowUpID(bouncedID) {
		t.Fatalf("failed original was not linked to its follow-up: %+v", failed)
	}
	waitSharedState(t, first, failed.FollowUpID, StateDelivered)
	select {
	case followUp := <-notices:
		if followUp.Target != "sender@example.test" || followUp.Attributes["mail_from"] != "" ||
			string(followUp.Payload) != "extension-owned failure follow-up" {
			t.Fatalf("shared failure follow-up has wrong content: %+v", followUp)
		}
	default:
		t.Fatal("shared failure follow-up was not delivered")
	}
	if err := first.Retry(bouncedID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("failed original with a follow-up was retried: %v", err)
	}

	corruptID := "cccccccccccccccccccccccccccccccc"
	_, err = db.Exec(`INSERT INTO deliveries
    (id, payload, state, event_id, next_attempt_at, created_at, updated_at)
    VALUES ($1, $2::jsonb, 'queued', 'corrupt-event', now(), now(), now())`,
		corruptID, `{"unexpected":"secret"}`)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := waitSharedState(t, second, corruptID, StateFailed)
	if corrupt.EventID != "corrupt-event" || corrupt.Attempts != 1 {
		t.Fatalf("invalid delivery receipt lost metadata: %+v", corrupt)
	}
	if err := first.Retry(corruptID); err == nil {
		t.Fatal("invalid persisted delivery was retried")
	}
	var persisted string
	if err := db.QueryRow(`SELECT payload::text FROM deliveries WHERE id = $1`, corruptID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, "secret") {
		t.Fatalf("invalid payload was retained in failed receipt: %s", persisted)
	}
}

func TestSharedQueueTakesOverAfterWorkerProcessDies(t *testing.T) {
	dsn := os.Getenv("GATEWAY_SHARED_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("set GATEWAY_SHARED_PG_TEST_DSN for the shared queue integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE deliveries (
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
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS deliveries`) })
	id := "dddddddddddddddddddddddddddddddd"
	now := time.Now().UTC()
	delivery := testDelivery()
	payload, err := json.Marshal(item{
		Version: spoolVersion, ID: id, Delivery: delivery, Protocol: delivery.Protocol,
		Adapter: delivery.AdapterName(), State: StateQueued,
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO deliveries
    (id, payload, state, next_attempt_at, created_at, updated_at)
    VALUES ($1, $2::jsonb, 'queued', now(), now(), now())`, id, string(payload))
	if err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestSharedQueueCrashWorker$")
	command.Env = append(os.Environ(), "GATEWAY_SHARED_CRASH_CHILD=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	claimed := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "CLAIMED ") {
				claimed <- scanner.Text()
				return
			}
		}
		claimed <- ""
	}()
	select {
	case marker := <-claimed:
		if marker != "CLAIMED "+id {
			t.Fatalf("crash worker did not claim delivery: %q", marker)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("crash worker did not claim delivery")
	}
	var token int64
	if err := db.QueryRow(`SELECT lease_token FROM deliveries WHERE id = $1`, id).Scan(&token); err != nil || token != 1 {
		t.Fatalf("first process did not own lease: token=%d err=%v", token, err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()

	var attempts atomic.Int32
	adapter := bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		attempts.Add(1)
		return nil
	})
	queue, err := NewShared(Config{
		Adapters:        map[string]bridge.GatewayDeliveryAdapter{"smtp": adapter},
		DeliveryTimeout: time.Second, PollInterval: 20 * time.Millisecond,
	}, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeSharedQueue(t, queue) })
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		status, err := queue.Status(id)
		if err == nil && status.State == StateDelivered {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	status, err := queue.Status(id)
	if err != nil || status.State != StateDelivered || attempts.Load() != 1 {
		t.Fatalf("surviving process did not finish delivery: status=%+v attempts=%d err=%v", status, attempts.Load(), err)
	}
	if err := db.QueryRow(`SELECT lease_token FROM deliveries WHERE id = $1`, id).Scan(&token); err != nil || token != 2 {
		t.Fatalf("delivery was not reassigned with a fenced lease: token=%d err=%v", token, err)
	}
}

func TestSharedQueueOnlyClaimsSupportedAdapters(t *testing.T) {
	dsn := os.Getenv("GATEWAY_SHARED_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("set GATEWAY_SHARED_PG_TEST_DSN for the shared queue integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE deliveries (
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
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS deliveries`) })
	var wrongAdapterCalls, smtpCalls atomic.Int32
	httpOnly, err := NewShared(Config{
		Adapters: map[string]bridge.GatewayDeliveryAdapter{
			"http": bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
				wrongAdapterCalls.Add(1)
				return nil
			}),
		}, PollInterval: 10 * time.Millisecond,
	}, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeSharedQueue(t, httpOnly) })
	id := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	now := time.Now().UTC()
	delivery := testDelivery()
	payload, err := json.Marshal(item{
		Version: spoolVersion, ID: id, Delivery: delivery, Protocol: delivery.Protocol,
		Adapter: delivery.AdapterName(), State: StateQueued,
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO deliveries
    (id, payload, state, next_attempt_at, created_at, updated_at)
    VALUES ($1, $2::jsonb, 'queued', now(), now(), now())`, id, string(payload))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	var state string
	var token int64
	if err := db.QueryRow(`SELECT state, lease_token FROM deliveries WHERE id = $1`, id).Scan(&state, &token); err != nil || state != "queued" || token != 0 || wrongAdapterCalls.Load() != 0 {
		t.Fatalf("HTTP-only worker claimed SMTP delivery: state=%s token=%d calls=%d err=%v", state, token, wrongAdapterCalls.Load(), err)
	}
	smtpOnly, err := NewShared(Config{
		Adapters: map[string]bridge.GatewayDeliveryAdapter{
			"smtp": bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
				smtpCalls.Add(1)
				return nil
			}),
		}, PollInterval: 10 * time.Millisecond,
	}, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeSharedQueue(t, smtpOnly) })
	waitSharedState(t, httpOnly, id, StateDelivered)
	if smtpCalls.Load() != 1 || wrongAdapterCalls.Load() != 0 {
		t.Fatalf("delivery went to wrong worker: smtp=%d http=%d", smtpCalls.Load(), wrongAdapterCalls.Load())
	}
}

func TestSharedQueueCrashWorker(t *testing.T) {
	if os.Getenv("GATEWAY_SHARED_CRASH_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	adapter := bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, delivery bridge.GatewayDelivery) error {
		fmt.Fprintln(os.Stdout, "CLAIMED dddddddddddddddddddddddddddddddd")
		select {}
	})
	_, err := NewShared(Config{
		Adapters:        map[string]bridge.GatewayDeliveryAdapter{"smtp": adapter},
		DeliveryTimeout: time.Second, PollInterval: 20 * time.Millisecond,
	}, os.Getenv("GATEWAY_SHARED_PG_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	select {}
}

func waitSharedState(t *testing.T, queue *SharedQueue, id string, wanted ...State) Status {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		status, err := queue.Status(id)
		if err == nil {
			for _, state := range wanted {
				if status.State == state {
					return status
				}
			}
		}
	}
	status, err := queue.Status(id)
	t.Fatalf("shared delivery %s did not reach %v: %+v %v", id, wanted, status, err)
	return Status{}
}

func closeSharedQueue(t *testing.T, queue *SharedQueue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := queue.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
