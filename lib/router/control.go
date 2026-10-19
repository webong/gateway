package router

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// provisioningControlHandler exposes only narrow private Planner APIs on a
// separate, operator-controlled listener. Planner authenticates every request.
func provisioningControlHandler(planner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !under(r.URL.Path, "/_internal/provisioning") && !mailboxCredentialRequest(r) {
			http.NotFound(w, r)
			return
		}
		planner.ServeHTTP(w, r)
	})
}

func mailboxCredentialRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	const prefix = "/_internal/gateway/mailbox-connections/"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, "/smtp") {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), "/smtp")
	if strings.Contains(id, "/") {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

// NewProvisioningControlServer serves the narrow private Planner interface
// used by a provisioning worker, independent of the PHP runtime adapter.
func NewProvisioningControlServer(planner http.Handler) *http.Server {
	return &http.Server{
		Handler:           provisioningControlHandler(planner),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}
