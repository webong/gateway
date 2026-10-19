package transport

import (
	"fmt"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/cmd/bridge/deliveryqueue"
	"github.com/webong/gateway/src/spinner/cmd/bridge/httpout"
	"github.com/webong/gateway/src/spinner/cmd/bridge/smtpout"
	"github.com/webong/gateway/src/spinner/internal/config"
	"github.com/webong/gateway/src/spinner/internal/forwarding"
	"github.com/webong/gateway/src/spinner/internal/logging"
)

// NewDeliveryQueue wires either the local spool or the optional PostgreSQL
// shared queue to the transport adapters configured in this Spinner process.
// With neither backend configured, HTTP keeps its legacy in-memory delivery.
func NewDeliveryQueue(settings *config.Config, forwarder *forwarding.Forwarder, logger *logging.Logger) (deliveryqueue.Backend, error) {
	if settings.DeliverySpoolPath == "" && settings.DeliveryDatabaseURL == "" {
		if settings.SMTPRelayAddress != "" || settings.MailboxSMTPResolverURL != "" || settings.HostedMailResolverURL != "" {
			return nil, fmt.Errorf("durable delivery storage is required for outbound SMTP")
		}
		return nil, nil
	}
	if settings.DeliverySpoolPath != "" && settings.DeliveryDatabaseURL != "" {
		return nil, fmt.Errorf("local and shared delivery storage cannot both be enabled")
	}
	httpAdapter, err := httpout.NewAdapter(forwarder)
	if err != nil {
		return nil, err
	}
	adapters := map[string]bridge.GatewayDeliveryAdapter{string(bridge.ProtocolHTTP): httpAdapter}
	if settings.SMTPRelayAddress != "" {
		adapter, err := NewSMTPAdapter(settings)
		if err != nil {
			return nil, err
		}
		adapters[string(bridge.ProtocolSMTP)] = adapter
	}
	if settings.MailboxSMTPResolverURL != "" {
		resolver, err := smtpout.NewHTTPMailboxResolver(settings.MailboxSMTPResolverURL, settings.InternalToken)
		if err != nil {
			return nil, err
		}
		adapter, err := smtpout.NewManagedMailboxAdapter(resolver, settings.SMTPRelayTimeout)
		if err != nil {
			return nil, err
		}
		adapters["mailbox.smtp"] = adapter
	}
	providerAdapters, err := registeredDeliveryAdapters(settings)
	if err != nil {
		return nil, err
	}
	for name, adapter := range providerAdapters {
		adapters[name] = adapter
	}
	if settings.HostedMailResolverURL != "" {
		if adapters["hosted.mail"] != nil {
			return nil, fmt.Errorf("hosted.mail is reserved for Planner-managed hosted delivery")
		}
		resolver, err := newHTTPHostedMailResolver(settings.HostedMailResolverURL, settings.InternalToken)
		if err != nil {
			return nil, err
		}
		adapter, err := newManagedHostedMailAdapter(resolver, settings.HostedMailAllowedHosts, nil)
		if err != nil {
			return nil, err
		}
		adapters["hosted.mail"] = adapter
	}
	fanout := newFanoutAdapter()
	adapters["gateway.fanout"] = fanout
	deliveryTimeout := settings.RequestTimeout
	if (settings.SMTPRelayAddress != "" || settings.MailboxSMTPResolverURL != "") && settings.SMTPRelayTimeout > deliveryTimeout {
		deliveryTimeout = settings.SMTPRelayTimeout
	}
	if deliveryTimeout <= 0 {
		deliveryTimeout = 30 * time.Second
	}
	queueConfig := deliveryqueue.Config{
		Path: settings.DeliverySpoolPath, Adapters: adapters,
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
		FailureNoticeDomain: settings.EmailDSNDomain,
	}
	if settings.DeliveryDatabaseURL != "" {
		queueConfig.Path = ""
		queue, err := deliveryqueue.NewShared(queueConfig, settings.DeliveryDatabaseURL)
		if err != nil {
			return nil, err
		}
		fanout.bind(queue)
		return queue, nil
	}
	queue, err := deliveryqueue.New(queueConfig)
	if err != nil {
		return nil, err
	}
	fanout.bind(queue)
	return queue, nil
}

// NewSMTPAdapter constructs the configured outbound relay for durable queue
// attempts and for the synchronous SMTP submit handoff.
func NewSMTPAdapter(settings *config.Config) (*smtpout.GatewayAdapter, error) {
	if settings == nil || settings.SMTPRelayAddress == "" {
		return nil, nil
	}
	sender, err := smtpout.NewSender(smtpout.Config{
		Address: settings.SMTPRelayAddress, LocalName: settings.SMTPRelayLocalName,
		ServerName: settings.SMTPRelayServerName, Username: settings.SMTPRelayUsername,
		Password: settings.SMTPRelayPassword, TLSMode: smtpout.TLSMode(settings.SMTPRelayTLSMode),
		Timeout: settings.SMTPRelayTimeout,
	})
	if err != nil {
		return nil, err
	}
	var signer smtpout.DKIMSigning
	if settings.EmailDKIMResolverURL != "" {
		resolved, err := smtpout.NewHTTPDKIMSigner(settings.EmailDKIMResolverURL, settings.InternalToken)
		if err != nil {
			return nil, err
		}
		signer = resolved
	}
	return smtpout.NewGatewayAdapter(sender, signer)
}
