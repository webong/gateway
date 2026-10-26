package transport

import (
	"fmt"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
	"github.com/webong/gateway/pkg/spinner/bridge/httpout"
	integrationextensions "github.com/webong/gateway/pkg/spinner/extensions/integration"
	"github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/forwarding"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
)

// NewDeliveryQueue constructs Spinner's protocol-neutral durable delivery
// queue. Product delivery adapters are supplied by installable Extensions;
// the core host owns only HTTP callback delivery.
func NewDeliveryQueue(settings *config.Config, forwarder *forwarding.Forwarder, logger *logging.Logger) (deliveryqueue.Backend, error) {
	if settings.DeliverySpoolPath == "" && settings.DeliveryDatabaseURL == "" {
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
	queueConfig := deliveryqueue.Config{
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
		queueConfig.Path = ""
		return deliveryqueue.NewShared(queueConfig, settings.DeliveryDatabaseURL)
	}
	return deliveryqueue.New(queueConfig)
}
