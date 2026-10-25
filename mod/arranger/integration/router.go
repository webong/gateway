package integration

import (
	"net/http"
	"strings"
)

// ManagedRouteHandler resolves and serves an application-managed route before
// a request falls through to Planner.
type ManagedRouteHandler interface {
	ServeWithPlanner(http.ResponseWriter, *http.Request, http.Handler) bool
}

// PublicRoute adds a host-provided route without teaching the shared Router
// about a specific transport or product capability.
type PublicRoute struct {
	Prefix  string
	Handler http.Handler
}

// NewHandler builds Gateway's public HTTP surface. Internal Planner routes are
// never exposed here; hosts expose approved routes on the private listener.
func NewHandler(planner, hooks http.Handler, managed ManagedRouteHandler, routes ...PublicRoute) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if under(r.URL.Path, "/_internal") {
			http.NotFound(w, r)
			return
		}
		for _, route := range routes {
			if route.Prefix != "" && route.Handler != nil && under(r.URL.Path, route.Prefix) {
				route.Handler.ServeHTTP(w, r)
				return
			}
		}
		if managed != nil && managed.ServeWithPlanner(w, r, planner) {
			return
		}
		if under(r.URL.Path, "/hooks") {
			if hooks == nil {
				http.NotFound(w, r)
				return
			}
			hooks.ServeHTTP(w, r)
			return
		}
		planner.ServeHTTP(w, r)
	})
}

func under(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
