package transport

import (
	"net/http"

	"github.com/webong/gateway/arranger/integration"
	emailspinner "github.com/webong/gateway/pkg/email/spinner"
)

// PublicRoutes returns the public routes contributed by the configured
// transport extensions. Router composes these without importing any concrete
// extension package.
func (router *Router) PublicRoutes(planner http.Handler) []integration.PublicRoute {
	return []integration.PublicRoute{{
		Prefix: emailspinner.WorkerIngressPrefix,
		Handler: emailspinner.NewWorkerIngressHandler(
			planner,
			router.InternalToken,
			router.deliveryQueue,
		),
	}}
}

// InternalRequestPolicies returns private Planner routes owned by configured
// transport extensions. Every policy remains fail-closed in its owning module.
func InternalRequestPolicies() []integration.InternalRequestPolicy {
	return []integration.InternalRequestPolicy{emailspinner.InternalPlannerRequest}
}
