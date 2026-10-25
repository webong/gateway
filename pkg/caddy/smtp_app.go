package caddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	smtpgateway "github.com/webong/gateway/pkg/email/spinner/smtp"
	smtpout "github.com/webong/gateway/pkg/email/spinner/smtpout"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
	"github.com/webong/gateway/pkg/spinner/spinnerapi"
	"github.com/webong/gateway/pkg/spinner/transport"
	"go.uber.org/zap"
)

const (
	defaultSMTPListen            = ":2525"
	defaultSMTPPlannerURL        = "http://127.0.0.1:8081"
	defaultSMTPHostname          = "gateway.local"
	defaultSMTPMaxMessageSize    = 10 * 1024 * 1024
	defaultSMTPMaxLineSize       = 16 * 1024
	defaultSMTPMaxRecipients     = 100
	defaultSMTPMaxConnections    = 1000
	defaultSMTPMessagesPerMinute = 120
	defaultSMTPMessageBurst      = 20
	defaultSMTPReadTimeout       = 5 * time.Minute
	defaultSMTPWriteTimeout      = 30 * time.Second
	defaultSMTPPlannerTimeout    = 30 * time.Second
	defaultSMTPMaxWorkers        = 100
	defaultSMTPMaxQueueSize      = 1000
	defaultSMTPMaxIdleConns      = 100
	defaultSMTPMaxConnsPerHost   = 100
	defaultSMTPIdleConnTimeout   = 90 * time.Second
	defaultSMTPRequestTimeout    = 30 * time.Second
	defaultSMTPRelayTimeout      = 30 * time.Second
	defaultSMTPLogLevel          = "info"
)

func init() {
	caddy.RegisterModule((*SMTPApp)(nil))
	httpcaddyfile.RegisterGlobalOption("gateway_smtp", parseSMTPApp)
}

// SMTPApp is a Caddy app module which starts the same Go SMTP adapter as
// pkg/spinner/proxy. It is a sibling TCP service, not an HTTP handler in php_server.
type SMTPApp struct {
	Listen                 string          `json:"listen,omitempty"`
	PlannerURL             string          `json:"planner_url,omitempty"`
	InternalToken          string          `json:"internal_token,omitempty"`
	Hostname               string          `json:"hostname,omitempty"`
	MaxMessageSize         int64           `json:"max_message_size,omitempty"`
	MaxLineSize            int             `json:"max_line_size,omitempty"`
	MaxRecipients          int             `json:"max_recipients,omitempty"`
	MaxConnections         int             `json:"max_connections,omitempty"`
	MessagesPerMinute      int             `json:"messages_per_minute,omitempty"`
	MessageBurst           int             `json:"message_burst,omitempty"`
	ReadTimeout            caddy.Duration  `json:"read_timeout,omitempty"`
	WriteTimeout           caddy.Duration  `json:"write_timeout,omitempty"`
	PlannerTimeout         caddy.Duration  `json:"planner_timeout,omitempty"`
	TLSCertFile            string          `json:"tls_cert_file,omitempty"`
	TLSKeyFile             string          `json:"tls_key_file,omitempty"`
	ImplicitTLS            bool            `json:"implicit_tls,omitempty"`
	AuthEnabled            bool            `json:"auth_enabled,omitempty"`
	RecipientPreflight     *bool           `json:"recipient_preflight,omitempty"`
	RelayAddress           string          `json:"relay_address,omitempty"`
	RelayLocalName         string          `json:"relay_local_name,omitempty"`
	RelayServerName        string          `json:"relay_server_name,omitempty"`
	RelayUsername          string          `json:"relay_username,omitempty"`
	RelayPassword          string          `json:"relay_password,omitempty"`
	RelayTLSMode           string          `json:"relay_tls_mode,omitempty"`
	RelayTimeout           caddy.Duration  `json:"relay_timeout,omitempty"`
	MailboxResolverURL     string          `json:"mailbox_resolver_url,omitempty"`
	OutboundResolverURL    string          `json:"outbound_resolver_url,omitempty"`
	HostedMailResolverURL  string          `json:"hosted_mail_resolver_url,omitempty"`
	HostedMailAllowedHosts []string        `json:"hosted_mail_allowed_hosts,omitempty"`
	DKIMResolverURL        string          `json:"dkim_resolver_url,omitempty"`
	ClamdNetwork           string          `json:"clamd_network,omitempty"`
	ClamdAddress           string          `json:"clamd_address,omitempty"`
	ClamdTimeout           caddy.Duration  `json:"clamd_timeout,omitempty"`
	DomainResolverAddress  string          `json:"domain_resolver_address,omitempty"`
	DomainLookupTimeout    caddy.Duration  `json:"domain_lookup_timeout,omitempty"`
	QuarantinePath         string          `json:"quarantine_path,omitempty"`
	QuarantineRetention    caddy.Duration  `json:"quarantine_retention,omitempty"`
	DSNDomain              string          `json:"dsn_domain,omitempty"`
	SpoolPath              string          `json:"spool_path,omitempty"`
	DeliveryDatabaseURL    string          `json:"delivery_database_url,omitempty"`
	MaxAttempts            int             `json:"max_attempts,omitempty"`
	InitialBackoff         caddy.Duration  `json:"initial_backoff,omitempty"`
	MaxBackoff             caddy.Duration  `json:"max_backoff,omitempty"`
	PollInterval           caddy.Duration  `json:"poll_interval,omitempty"`
	ReceiptRetention       *caddy.Duration `json:"receipt_retention,omitempty"`
	MaxWorkers             int             `json:"max_workers,omitempty"`
	MaxQueueSize           int             `json:"max_queue_size,omitempty"`
	RequestTimeout         caddy.Duration  `json:"request_timeout,omitempty"`
	MaxBodySize            int64           `json:"max_body_size,omitempty"`
	MaxIdleConns           int             `json:"max_idle_conns,omitempty"`
	MaxConnsPerHost        int             `json:"max_conns_per_host,omitempty"`
	IdleConnTimeout        caddy.Duration  `json:"idle_conn_timeout,omitempty"`
	LogLevel               string          `json:"log_level,omitempty"`

	server            *smtpgateway.Server
	forwarder         *spinnerapi.Forwarder
	workerPool        *spinnerapi.WorkerPool
	deliveryQueue     deliveryqueue.Backend
	submissionChecker smtpgateway.SigningDomainChecker
	submissionScanner smtpgateway.MalwareScanner
}

