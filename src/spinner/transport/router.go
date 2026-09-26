// Package transport exposes Spinner's HTTP delivery machinery to Router
// compositions without making them depend on the standalone Spinner command.
package transport

import (
	"fmt"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/internal/config"
	"github.com/webong/gateway/src/spinner/internal/forwarding"
	"github.com/webong/gateway/src/spinner/internal/logging"
	"github.com/webong/gateway/src/spinner/internal/workers"
)

type Router struct {
	Executor        *bridge.RelayExecutor
	InternalToken   string
	MaxBodySize     int64
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration

	forwarder  *forwarding.Forwarder
	workerPool *workers.WorkerPool
}

func NewRouterFromEnvironment() (*Router, error) {
	settings, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}
	if settings.Runtime != "http" {
		return nil, fmt.Errorf("the embedded Router requires GATEWAY_RUNTIME=http")
	}
	if settings.InternalToken == "" {
		return nil, fmt.Errorf("GATEWAY_INTERNAL_TOKEN is required")
	}
	logger := logging.NewLogger(settings.LogLevel)
	forwarder := forwarding.NewForwarder(settings, logger)
	workerPool := workers.NewWorkerPool(settings.MaxWorkers, settings.MaxQueueSize, forwarder, logger)
	return &Router{
		Executor:        bridge.NewRelayExecutor(forwarder, workerPool),
		InternalToken:   settings.InternalToken,
		MaxBodySize:     settings.MaxBodySize,
		RequestTimeout:  settings.RequestTimeout,
		ShutdownTimeout: settings.ShutdownTimeout,
		forwarder:       forwarder,
		workerPool:      workerPool,
	}, nil
}

func (r *Router) Close() {
	r.workerPool.Shutdown()
	r.forwarder.CloseIdleConnections()
}
