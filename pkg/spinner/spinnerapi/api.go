// Package spinnerapi is the public seam for host adapters that live outside
// the Spinner module, such as pkg/caddy. It re-exports the Spinner runtime
// types those adapters need without making internal packages public.
package spinnerapi

import (
	"fmt"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
	"github.com/webong/gateway/pkg/spinner/bridge/httpout"
	integrationextensions "github.com/webong/gateway/pkg/spinner/extensions/integration"
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

func NewLogger(level string) *Logger {
	return internallogging.NewLogger(level)
}

func NewForwarder(settings *Config, logger *Logger) *Forwarder {
	return internalforwarding.NewForwarder(settings, logger)
}

func NewWorkerPool(workers, queueSize int, forwarder *Forwarder, logger *Logger) *WorkerPool {
	return internalworkers.NewWorkerPool(workers, queueSize, forwarder, logger)
}

// NewHTTPDeliveryQueue constructs the protocol-neutral durable queue used by
// host adapters. Product Extensions provide any additional delivery adapters
// through their own runtime contracts; this seam never imports them.
func NewHTTPDeliveryQueue(settings *Config, forwarder *Forwarder, logger *Logger) (deliveryqueue.Backend, error) {
	if settings == nil || settings.DeliverySpoolPath == "" && settings.DeliveryDatabaseURL == "" {
		return nil, nil
	}
	if settings.DeliverySpoolPath != "" && settings.DeliveryDatabaseURL != "" {
		return nil, fmt.Errorf("local and shared delivery storage cannot both be enabled")
	}
	httpAdapter, err := httpout.NewAdapter(forwarder)
	if err != nil {
		return nil, err
	}
	deliveryTimeout := settings.RequestTimeout
	if deliveryTimeout <= 0 {
		deliveryTimeout = 30 * time.Second
	}
	config := deliveryqueue.Config{
		Path: settings.DeliverySpoolPath,
		Adapters: map[string]bridge.GatewayDeliveryAdapter{
			string(bridge.ProtocolHTTP): httpAdapter,
		},
		AdapterResolver:        integrationextensions.DefaultRegistry,
		FailureHandlerResolver: integrationextensions.DefaultRegistry,
		Observer: deliveryqueue.ObserverFunc(func(event deliveryqueue.Event) {
			if logger == nil {
				return
			}
			switch event.State {
			case deliveryqueue.StateFailed:
				logger.Error("Delivery %s failed after %d attempts: %s", event.ID, event.Attempts, event.Error)
			case deliveryqueue.StateDeferred:
				logger.Warn("Delivery %s deferred after attempt %d: %s", event.ID, event.Attempts, event.Error)
			default:
				logger.Debug("Delivery %s entered state %s", event.ID, event.State)
			}
		}),
		MaxAttempts: settings.DeliveryMaxAttempts, InitialBackoff: settings.DeliveryInitialBackoff,
		MaxBackoff: settings.DeliveryMaxBackoff, DeliveryTimeout: deliveryTimeout,
		PollInterval: settings.DeliveryPollInterval, ReceiptRetention: settings.DeliveryReceiptRetention,
	}
	if settings.DeliveryDatabaseURL != "" {
		config.Path = ""
		return deliveryqueue.NewShared(config, settings.DeliveryDatabaseURL)
	}
	return deliveryqueue.New(config)
}
