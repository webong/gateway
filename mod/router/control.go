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
		if !under(r.URL.Path, "/_internal/provisioning") &&
			!mailboxCredentialRequest(r) && !emailRuntimeRequest(r) {
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

// emailRuntimeRequest allows only the private Planner operations required by
// the SMTP runtime. Public email management APIs remain on the public Router.
func emailRuntimeRequest(r *http.Request) bool {
	const prefix = "/_internal/gateway/email/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(r.URL.Path, prefix)

	switch {
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		strings.HasPrefix(remainder, "dkim/"):
		return singlePathSegment(strings.TrimPrefix(remainder, "dkim/"))
	case r.Method == http.MethodGet && strings.HasPrefix(remainder, "hosted-adapters/"):
		return singlePathSegment(strings.TrimPrefix(remainder, "hosted-adapters/"))
	case r.Method == http.MethodPost && remainder == "outbound-plan":
		return true
	case r.Method == http.MethodPost && strings.HasPrefix(remainder, "outbound-attempts/"):
		attempt := strings.TrimPrefix(remainder, "outbound-attempts/")
		if strings.HasSuffix(attempt, "/reserve") {
			attempt = strings.TrimSuffix(attempt, "/reserve")
		}
		return singlePathSegment(attempt)
	default:
		return false
	}
}

func singlePathSegment(value string) bool {
	return value != "" && !strings.Contains(value, "/")
}

// NewProvisioningControlServer serves the narrow private Planner interface
// used by provisioning and transport workers, independent of the PHP adapter.
func NewProvisioningControlServer(planner http.Handler) *http.Server {
	return &http.Server{
		Handler:           provisioningControlHandler(planner),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}
