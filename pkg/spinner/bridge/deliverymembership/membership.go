// Package deliverymembership advertises live private delivery-admin listeners
// through the shared PostgreSQL database used by all Spinner replicas.
package deliverymembership

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"
)

const (
	heartbeatInterval = 5 * time.Second
	leaseDuration     = 20 * time.Second
	pruneInterval     = time.Hour
)

// Lease owns one ephemeral endpoint registration. A crashed process disappears
// from Planner discovery after its signed expiry even if Close never runs.
type Lease struct {
	db        *sql.DB
	id        string
	origin    string
	token     string
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// ListenAndServe binds before publishing membership and owns the registration
// for precisely the lifetime of the private admin listener.
func ListenAndServe(server *http.Server, databaseURL, advertiseURL, token string) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	lease, err := Start(databaseURL, advertiseURL, token)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if closeErr := lease.Close(ctx); closeErr != nil {
			log.Printf("delivery admin membership cleanup failed: %v", closeErr)
		}
	}()
	return server.Serve(listener)
}

// Start registers a listener after it has bound successfully. An empty
// advertise URL disables discovery; a non-empty one requires shared storage.
func Start(databaseURL, advertiseURL, token string) (*Lease, error) {
	if strings.TrimSpace(advertiseURL) == "" {
		return nil, nil
	}
	if strings.TrimSpace(databaseURL) == "" || strings.TrimSpace(token) == "" {
		return nil, errors.New("delivery admin discovery requires shared PostgreSQL and an internal token")
	}
	origin, err := normalizeOrigin(advertiseURL)
	if err != nil {
		return nil, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("generate delivery admin endpoint ID: %w", err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open delivery admin membership database: %w", err)
	}
	db.SetMaxOpenConns(1)
	lease := &Lease{db: db, id: hex.EncodeToString(random[:]), origin: origin, token: token, done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = lease.renew(ctx)
	cancel()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("register delivery admin endpoint: %w", err)
	}
	pruneCtx, pruneCancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := lease.prune(pruneCtx); err != nil {
		log.Printf("delivery admin membership pruning failed: %v", err)
	}
	pruneCancel()
	ctx, lease.cancel = context.WithCancel(context.Background())
	go lease.run(ctx)

	return lease, nil
}

func (lease *Lease) run(ctx context.Context) {
	defer close(lease.done)
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	pruneTicker := time.NewTicker(pruneInterval)
	defer pruneTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			if err := lease.renew(renewCtx); err != nil && ctx.Err() == nil {
				log.Printf("delivery admin membership heartbeat failed: %v", err)
			}
			cancel()
		case <-pruneTicker.C:
			pruneCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			if err := lease.prune(pruneCtx); err != nil && ctx.Err() == nil {
				log.Printf("delivery admin membership pruning failed: %v", err)
			}
			cancel()
		}
	}
}

func (lease *Lease) prune(ctx context.Context) error {
	_, err := lease.db.ExecContext(ctx, `DELETE FROM delivery_admin_endpoints WHERE expires_unix <= $1`, time.Now().UTC().Unix())
	return err
}

func (lease *Lease) renew(ctx context.Context) error {
	expires := time.Now().UTC().Add(leaseDuration).Unix()
	_, err := lease.db.ExecContext(ctx, `
		INSERT INTO delivery_admin_endpoints (id, origin, expires_unix, signature)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			origin = EXCLUDED.origin,
			expires_unix = EXCLUDED.expires_unix,
			signature = EXCLUDED.signature
	`, lease.id, lease.origin, expires, signature(lease.id, lease.origin, expires, lease.token))
	return err
}

// Close stops heartbeats and removes the registration; expiry is the fallback
// when shutdown cannot reach PostgreSQL.
func (lease *Lease) Close(ctx context.Context) error {
	if lease == nil {
		return nil
	}
	lease.closeOnce.Do(func() {
		lease.cancel()
		select {
		case <-lease.done:
		case <-ctx.Done():
			lease.closeErr = ctx.Err()
		}
		if lease.closeErr == nil {
			_, lease.closeErr = lease.db.ExecContext(ctx, `DELETE FROM delivery_admin_endpoints WHERE id = $1`, lease.id)
		}
		lease.closeErr = errors.Join(lease.closeErr, lease.db.Close())
	})
	return lease.closeErr
}

func signature(id, origin string, expires int64, token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte(id + "\n" + origin + "\n" + strconv.FormatInt(expires, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func normalizeOrigin(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("delivery admin advertise URL must be a private HTTP(S) origin")
	}
	if len(value) > 2048 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\t") || parsed.Opaque != "" {
		return "", errors.New("delivery admin advertise URL is invalid")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", errors.New("delivery admin advertise URL has an invalid port")
		}
	}
	return strings.TrimSuffix(value, "/"), nil
}
