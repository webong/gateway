// Package spinnerapi is the public seam for host adapters that live outside
// the Spinner module, such as pkg/caddy. It re-exports the Spinner runtime
// types those adapters need without making internal packages public.
package spinnerapi

import (
	"net/netip"

	internalconfig "github.com/webong/gateway/pkg/spinner/internal/config"
	internalforwarding "github.com/webong/gateway/pkg/spinner/internal/forwarding"
	internallogging "github.com/webong/gateway/pkg/spinner/internal/logging"
	internalworkers "github.com/webong/gateway/pkg/spinner/internal/workers"
)

type (
	Config     = internalconfig.Config
	Forwarder  = internalforwarding.Forwarder
	Logger     = internallogging.Logger
	WorkerPool = internalworkers.WorkerPool
)

func ParseHTTPPrivateNetworks(raw string) ([]netip.Prefix, error) {
	return internalconfig.ParseHTTPPrivateNetworks(raw)
}

func ParseWebstreamPrivateNetworks(raw string) ([]netip.Prefix, error) {
	return internalconfig.ParseWebstreamPrivateNetworks(raw)
}

func NewLogger(level string) *Logger {
	return internallogging.NewLogger(level)
}

func NewForwarder(settings *Config, logger *Logger) *Forwarder {
	return internalforwarding.NewForwarder(settings, logger)
}

func NewWorkerPool(workers, queueSize int, forwarder *Forwarder, logger *Logger) *WorkerPool {
	return internalworkers.NewWorkerPool(workers, queueSize, forwarder, logger)
}
