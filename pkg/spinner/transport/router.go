// Package transport exposes Spinner's HTTP delivery machinery to Router
// compositions without making them depend on the standalone Spinner command.
package transport

import (
	"context"
	"fmt"
	"net/http"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryadmin"
	"github.com/webong/gateway/pkg/spinner/bridge/deliverymembership"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
	"github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/forwarding"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
	"github.com/webong/gateway/pkg/spinner/internal/workers"
)

type Router struct {
	settings                *config.Config
	Executor                *bridge.RelayExecutor
	InternalToken           string
	MaxBodySize             int64
	RequestTimeout          time.Duration
	WebSocketMaxConnections int
	WebSocketMaxDuration    time.Duration
	WebstreamHeartbeat      time.Duration
	WebstreamRetry          time.Duration
	WebstreamMaxConnections int
	WebstreamMaxDuration    time.Duration
	SchedulesEnabled        bool
	SchedulePollInterval    time.Duration
	ProvisioningRouteProxy  bool
	ProvisioningRouteCIDRs  []string
	ShutdownTimeout         time.Duration
	DeliveryAdminServer     *http.Server

	forwarder     *forwarding.Forwarder
	workerPool    *workers.WorkerPool
	deliveryQueue deliveryqueue.Backend
}

func NewRouterFromEnvironment() (*Router, error) {
	settings, err := config.LoadRouterConfig()
	if err != nil {
		return nil, err
	}
	if settings.InternalToken == "" {
		return nil, fmt.Errorf("GATEWAY_INTERNAL_TOKEN is required")
	}
	logger := logging.NewLogger(settings.LogLevel)
	forwarder := forwarding.NewForwarder(settings, logger)
	workerPool := workers.NewWorkerPool(settings.MaxWorkers, settings.MaxQueueSize, forwarder, logger)
	deliveryQueue, err := NewDeliveryQueue(settings, forwarder, logger)
	if err != nil {
		workerPool.Shutdown()
		forwarder.CloseIdleConnections()
		return nil, err
	}
	var executorOptions []bridge.RelayExecutorOption
	if deliveryQueue != nil {
		executorOptions = append(executorOptions, bridge.WithGatewayDeliveryQueue(deliveryQueue))
	}
	var adminServer *http.Server
	if settings.DeliveryAdminAddress != "" {
		options := []deliveryadmin.HandlerOption{deliveryadmin.WithAutomationExecutor(automationextensions.Runtime())}
		if settings.SMTPRelayAddress != "" || settings.MailboxSMTPResolverURL != "" || settings.OutboundResolverURL != "" {
			var checker interface{ CheckSigningDomain(string) error }
			if settings.SMTPRelayAddress != "" {
				adapter, adapterErr := NewSMTPAdapter(settings)
				if adapterErr != nil {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					_ = deliveryQueue.Close(ctx)
					cancel()
					workerPool.Shutdown()
					forwarder.CloseIdleConnections()
					return nil, adapterErr
				}
				checker = adapter
			}
			scanner, _, protectionErr := NewEmailProtection(settings)
			if protectionErr != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = deliveryQueue.Close(ctx)
				cancel()
				workerPool.Shutdown()
				forwarder.CloseIdleConnections()
				return nil, protectionErr
			}
			options = append(options, deliveryadmin.WithEmailSubmission(deliveryQueue, checker, scanner, settings.SMTPMaxMessageSize, []byte(settings.InternalToken), settings.EmailDSNDomain != ""))
		}
		handler, err := deliveryadmin.NewHandler(deliveryQueue, settings.InternalToken, options...)
		if err != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = deliveryQueue.Close(ctx)
			cancel()
			workerPool.Shutdown()
			forwarder.CloseIdleConnections()
			return nil, err
		}
		adminServer = &http.Server{
			Addr: settings.DeliveryAdminAddress, Handler: handler,
			ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		}
	}
	return &Router{
		settings:                settings,
		Executor:                bridge.NewRelayExecutor(forwarder, workerPool, executorOptions...),
		InternalToken:           settings.InternalToken,
		MaxBodySize:             settings.MaxBodySize,
		RequestTimeout:          settings.RequestTimeout,
		WebSocketMaxConnections: settings.WebSocketMaxConnections,
		WebSocketMaxDuration:    settings.WebSocketMaxDuration,
		WebstreamHeartbeat:      settings.WebstreamHeartbeat,
		WebstreamRetry:          settings.WebstreamRetry,
		WebstreamMaxConnections: settings.WebstreamMaxConnections,
		WebstreamMaxDuration:    settings.WebstreamMaxDuration,
		SchedulesEnabled:        settings.SchedulesEnabled,
		SchedulePollInterval:    settings.SchedulePollInterval,
		ProvisioningRouteProxy:  settings.ProvisioningRouteProxy,
		ProvisioningRouteCIDRs:  settings.ProvisioningRouteCIDRs,
		ShutdownTimeout:         settings.ShutdownTimeout,
		DeliveryAdminServer:     adminServer,
		forwarder:               forwarder,
		workerPool:              workerPool,
		deliveryQueue:           deliveryQueue,
	}, nil
}

func (r *Router) Close() {
	if r.DeliveryAdminServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), r.ShutdownTimeout)
		_ = r.DeliveryAdminServer.Shutdown(ctx)
		cancel()
	}
	if r.deliveryQueue != nil {
		ctx, cancel := context.WithTimeout(context.Background(), r.ShutdownTimeout)
		defer cancel()
		_ = r.deliveryQueue.Close(ctx)
	}
	r.workerPool.Shutdown()
	r.forwarder.CloseIdleConnections()
}

// ServeDeliveryAdmin publishes this private listener only while it is bound.
func (r *Router) ServeDeliveryAdmin() error {
	if r.DeliveryAdminServer == nil {
		return nil
	}
	return deliverymembership.ListenAndServe(r.DeliveryAdminServer, r.settings.DeliveryDatabaseURL, r.settings.DeliveryAdminAdvertiseURL, r.InternalToken)
}
