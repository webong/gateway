package transport

import (
	"context"
	"fmt"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	dnsgateway "github.com/webong/gateway/src/spinner/cmd/bridge/dns"
	imapgateway "github.com/webong/gateway/src/spinner/cmd/bridge/imap"
	pop3gateway "github.com/webong/gateway/src/spinner/cmd/bridge/pop3"
	smtpgateway "github.com/webong/gateway/src/spinner/cmd/bridge/smtp"
	relayconfig "github.com/webong/gateway/src/spinner/internal/config"
)

// ProtocolListener is an optional application-protocol ingress listener.
type ProtocolListener interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

func (r *Router) HasProtocolListeners() bool {
	return r.settings.SMTPAddress != "" || r.settings.DNSAddress != "" || r.settings.POP3Address != "" || r.settings.IMAPAddress != ""
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
	if r.settings.POP3Address != "" {
		server, err := NewPOP3Server(r.settings, planner)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, server)
	}
	if r.settings.IMAPAddress != "" {
		server, err := NewIMAPServer(r.settings, planner)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, server)
	}
	return listeners, nil
}

func NewIMAPServer(config *relayconfig.Config, planner bridge.ProtocolPlanner) (*imapgateway.Server, error) {
	tlsConfig, err := imapgateway.LoadTLSConfig(config.IMAPTLSCertFile, config.IMAPTLSKeyFile)
	if err != nil {
		return nil, err
	}
	upstreams := make(map[string]imapgateway.Upstream, len(config.IMAPUpstreams))
	for id, upstream := range config.IMAPUpstreams {
		upstreams[id] = imapgateway.Upstream{Address: upstream.Address, ServerName: upstream.ServerName}
	}
	return imapgateway.NewServer(imapgateway.Config{
		Address: config.IMAPAddress, Hostname: config.IMAPHostname,
		TLSConfig: tlsConfig, Upstreams: upstreams,
		MaxConnections: config.IMAPMaxConnections, IdleTimeout: config.IMAPIdleTimeout,
		AuthPerMinute: config.IMAPAuthPerMinute, AuthBurst: config.IMAPAuthBurst,
		PlannerTimeout: config.IMAPPlannerTimeout, UpstreamTimeout: config.IMAPUpstreamTimeout,
	}, planner)
}

func NewPOP3Server(config *relayconfig.Config, planner bridge.ProtocolPlanner) (*pop3gateway.Server, error) {
	tlsConfig, err := pop3gateway.LoadTLSConfig(config.POP3TLSCertFile, config.POP3TLSKeyFile)
	if err != nil {
		return nil, err
	}
	upstreams := make(map[string]pop3gateway.Upstream, len(config.POP3Upstreams))
	for id, upstream := range config.POP3Upstreams {
		upstreams[id] = pop3gateway.Upstream{Address: upstream.Address, ServerName: upstream.ServerName}
	}
	return pop3gateway.NewServer(pop3gateway.Config{
		Address: config.POP3Address, Hostname: config.POP3Hostname,
		TLSConfig: tlsConfig, Upstreams: upstreams,
		MaxConnections: config.POP3MaxConnections, IdleTimeout: config.POP3IdleTimeout,
		AuthPerMinute: config.POP3AuthPerMinute, AuthBurst: config.POP3AuthBurst,
		PlannerTimeout: config.POP3PlannerTimeout, UpstreamTimeout: config.POP3UpstreamTimeout,
	}, planner)
}

