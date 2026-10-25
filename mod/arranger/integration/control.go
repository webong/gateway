package integration

import (
	"net/http"
	"time"
)

// InternalRequestPolicy decides whether a private Planner request is part of
// the capability surface exposed by a host. Policies must fail closed.
type InternalRequestPolicy func(*http.Request) bool

// AnyInternalRequest combines capability-owned policies without teaching the
// Router about those capabilities.
func AnyInternalRequest(policies ...InternalRequestPolicy) InternalRequestPolicy {
	return func(request *http.Request) bool {
		for _, policy := range policies {
			if policy != nil && policy(request) {
				return true
			}
		}
		return false
	}
}

// NewInternalControlHandler exposes only capability-approved private Planner
// APIs on a separate listener. Planner authenticates every accepted request.
func NewInternalControlHandler(planner http.Handler, allowed InternalRequestPolicy) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowed == nil || !allowed(r) {
			http.NotFound(w, r)
			return
		}
		planner.ServeHTTP(w, r)
	})
}

// NewInternalControlServer serves the narrow private Planner interface used by
// capability workers, independent of the Planner runtime adapter.
func NewInternalControlServer(planner http.Handler, allowed InternalRequestPolicy) *http.Server {
	return &http.Server{
		Handler:           NewInternalControlHandler(planner, allowed),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}