func (*SMTPApp) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "gateway.smtp",
		New: func() caddy.Module { return new(SMTPApp) },
	}
}

func (a *SMTPApp) Provision(_ caddy.Context) error {
	a.applyDefaults()
	if err := a.Validate(); err != nil {
		return err
	}
	httpPrivateNetworks, err := spinnerapi.ParseHTTPPrivateNetworks(os.Getenv("GATEWAY_HTTP_PRIVATE_NETWORK_CIDRS"))
	if err != nil {
		return err
	}

	planner, err := bridge.NewHTTPProtocolPlanner(
		a.PlannerURL,
		&http.Client{Timeout: time.Duration(a.RequestTimeout)},
		a.MaxBodySize,
		a.InternalToken,
	)
	if err != nil {
		return err
	}

	settings := &spinnerapi.Config{
		MaxBodySize: a.MaxBodySize, MaxWorkers: a.MaxWorkers, MaxQueueSize: a.MaxQueueSize,
		RequestTimeout: time.Duration(a.RequestTimeout), MaxIdleConns: a.MaxIdleConns,
		MaxConnsPerHost: a.MaxConnsPerHost, IdleConnTimeout: time.Duration(a.IdleConnTimeout),
		DeliverySpoolPath: a.SpoolPath, DeliveryDatabaseURL: a.DeliveryDatabaseURL,
		DeliveryMaxAttempts:    a.MaxAttempts,
		DeliveryInitialBackoff: time.Duration(a.InitialBackoff), DeliveryMaxBackoff: time.Duration(a.MaxBackoff),
		DeliveryPollInterval: time.Duration(a.PollInterval), DeliveryReceiptRetention: time.Duration(*a.ReceiptRetention),
		SMTPRelayAddress: a.RelayAddress, SMTPRelayLocalName: a.RelayLocalName,
		SMTPRelayServerName: a.RelayServerName, SMTPRelayUsername: a.RelayUsername,
		SMTPRelayPassword: a.RelayPassword, SMTPRelayTLSMode: a.RelayTLSMode,
		SMTPRelayTimeout:       time.Duration(a.RelayTimeout),
		MailboxSMTPResolverURL: a.MailboxResolverURL,
		OutboundResolverURL:    a.OutboundResolverURL,
		HostedMailResolverURL:  a.HostedMailResolverURL,
		HostedMailAllowedHosts: a.HostedMailAllowedHosts,
		InternalToken:          a.InternalToken,
		HTTPPrivateNetworks:    httpPrivateNetworks,
		EmailDKIMResolverURL:   a.DKIMResolverURL,
		EmailClamdNetwork:      a.ClamdNetwork, EmailClamdAddress: a.ClamdAddress,
		EmailClamdTimeout: time.Duration(a.ClamdTimeout), EmailQuarantinePath: a.QuarantinePath,
		EmailDomainResolverAddress: a.DomainResolverAddress,
		EmailDomainLookupTimeout:   time.Duration(a.DomainLookupTimeout),
		EmailQuarantineRetention:   time.Duration(a.QuarantineRetention),
		EmailDSNDomain:             a.DSNDomain,
	}
	domainResolver, err := transport.NewEmailDomainResolver(settings)
	if err != nil {
		return err
	}
	logger := spinnerapi.NewLogger(a.LogLevel)
	a.forwarder = spinnerapi.NewForwarder(settings, logger)
	a.workerPool = spinnerapi.NewWorkerPool(a.MaxWorkers, a.MaxQueueSize, a.forwarder, logger)
	tlsConfig, err := smtpgateway.LoadTLSConfig(a.TLSCertFile, a.TLSKeyFile)
	if err != nil {
		a.workerPool.Shutdown()
		a.forwarder.CloseIdleConnections()
		return err
	}
	executorOptions := make([]bridge.RelayExecutorOption, 0, 1)
	a.deliveryQueue, err = transport.NewDeliveryQueue(settings, a.forwarder, logger)
	if err != nil {
		a.workerPool.Shutdown()
		a.forwarder.CloseIdleConnections()
		return err
	}
	if a.deliveryQueue != nil {
		executorOptions = append(executorOptions, bridge.WithGatewayDeliveryQueue(a.deliveryQueue))
	}
	submitter, err := transport.NewSMTPAdapter(settings)
	if err != nil {
		if a.deliveryQueue != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = a.deliveryQueue.Close(ctx)
			cancel()
		}
		a.workerPool.Shutdown()
		a.forwarder.CloseIdleConnections()
		return err
	}
	var submitAdapter bridge.GatewayDeliveryAdapter
	var signingChecker smtpgateway.SigningDomainChecker
	if submitter != nil {
		submitAdapter = submitter
		signingChecker = submitter
	}
	a.submissionChecker = signingChecker
	scanner, quarantine, err := transport.NewEmailProtection(settings)
	if err != nil {
		if a.deliveryQueue != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = a.deliveryQueue.Close(ctx)
			cancel()
		}
		a.workerPool.Shutdown()
		a.forwarder.CloseIdleConnections()
		return err
	}
	a.submissionScanner = scanner
	a.server, err = smtpgateway.NewServer(smtpgateway.Config{
		Address:                 a.Listen,
		Hostname:                a.Hostname,
		MaxMessageSize:          a.MaxMessageSize,
		MaxLineSize:             a.MaxLineSize,
		MaxRecipients:           a.MaxRecipients,
		MaxConnections:          a.MaxConnections,
		MessagesPerMinute:       a.MessagesPerMinute,
		MessageBurst:            a.MessageBurst,
		ReadTimeout:             time.Duration(a.ReadTimeout),
		WriteTimeout:            time.Duration(a.WriteTimeout),
		PlannerTimeout:          time.Duration(a.PlannerTimeout),
		SubmitTimeout:           time.Duration(a.RelayTimeout),
		Submitter:               submitAdapter,
		SigningDomainChecker:    signingChecker,
		Scanner:                 scanner,
		ScanTimeout:             time.Duration(a.ClamdTimeout),
		Quarantine:              quarantine,
		QuarantineRetention:     time.Duration(a.QuarantineRetention),
		DomainExistenceResolver: domainResolver,
		DomainExistenceTimeout:  time.Duration(a.DomainLookupTimeout),
		LoopKey:                 []byte(a.InternalToken),
		TLSConfig:               tlsConfig,
		ImplicitTLS:             a.ImplicitTLS,
		AuthEnabled:             a.AuthEnabled,
		RecipientPreflight:      a.RecipientPreflight != nil && *a.RecipientPreflight,
	}, planner, bridge.NewRelayExecutor(a.forwarder, a.workerPool, executorOptions...))
	if err != nil {
		if a.deliveryQueue != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = a.deliveryQueue.Close(ctx)
			cancel()
		}
		a.workerPool.Shutdown()
		a.forwarder.CloseIdleConnections()
		return err
	}

	return nil
}

