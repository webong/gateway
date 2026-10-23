package caddy

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	caddyserver "github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	_ "github.com/jackc/pgx/v4/stdlib"

	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
)

func TestCaddySharedDeliveryConfiguration(t *testing.T) {
	handler := &CaddyHandler{}
	if err := handler.UnmarshalCaddyfile(caddyfile.NewTestDispenser(`gateway {
    internal_token test-token
    delivery_database_url postgres://gateway@db.internal/gateway
}`)); err != nil {
		t.Fatal(err)
	}
	if handler.DeliveryDatabaseURL != "postgres://gateway@db.internal/gateway" || handler.Validate() != nil {
		t.Fatalf("Caddy handler did not accept shared delivery config: %+v", handler)
	}
	handler.SpoolPath = "/var/lib/gateway/deliveries"
	if err := handler.Validate(); err == nil {
		t.Fatal("Caddy handler accepted both delivery backends")
	}

	module, err := parseSMTPApp(caddyfile.NewTestDispenser(`gateway_smtp {
    internal_token test-token
    relay_address smtp.example.test:587
    delivery_database_url postgres://gateway@db.internal/gateway
}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	app := &SMTPApp{}
	if err := json.Unmarshal(module.(httpcaddyfile.App).Value, app); err != nil {
		t.Fatal(err)
	}
	app.applyDefaults()
	if app.DeliveryDatabaseURL != "postgres://gateway@db.internal/gateway" || app.Validate() != nil {
		t.Fatalf("Caddy SMTP app did not accept shared delivery config: %+v", app)
	}
	app.SpoolPath = "/var/lib/gateway/deliveries"
	if err := app.Validate(); err == nil {
		t.Fatal("Caddy SMTP app accepted both delivery backends")
	}
}

func TestCaddySharedPostgresQueuesProvision(t *testing.T) {
	dsn := os.Getenv("GATEWAY_SHARED_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("set GATEWAY_SHARED_PG_TEST_DSN for the Caddy shared queue integration test")
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
	app := &SMTPApp{
		InternalToken: "test-token", DeliveryDatabaseURL: dsn,
		RelayAddress: "127.0.0.1:2525", DSNDomain: "gateway.example.test",
	}
	if err := app.Provision(caddyserver.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Stop() })
	if _, ok := app.deliveryQueue.(*deliveryqueue.SharedQueue); !ok {
		t.Fatalf("Caddy SMTP app did not select PostgreSQL queue: %T", app.deliveryQueue)
	}
	handler := &CaddyHandler{InternalToken: "test-token", DeliveryDatabaseURL: dsn}
	if err := handler.Provision(caddyserver.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })
	if _, ok := handler.deliveryQueue.(*deliveryqueue.SharedQueue); !ok {
		t.Fatalf("Caddy HTTP handler did not select PostgreSQL queue: %T", handler.deliveryQueue)
	}
}

func TestCaddyDeliveryAdminMembership(t *testing.T) {
	dsn := os.Getenv("GATEWAY_DELIVERY_MEMBERSHIP_TEST_DSN")
	if dsn == "" {
		t.Skip("set GATEWAY_DELIVERY_MEMBERSHIP_TEST_DSN for Caddy membership integration")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const origin = "http://127.0.0.1:5052"
	handler := &CaddyHandler{
		InternalToken: "test-token", DeliveryDatabaseURL: dsn,
		AdminAddress: "127.0.0.1:0", AdminAdvertiseURL: origin,
	}
	if err := handler.Provision(caddyserver.Context{}); err != nil {
		t.Fatal(err)
	}
	defer handler.Cleanup()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM delivery_admin_endpoints WHERE origin = $1 AND expires_unix > $2`, origin, time.Now().Unix()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Caddy admin endpoint was not registered: count=%d err=%v", count, err)
	}
	if err := handler.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM delivery_admin_endpoints WHERE origin = $1`, origin).Scan(&count); err != nil || count != 0 {
		t.Fatalf("Caddy admin endpoint remained after cleanup: count=%d err=%v", count, err)
	}
}
