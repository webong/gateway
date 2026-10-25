package transport

import (
	"net/http"

	emailspinner "github.com/webong/gateway/pkg/email/spinner"
)

// WorkerIngressHandler composes the Email Extension's public worker ingress
// with Spinner's durable queue and private Planner credential.
func (router *Router) WorkerIngressHandler(planner http.Handler) http.Handler {
	return emailspinner.NewWorkerIngressHandler(planner, router.InternalToken, router.deliveryQueue)
}
