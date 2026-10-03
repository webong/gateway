package router

import (
	"net/http"
	"time"
)

// provisioningControlHandler exposes only the Planner's provisioning API on a
// separate, operator-controlled listener. The Planner still authenticates each
// request with its internal or target-worker credential.
func provisioningControlHandler(planner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !under(r.URL.Path, "/_internal/provisioning") {
			http.NotFound(w, r)
			return
		}
		planner.ServeHTTP(w, r)
	})
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