func (a *SMTPApp) Validate() error {
	if strings.TrimSpace(a.Listen) == "" {
		return fmt.Errorf("gateway.smtp listen is required")
	}
	if strings.TrimSpace(a.PlannerURL) == "" {
		return fmt.Errorf("gateway.smtp planner_url is required")
	}
	if strings.TrimSpace(a.InternalToken) == "" {
		return fmt.Errorf("gateway.smtp internal_token is required")
	}
	if a.MaxMessageSize < 1 || a.MaxLineSize < 1 || a.MaxRecipients < 1 || a.MaxConnections < 1 || a.MessagesPerMinute < 1 || a.MessageBurst < 1 {
		return fmt.Errorf("gateway.smtp message, recipient, and connection limits must be positive")
	}
	if a.MaxWorkers < 1 || a.MaxQueueSize < 1 || a.MaxIdleConns < 1 || a.MaxConnsPerHost < 1 {
		return fmt.Errorf("gateway.smtp worker and connection limits must be positive")
	}
	if a.ReadTimeout < 1 || a.WriteTimeout < 1 || a.PlannerTimeout < 1 || a.RequestTimeout < 1 || a.IdleConnTimeout < 1 {
		return fmt.Errorf("gateway.smtp timeouts must be positive")
	}
	if (a.RelayUsername == "") != (a.RelayPassword == "") {
		return fmt.Errorf("gateway.smtp relay_username and relay_password must be configured together")
	}
	if a.RelayTLSMode != "none" && a.RelayTLSMode != "starttls" && a.RelayTLSMode != "implicit" {
		return fmt.Errorf("gateway.smtp relay_tls_mode must be none, starttls, or implicit")
	}
	if a.RelayUsername != "" && a.RelayTLSMode == "none" {
		return fmt.Errorf("gateway.smtp relay authentication requires TLS")
	}
	if a.RelayTimeout < 1 {
		return fmt.Errorf("gateway.smtp relay_timeout must be positive")
	}
	if a.ClamdTimeout < 1 {
		return fmt.Errorf("gateway.smtp clamd_timeout must be positive")
	}
	if a.DomainResolverAddress != "" && a.DomainLookupTimeout < 1 {
		return fmt.Errorf("gateway.smtp domain_lookup_timeout must be positive")
	}
	if a.DomainResolverAddress != "" {
		if _, err := smtpgateway.NewDomainExistenceResolver(a.DomainResolverAddress); err != nil {
			return err
		}
	}
	if a.QuarantineRetention < 0 || (a.QuarantineRetention > 0 && a.QuarantinePath == "") {
		return fmt.Errorf("gateway.smtp quarantine_retention requires quarantine_path and cannot be negative")
	}
	if a.DSNDomain != "" && a.RelayAddress == "" {
		return fmt.Errorf("gateway.smtp dsn_domain requires relay_address")
	}
	if a.DKIMResolverURL != "" && (a.RelayAddress == "" && a.OutboundResolverURL == "" || a.InternalToken == "") {
		return fmt.Errorf("gateway.smtp dkim_resolver_url requires relay_address and internal_token")
	}
	if strings.TrimSpace(a.SpoolPath) != "" && strings.TrimSpace(a.DeliveryDatabaseURL) != "" {
		return fmt.Errorf("gateway.smtp spool_path and delivery_database_url are mutually exclusive")
	}
	if a.RelayAddress != "" && strings.TrimSpace(a.SpoolPath) == "" && strings.TrimSpace(a.DeliveryDatabaseURL) == "" {
		return fmt.Errorf("gateway.smtp spool_path or delivery_database_url is required when relay_address is configured")
	}
	if a.MailboxResolverURL != "" && (a.InternalToken == "" || (a.SpoolPath == "" && a.DeliveryDatabaseURL == "")) {
		return fmt.Errorf("gateway.smtp mailbox delivery requires internal_token and durable storage")
	}
	if a.OutboundResolverURL != "" && (a.InternalToken == "" || (a.SpoolPath == "" && a.DeliveryDatabaseURL == "")) {
		return fmt.Errorf("gateway.smtp outbound delivery requires internal_token and durable storage")
	}
	if a.HostedMailResolverURL != "" && (a.InternalToken == "" || len(a.HostedMailAllowedHosts) == 0) {
		return fmt.Errorf("gateway.smtp hosted mail requires internal_token and deployed allowed hosts")
	}
	if a.HostedMailResolverURL != "" && a.SpoolPath == "" && a.DeliveryDatabaseURL == "" {
		return fmt.Errorf("gateway.smtp hosted mail adapters require durable storage")
	}
	if a.MaxAttempts < 1 || a.InitialBackoff < 1 || a.MaxBackoff < a.InitialBackoff || a.PollInterval < 1 || (a.ReceiptRetention != nil && *a.ReceiptRetention < 0) {
		return fmt.Errorf("gateway.smtp spool attempts and durations are invalid")
	}

	return nil
}

