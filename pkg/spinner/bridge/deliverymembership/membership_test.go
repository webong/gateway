package deliverymembership

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormalizeOrigin(t *testing.T) {
	for _, value := range []string{"http://router.internal:5052", "https://[::1]:5052/"} {
		if got, err := normalizeOrigin(value); err != nil || got != strings.TrimSuffix(value, "/") {
			t.Fatalf("normalizeOrigin(%q) = %q, %v", value, got, err)
		}
	}
	for _, value := range []string{"", "http://", "ftp://router.internal", "http://user@router.internal", "http://router.internal/path", "http://router.internal?query=1", "http://router.internal:99999", " http://router.internal", "http://router.internal\n"} {
		if got, err := normalizeOrigin(value); err == nil {
			t.Fatalf("normalizeOrigin(%q) accepted %q", value, got)
		}
	}
}

func TestSignatureMatchesPlannerWireFormat(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	const origin = "http://router.internal:5052"
	const token = "private-token"
	const expires = int64(1735689600)
	if got := signature(id, origin, expires, token); got != "a4e0a2570dce018961cc0d8c6fc2a39b0a41cc3b6d19b19b6b23c649a0286427" {
		t.Fatalf("signature changed: %s", got)
	}
}

func TestLeaseRegistersAndRemovesEndpoint(t *testing.T) {
	dsn := os.Getenv("GATEWAY_DELIVERY_MEMBERSHIP_TEST_DSN")
	if dsn == "" {
		t.Skip("GATEWAY_DELIVERY_MEMBERSHIP_TEST_DSN is not set")
	}
	const origin = "http://router.internal:5052"
	const token = "test-private-token"
	lease, err := Start(dsn, origin, token)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var registeredOrigin, signed string
	var expires int64
	if err := db.QueryRow(`SELECT origin, expires_unix, signature FROM delivery_admin_endpoints WHERE id = $1`, lease.id).Scan(&registeredOrigin, &expires, &signed); err != nil {
		t.Fatal(err)
	}
	if registeredOrigin != origin || expires <= time.Now().Unix() || signed != signature(lease.id, origin, expires, token) {
		t.Fatalf("invalid endpoint registration: origin=%q expires=%d signature=%q", registeredOrigin, expires, signed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lease.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM delivery_admin_endpoints WHERE id = $1`, lease.id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("endpoint registration remains after Close: count=%d err=%v", count, err)
	}
}

func TestStartPrunesExpiredEndpoints(t *testing.T) {
	dsn := os.Getenv("GATEWAY_DELIVERY_MEMBERSHIP_TEST_DSN")
	if dsn == "" {
		t.Skip("GATEWAY_DELIVERY_MEMBERSHIP_TEST_DSN is not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const staleID = "ffffffffffffffffffffffffffffffff"
	if _, err := db.Exec(`INSERT INTO delivery_admin_endpoints (id, origin, expires_unix, signature) VALUES ($1, $2, $3, $4) ON CONFLICT (id) DO UPDATE SET expires_unix = EXCLUDED.expires_unix`, staleID, "http://stale.internal:5052", time.Now().Add(-time.Minute).Unix(), strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM delivery_admin_endpoints WHERE id = $1`, staleID)
	lease, err := Start(dsn, "http://current.internal:5052", "test-private-token")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = lease.Close(ctx)
	}()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM delivery_admin_endpoints WHERE id = $1`, staleID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale endpoint remained: count=%d err=%v", count, err)
	}
}