func newSMTPServer(config *relayconfig.Config, planner bridge.ProtocolPlanner, executor bridge.GatewayExecutor) (*smtpgateway.Server, error) {
	tlsConfig, err := smtpgateway.LoadTLSConfig(config.SMTPTLSCertFile, config.SMTPTLSKeyFile)
	if err != nil {
		return nil, err
	}
	scanner, quarantine, err := NewEmailProtection(config)
	if err != nil {
		return nil, err
	}
	domainResolver, err := NewEmailDomainResolver(config)
	if err != nil {
		return nil, err
	}
	submitter, err := NewSMTPAdapter(config)
	if err != nil {
		return nil, err
	}
	var submitAdapter bridge.GatewayDeliveryAdapter
	var signingChecker smtpgateway.SigningDomainChecker
	if submitter != nil {
		submitAdapter = submitter
		signingChecker = submitter
	}

	return smtpgateway.NewServer(smtpgateway.Config{
		Address:                 config.SMTPAddress,
		Hostname:                config.SMTPHostname,
		MaxMessageSize:          config.SMTPMaxMessageSize,
		MaxRecipients:           config.SMTPMaxRecipients,
		MaxConnections:          config.SMTPMaxConnections,
		MessagesPerMinute:       config.SMTPMessagesPerMinute,
		MessageBurst:            config.SMTPMessageBurst,
		ReadTimeout:             config.SMTPReadTimeout,
		WriteTimeout:            config.SMTPWriteTimeout,
		PlannerTimeout:          config.SMTPPlannerTimeout,
		SubmitTimeout:           config.SMTPRelayTimeout,
		Submitter:               submitAdapter,
		SigningDomainChecker:    signingChecker,
		LoopKey:                 []byte(config.InternalToken),
		TLSConfig:               tlsConfig,
		ImplicitTLS:             config.SMTPImplicitTLS,
		AuthEnabled:             config.SMTPAuthEnabled,
		RecipientPreflight:      config.SMTPRecipientPreflight,
		Scanner:                 scanner,
		ScanTimeout:             config.EmailClamdTimeout,
		Quarantine:              quarantine,
		QuarantineRetention:     config.EmailQuarantineRetention,
		DomainExistenceResolver: domainResolver,
		DomainExistenceTimeout:  config.EmailDomainLookupTimeout,
	}, planner, executor)
}

func NewEmailDomainResolver(config *relayconfig.Config) (smtpgateway.DomainExistenceResolver, error) {
	if config == nil || config.EmailDomainResolverAddress == "" {
		return nil, nil
	}
	return smtpgateway.NewDomainExistenceResolver(config.EmailDomainResolverAddress)
}

func NewEmailProtection(config *relayconfig.Config) (smtpgateway.MalwareScanner, smtpgateway.QuarantineStore, error) {
	if config == nil {
		return nil, nil, fmt.Errorf("email protection configuration is required")
	}
	if config.EmailQuarantineRetention < 0 || (config.EmailQuarantineRetention > 0 && config.EmailQuarantinePath == "") {
		return nil, nil, fmt.Errorf("GATEWAY_EMAIL_QUARANTINE_RETENTION requires GATEWAY_EMAIL_QUARANTINE_PATH and cannot be negative")
	}
	var scanner smtpgateway.MalwareScanner
	if config.EmailClamdAddress != "" {
		network := config.EmailClamdNetwork
		if network == "" {
			network = "tcp"
		}
		timeout := config.EmailClamdTimeout
		if timeout == 0 {
			timeout = 10 * time.Second
		}
		var err error
		scanner, err = smtpgateway.NewClamdScanner(network, config.EmailClamdAddress, timeout)
		if err != nil {
			return nil, nil, err
		}
	}
	if config.EmailQuarantinePath == "" {
		return scanner, nil, nil
	}
	quarantine, err := smtpgateway.NewFileQuarantine(config.EmailQuarantinePath)
	if err != nil {
		return nil, nil, err
	}
	return scanner, quarantine, nil
}

func newDNSServer(config *relayconfig.Config, planner bridge.ProtocolPlanner, executor *bridge.RelayExecutor) (*dnsgateway.Server, error) {
	return dnsgateway.NewServer(dnsgateway.Config{
		Address:            config.DNSAddress,
		Zone:               config.DNSZone,
		Zones:              DNSZones(config),
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

// DNSZones converts the shared settings for either Router host.
func DNSZones(config *relayconfig.Config) []dnsgateway.Zone {
	zones := make([]dnsgateway.Zone, 0, len(config.DNSZones))
	for _, zone := range config.DNSZones {
		soaEmail := zone.SOAEmail
		if soaEmail == "" {
			soaEmail = config.DNSSOAEmail
		}
		zones = append(zones, dnsgateway.Zone{Name: zone.Name, Nameservers: zone.Nameservers, SOAEmail: soaEmail, TTL: zone.TTL})
	}
	return zones
}