func (a *SMTPApp) Start() error {
	address, err := caddy.ParseNetworkAddress(a.Listen)
	if err != nil {
		return fmt.Errorf("parse gateway.smtp listen address: %w", err)
	}
	listenerValue, err := address.Listen(context.Background(), 0, net.ListenConfig{})
	if err != nil {
		return fmt.Errorf("listen for gateway.smtp: %w", err)
	}
	listener, ok := listenerValue.(net.Listener)
	if !ok {
		if closer, ok := listenerValue.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return fmt.Errorf("gateway.smtp listen address does not provide a stream listener")
	}

	go func() {
		if err := a.server.Serve(listener); err != nil && !errors.Is(err, smtpgateway.ErrServerClosed) {
			caddy.Log().Error("gateway.smtp listener stopped", zap.Error(err))
		}
	}()

	return nil
}

func (a *SMTPApp) Stop() error {
	var shutdownErr error
	if a.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		shutdownErr = a.server.Shutdown(ctx)
		cancel()
	}
	if a.deliveryQueue != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := a.deliveryQueue.Close(ctx); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
		cancel()
	}
	if a.workerPool != nil {
		a.workerPool.Shutdown()
	}
	if a.forwarder != nil {
		a.forwarder.CloseIdleConnections()
	}

	return shutdownErr
}

