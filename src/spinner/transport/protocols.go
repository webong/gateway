package transport

import (
	"context"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	dnsgateway "github.com/webong/gateway/src/spinner/cmd/bridge/dns"
	smtpgateway "github.com/webong/gateway/src/spinner/cmd/bridge/smtp"
	relayconfig "github.com/webong/gateway/src/spinner/internal/config"
)

// ProtocolListener is an optional SMTP or DNS ingress listener.
type ProtocolListener interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

func (r *Router) HasProtocolListeners() bool {
	return r.settings.SMTPAddress != "" || r.settings.DNSAddress != ""
}

func (r *Router) ProtocolListeners(planner bridge.ProtocolPlanner) ([]ProtocolListener, error) {
	var listeners []ProtocolListener
	if r.settings.SMTPAddress != "" {
		server, err := newSMTPServer(r.settings, planner, r.Executor)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, server)
	}
	if r.settings.DNSAddress != "" {
		server, err := newDNSServer(r.settings, planner, r.Executor)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, server)
	}
	return listeners, nil
}

func newSMTPServer(config *relayconfig.Config, planner bridge.ProtocolPlanner, executor bridge.GatewayExecutor) (*smtpgateway.Server, error) {
	tlsConfig, err := smtpgateway.LoadTLSConfig(config.SMTPTLSCertFile, config.SMTPTLSKeyFile)
	if err != nil {
		return nil, err
	}

	return smtpgateway.NewServer(smtpgateway.Config{
		Address:        config.SMTPAddress,
		Hostname:       config.SMTPHostname,
		MaxMessageSize: config.SMTPMaxMessageSize,
		MaxRecipients:  config.SMTPMaxRecipients,
		ReadTimeout:    config.SMTPReadTimeout,
		WriteTimeout:   config.SMTPWriteTimeout,
		PlannerTimeout: config.SMTPPlannerTimeout,
		TLSConfig:      tlsConfig,
		ImplicitTLS:    config.SMTPImplicitTLS,
		AuthEnabled:    config.SMTPAuthEnabled,
	}, planner, executor)
}

func newDNSServer(config *relayconfig.Config, planner bridge.ProtocolPlanner, executor *bridge.RelayExecutor) (*dnsgateway.Server, error) {
	return dnsgateway.NewServer(dnsgateway.Config{
		Address:            config.DNSAddress,
		Zone:               config.DNSZone,
		Nameservers:        config.DNSNameservers,
		SOAEmail:           config.DNSSOAEmail,
		TTL:                config.DNSTTL,
		PlannerTimeout:     config.DNSPlannerTimeout,
		ReadTimeout:        config.DNSReadTimeout,
		WriteTimeout:       config.DNSWriteTimeout,
		MaxResponseRecords: config.DNSMaxResponseRecords,
		RateLimit:          config.DNSRateLimit,
		RateBurst:          config.DNSRateBurst,
	}, planner, executor)

}