func (a *SMTPApp) applyDefaults() {
	if a.RecipientPreflight == nil {
		enabled := a.RelayAddress != "" || a.HostedMailResolverURL != ""
		a.RecipientPreflight = &enabled
	}
	if a.Listen == "" {
		a.Listen = defaultSMTPListen
	}
	if a.PlannerURL == "" {
		a.PlannerURL = defaultSMTPPlannerURL
	}
	if a.Hostname == "" {
		a.Hostname = defaultSMTPHostname
	}
	if a.MaxMessageSize == 0 {
		a.MaxMessageSize = defaultSMTPMaxMessageSize
	}
	if a.MaxLineSize == 0 {
		a.MaxLineSize = defaultSMTPMaxLineSize
	}
	if a.MaxRecipients == 0 {
		a.MaxRecipients = defaultSMTPMaxRecipients
	}
	if a.MaxConnections == 0 {
		a.MaxConnections = defaultSMTPMaxConnections
	}
	if a.MessagesPerMinute == 0 {
		a.MessagesPerMinute = defaultSMTPMessagesPerMinute
	}
	if a.MessageBurst == 0 {
		a.MessageBurst = defaultSMTPMessageBurst
	}
	if a.ReadTimeout == 0 {
		a.ReadTimeout = caddy.Duration(defaultSMTPReadTimeout)
	}
	if a.WriteTimeout == 0 {
		a.WriteTimeout = caddy.Duration(defaultSMTPWriteTimeout)
	}
	if a.PlannerTimeout == 0 {
		a.PlannerTimeout = caddy.Duration(defaultSMTPPlannerTimeout)
	}
	if a.MaxWorkers == 0 {
		a.MaxWorkers = defaultSMTPMaxWorkers
	}
	if a.MaxQueueSize == 0 {
		a.MaxQueueSize = defaultSMTPMaxQueueSize
	}
	if a.RequestTimeout == 0 {
		a.RequestTimeout = caddy.Duration(defaultSMTPRequestTimeout)
	}
	if a.MaxBodySize == 0 {
		a.MaxBodySize = defaultCaddyMaxBodySize
	}
	if a.MaxIdleConns == 0 {
		a.MaxIdleConns = defaultSMTPMaxIdleConns
	}
	if a.MaxConnsPerHost == 0 {
		a.MaxConnsPerHost = defaultSMTPMaxConnsPerHost
	}
	if a.IdleConnTimeout == 0 {
		a.IdleConnTimeout = caddy.Duration(defaultSMTPIdleConnTimeout)
	}
	if a.LogLevel == "" {
		a.LogLevel = defaultSMTPLogLevel
	}
	if a.RelayLocalName == "" {
		a.RelayLocalName = defaultSMTPHostname
	}
	if a.RelayTLSMode == "" {
		a.RelayTLSMode = string(smtpout.TLSStartTLS)
	}
	if a.RelayTimeout == 0 {
		a.RelayTimeout = caddy.Duration(defaultSMTPRelayTimeout)
	}
	if a.ClamdNetwork == "" {
		a.ClamdNetwork = "tcp"
	}
	if a.ClamdTimeout == 0 {
		a.ClamdTimeout = caddy.Duration(10 * time.Second)
	}
	if a.DomainLookupTimeout == 0 {
		a.DomainLookupTimeout = caddy.Duration(3 * time.Second)
	}
	if a.MaxAttempts == 0 {
		a.MaxAttempts = 8
	}
	if a.InitialBackoff == 0 {
		a.InitialBackoff = caddy.Duration(5 * time.Second)
	}
	if a.MaxBackoff == 0 {
		a.MaxBackoff = caddy.Duration(time.Hour)
	}
	if a.PollInterval == 0 {
		a.PollInterval = caddy.Duration(time.Second)
	}
	if a.ReceiptRetention == nil {
		value := caddy.Duration(90 * 24 * time.Hour)
		a.ReceiptRetention = &value
	}
}

func parseSMTPApp(d *caddyfile.Dispenser, _ any) (any, error) {
	var app SMTPApp
	d.Next()
	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		case "listen":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.Listen = value
		case "planner_url":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.PlannerURL = value
		case "internal_token":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.InternalToken = value
		case "hostname":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.Hostname = value
		case "tls_cert_file":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.TLSCertFile = value
		case "tls_key_file":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.TLSKeyFile = value
		case "relay_address", "relay_local_name", "relay_server_name", "relay_username", "relay_password", "relay_tls_mode", "mailbox_resolver_url", "outbound_resolver_url", "hosted_mail_resolver_url", "hosted_mail_allowed_hosts", "dkim_resolver_url", "spool_path", "delivery_database_url", "clamd_network", "clamd_address", "domain_resolver_address", "quarantine_path", "dsn_domain":
			option := d.Val()
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			switch option {
			case "relay_address":
				app.RelayAddress = value
			case "relay_local_name":
				app.RelayLocalName = value
			case "relay_server_name":
				app.RelayServerName = value
			case "relay_username":
				app.RelayUsername = value
			case "relay_password":
				app.RelayPassword = value
			case "relay_tls_mode":
				app.RelayTLSMode = strings.ToLower(value)
			case "mailbox_resolver_url":
				app.MailboxResolverURL = value
			case "outbound_resolver_url":
				app.OutboundResolverURL = value
			case "hosted_mail_resolver_url":
				app.HostedMailResolverURL = value
			case "hosted_mail_allowed_hosts":
				app.HostedMailAllowedHosts = strings.Split(value, ",")
			case "dkim_resolver_url":
				app.DKIMResolverURL = value
			case "spool_path":
				app.SpoolPath = value
			case "delivery_database_url":
				app.DeliveryDatabaseURL = value
			case "clamd_network":
				app.ClamdNetwork = value
			case "clamd_address":
				app.ClamdAddress = value
			case "domain_resolver_address":
				app.DomainResolverAddress = value
			case "quarantine_path":
				app.QuarantinePath = value
			case "dsn_domain":
				app.DSNDomain = value
			}
		case "implicit_tls", "auth_enabled", "recipient_preflight":
			option := d.Val()
			value, err := boolArg(d)
			if err != nil {
				return nil, err
			}
			switch option {
			case "implicit_tls":
				app.ImplicitTLS = value
			case "auth_enabled":
				app.AuthEnabled = value
			case "recipient_preflight":
				app.RecipientPreflight = &value
			}
		case "max_message_size":
			value, err := nextInt64(d)
			if err != nil {
				return nil, err
			}
			app.MaxMessageSize = value
		case "max_line_size", "max_recipients", "max_connections", "messages_per_minute", "message_burst", "max_workers", "max_queue_size", "max_idle_conns", "max_conns_per_host", "max_attempts":
			option := d.Val()
			value, err := nextInt(d)
			if err != nil {
				return nil, err
			}
			switch option {
			case "max_line_size":
				app.MaxLineSize = value
			case "max_recipients":
				app.MaxRecipients = value
			case "max_connections":
				app.MaxConnections = value
			case "messages_per_minute":
				app.MessagesPerMinute = value
			case "message_burst":
				app.MessageBurst = value
			case "max_workers":
				app.MaxWorkers = value
			case "max_queue_size":
				app.MaxQueueSize = value
			case "max_idle_conns":
				app.MaxIdleConns = value
			case "max_conns_per_host":
				app.MaxConnsPerHost = value
			case "max_attempts":
				app.MaxAttempts = value
			}
		case "read_timeout", "write_timeout", "planner_timeout", "request_timeout", "idle_conn_timeout", "relay_timeout", "clamd_timeout", "domain_lookup_timeout", "quarantine_retention", "initial_backoff", "max_backoff", "poll_interval", "receipt_retention":
			option := d.Val()
			value, err := nextDuration(d)
			if err != nil {
				return nil, err
			}
			switch option {
			case "read_timeout":
				app.ReadTimeout = caddy.Duration(value)
			case "write_timeout":
				app.WriteTimeout = caddy.Duration(value)
			case "planner_timeout":
				app.PlannerTimeout = caddy.Duration(value)
			case "request_timeout":
				app.RequestTimeout = caddy.Duration(value)
			case "idle_conn_timeout":
				app.IdleConnTimeout = caddy.Duration(value)
			case "relay_timeout":
				app.RelayTimeout = caddy.Duration(value)
			case "clamd_timeout":
				app.ClamdTimeout = caddy.Duration(value)
			case "domain_lookup_timeout":
				app.DomainLookupTimeout = caddy.Duration(value)
			case "quarantine_retention":
				app.QuarantineRetention = caddy.Duration(value)
			case "initial_backoff":
				app.InitialBackoff = caddy.Duration(value)
			case "max_backoff":
				app.MaxBackoff = caddy.Duration(value)
			case "poll_interval":
				app.PollInterval = caddy.Duration(value)
			case "receipt_retention":
				converted := caddy.Duration(value)
				app.ReceiptRetention = &converted
			}
		case "max_body_size":
			value, err := nextInt64(d)
			if err != nil {
				return nil, err
			}
			app.MaxBodySize = value
		case "log_level":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.LogLevel = value
		default:
			return nil, d.Errf("unrecognized gateway_smtp option %q", d.Val())
		}
	}

	data, err := json.Marshal(app)
	if err != nil {
		return nil, err
	}

	return httpcaddyfile.App{Name: "gateway.smtp", Value: data}, nil
}

func stringArg(d *caddyfile.Dispenser) (string, error) {
	if !d.NextArg() {
		return "", d.ArgErr()
	}
	value := d.Val()
	if d.NextArg() {
		return "", d.ArgErr()
	}
	return value, nil
}

func boolArg(d *caddyfile.Dispenser) (bool, error) {
	if !d.NextArg() {
		return false, d.ArgErr()
	}
	value, err := strconv.ParseBool(d.Val())
	if err != nil {
		return false, d.Errf("invalid boolean %q: %v", d.Val(), err)
	}
	if d.NextArg() {
		return false, d.ArgErr()
	}
	return value, nil
}

var (
	_ caddy.App         = (*SMTPApp)(nil)
	_ caddy.Provisioner = (*SMTPApp)(nil)
	_ caddy.Validator   = (*SMTPApp)(nil)
)
