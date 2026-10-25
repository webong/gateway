package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/webong/gateway/pkg/spinner/provision"
)

type Config struct {
	Runtime                    string
	PlannerURL                 string
	PlannerSocket              string
	Port                       string
	InternalToken              string
	MaxWorkers                 int
	MaxQueueSize               int
	RequestTimeout             time.Duration
	AutomationExecutor         string
	AutomationWorkers          int
	AutomationQueueSize        int
	AutomationTimeout          time.Duration
	WebSocketMaxConnections    int
	WebSocketMaxDuration       time.Duration
	WebstreamHeartbeat         time.Duration
	WebstreamRetry             time.Duration
	WebstreamMaxConnections    int
	WebstreamMaxDuration       time.Duration
	WebstreamPrivateNetworks   []netip.Prefix
	HTTPPrivateNetworks        []netip.Prefix
	MaxBodySize                int64
	MaxIdleConns               int
	MaxConnsPerHost            int
	IdleConnTimeout            time.Duration
	LogLevel                   string
	ShutdownTimeout            time.Duration
	SMTPAddress                string
	POP3Address                string
	POP3Hostname               string
	POP3TLSCertFile            string
	POP3TLSKeyFile             string
	POP3Upstreams              map[string]POP3UpstreamSpec
	POP3MaxConnections         int
	POP3AuthPerMinute          int
	POP3AuthBurst              int
	POP3IdleTimeout            time.Duration
	POP3PlannerTimeout         time.Duration
	POP3UpstreamTimeout        time.Duration
	IMAPAddress                string
	IMAPHostname               string
	IMAPTLSCertFile            string
	IMAPTLSKeyFile             string
	IMAPUpstreams              map[string]IMAPUpstreamSpec
	IMAPMaxConnections         int
	IMAPAuthPerMinute          int
	IMAPAuthBurst              int
	IMAPIdleTimeout            time.Duration
	IMAPPlannerTimeout         time.Duration
	IMAPUpstreamTimeout        time.Duration
	SMTPHostname               string
	SMTPMaxMessageSize         int64
	SMTPMaxRecipients          int
	SMTPMaxConnections         int
	SMTPMessagesPerMinute      int
	SMTPMessageBurst           int
	SMTPReadTimeout            time.Duration
	SMTPWriteTimeout           time.Duration
	SMTPPlannerTimeout         time.Duration
	SMTPTLSCertFile            string
	SMTPTLSKeyFile             string
	SMTPImplicitTLS            bool
	SMTPAuthEnabled            bool
	SMTPRecipientPreflight     bool
	EmailClamdNetwork          string
	EmailClamdAddress          string
	EmailClamdTimeout          time.Duration
	EmailDomainResolverAddress string
	EmailDomainLookupTimeout   time.Duration
	EmailQuarantinePath        string
	EmailQuarantineRetention   time.Duration
	EmailDSNDomain             string
	SMTPRelayAddress           string
	SMTPRelayLocalName         string
	SMTPRelayServerName        string
	SMTPRelayUsername          string
	SMTPRelayPassword          string
	SMTPRelayTLSMode           string
	SMTPRelayTimeout           time.Duration
	MailboxSMTPResolverURL     string
	OutboundResolverURL        string
	HostedMailResolverURL      string
	HostedMailAllowedHosts     []string
	EmailDKIMResolverURL       string
	DeliverySpoolPath          string
	DeliveryDatabaseURL        string
	DeliveryAdminAddress       string
	DeliveryAdminAdvertiseURL  string
	DeliveryMaxAttempts        int
	DeliveryInitialBackoff     time.Duration
	DeliveryMaxBackoff         time.Duration
	DeliveryPollInterval       time.Duration
	DeliveryReceiptRetention   time.Duration
	SchedulesEnabled           bool
	SchedulePollInterval       time.Duration
	DNSAddress                 string
	DNSZone                    string
	DNSZones                   []DNSZoneSpec
	DNSNameservers             []string
	DNSSOAEmail                string
	DNSTTL                     uint32
	DNSPlannerTimeout          time.Duration
	DNSReadTimeout             time.Duration
	DNSWriteTimeout            time.Duration
	DNSMaxResponseRecords      int
	DNSRateLimit               int
	DNSRateBurst               int
	DNSZoneRefreshInterval     time.Duration
	Provisioning               provision.HostConfig
	ProvisioningRouteProxy     bool
	ProvisioningRouteCIDRs     []string
}

type DNSZoneSpec struct {
	Name        string   `json:"name"`
	Nameservers []string `json:"nameservers"`
	SOAEmail    string   `json:"soa_email,omitempty"`
	TTL         uint32   `json:"ttl,omitempty"`
}

type POP3UpstreamSpec struct {
	Address    string `json:"address"`
	ServerName string `json:"server_name"`
}

type IMAPUpstreamSpec struct {
	Address    string `json:"address"`
	ServerName string `json:"server_name"`
}

// ParseWebstreamPrivateNetworks sets a deployment ceiling, not a source grant.
// Planner grants must be narrower than these ranges to permit a private dial.
func ParseWebstreamPrivateNetworks(raw string) ([]netip.Prefix, error) {
	return parsePrivateNetworks(raw, "GATEWAY_WEBSTREAM_PRIVATE_NETWORK_CIDRS")
}

func ParseHTTPPrivateNetworks(raw string) ([]netip.Prefix, error) {
	return parsePrivateNetworks(raw, "GATEWAY_HTTP_PRIVATE_NETWORK_CIDRS")
}

func parsePrivateNetworks(raw, setting string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	privateRanges := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("fc00::/7"),
	}
	var result []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil || prefix.Addr().Is4In6() || prefix != prefix.Masked() {
			return nil, fmt.Errorf("invalid %s entry %q", setting, item)
		}
		private := false
		for _, allowed := range privateRanges {
			if prefix.Bits() >= allowed.Bits() && allowed.Contains(prefix.Addr()) {
				private = true
				break
			}
		}
		if !private {
			return nil, fmt.Errorf("Webstream private network %q is outside private address space", item)
		}
		result = append(result, prefix)
	}
	return result, nil
}

func LoadConfig() (*Config, error) {
	runtime := getEnv("GATEWAY_RUNTIME", "")
	if runtime == "" {
		runtime = "http"
	}
	runtime = strings.ToLower(strings.TrimSpace(runtime))
	if runtime != "http" && runtime != "diagnostic" {
		return nil, fmt.Errorf("unsupported GATEWAY_RUNTIME %q (expected http or diagnostic)", runtime)
	}
	return loadConfig(runtime)
}

// LoadRouterConfig loads Spinner settings for a Router that already owns its
// in-process Planner adapter. It never selects a network backend from the
// standalone Spinner runtime setting.
func LoadRouterConfig() (*Config, error) {
	if value := strings.TrimSpace(os.Getenv("GATEWAY_RUNTIME")); value != "" {
		return nil, fmt.Errorf("GATEWAY_RUNTIME=%s is for standalone Spinner; the bundled Router selects its Planner adapter in-process", value)
	}
	return loadConfig("embedded")
}

func loadConfig(runtime string) (*Config, error) {
	runtime = strings.ToLower(strings.TrimSpace(runtime))
	if runtime != "http" && runtime != "diagnostic" && runtime != "embedded" {
		return nil, fmt.Errorf("unsupported runtime %q (expected http, diagnostic, or embedded)", runtime)
	}

	provisioningConfig, err := provision.LoadConfig(runtime)
	if err != nil {
		return nil, err
	}
	privateNetworks, err := ParseWebstreamPrivateNetworks(os.Getenv("GATEWAY_WEBSTREAM_PRIVATE_NETWORK_CIDRS"))
	if err != nil {
		return nil, err
	}
	httpPrivateNetworks, err := ParseHTTPPrivateNetworks(os.Getenv("GATEWAY_HTTP_PRIVATE_NETWORK_CIDRS"))
	if err != nil {
		return nil, err
	}
	legacyEnabled := getEnvBool("GATEWAY_REVERB_ENABLED", false) || getEnvBool("GATEWAY_MERCURE_ENABLED", false) || getEnvBool("GATEWAY_CENTRIFUGO_ENABLED", false)
	if (legacyEnabled || os.Getenv("GATEWAY_INTEGRATION_PATHS") != "") && !provisioningConfig.Enabled {
		return nil, fmt.Errorf("integrations require GATEWAY_PROVISIONING_ENABLED")
	}
	if legacyEnabled && os.Getenv("GATEWAY_INTEGRATION_PATHS") == "" {
		return nil, fmt.Errorf("set GATEWAY_INTEGRATION_PATHS to installed integrations; GATEWAY_*_ENABLED alone no longer loads Go code")
	}

	dnsAddress := getEnv("GATEWAY_DNS_ADDR", "")
	dynamicDNS := (runtime == "embedded" || runtime == "http" && getEnv("GATEWAY_PLANNER_URL", "") != "") && getEnv("GATEWAY_INTERNAL_TOKEN", "") != ""
	if dnsAddress != "" && !dynamicDNS {
		return nil, fmt.Errorf("GATEWAY_DNS_ADDR requires a private Planner connection and GATEWAY_INTERNAL_TOKEN")
	}
	smtpRelayAddress := getEnv("GATEWAY_SMTP_RELAY_ADDR", "")

	config := &Config{
		Runtime:                    runtime,
		PlannerURL:                 getEnv("GATEWAY_PLANNER_URL", ""),
		PlannerSocket:              getEnv("GATEWAY_PLANNER_SOCKET", ""),
		Port:                       getEnv("PORT", "5001"),
		InternalToken:              getEnv("GATEWAY_INTERNAL_TOKEN", ""),
		MaxWorkers:                 getEnvInt("MAX_WORKERS", 100),
		MaxQueueSize:               getEnvInt("MAX_QUEUE_SIZE", 1000),
		RequestTimeout:             getEnvDuration("REQUEST_TIMEOUT", 30*time.Second),
		AutomationExecutor:         strings.ToLower(strings.TrimSpace(getEnv("GATEWAY_AUTOMATION_EXECUTOR", "ergo"))),
		AutomationWorkers:          getEnvInt("GATEWAY_AUTOMATION_WORKERS", 8),
		AutomationQueueSize:        getEnvInt("GATEWAY_AUTOMATION_QUEUE_SIZE", 32),
		AutomationTimeout:          getEnvDuration("GATEWAY_AUTOMATION_TIMEOUT", 10*time.Second),
		WebSocketMaxConnections:    getEnvInt("GATEWAY_WEBSOCKET_MAX_CONNECTIONS", 1000),
		WebSocketMaxDuration:       getEnvDuration("GATEWAY_WEBSOCKET_MAX_DURATION", 24*time.Hour),
		WebstreamHeartbeat:         getEnvDuration("GATEWAY_WEBSTREAM_HEARTBEAT_INTERVAL", 15*time.Second),
		WebstreamRetry:             getEnvDuration("GATEWAY_WEBSTREAM_RETRY", 3*time.Second),
		WebstreamMaxConnections:    getEnvInt("GATEWAY_WEBSTREAM_MAX_CONNECTIONS", 1000),
		WebstreamMaxDuration:       getEnvDuration("GATEWAY_WEBSTREAM_MAX_DURATION", 24*time.Hour),
		WebstreamPrivateNetworks:   privateNetworks,
		HTTPPrivateNetworks:        httpPrivateNetworks,
		MaxBodySize:                getEnvInt64("MAX_BODY_SIZE", 10*1024*1024),
		MaxIdleConns:               getEnvInt("MAX_IDLE_CONNS", 100),
		MaxConnsPerHost:            getEnvInt("MAX_CONNS_PER_HOST", 100),
		IdleConnTimeout:            getEnvDuration("IDLE_CONN_TIMEOUT", 90*time.Second),
		LogLevel:                   getEnv("LOG_LEVEL", "debug"),
		ShutdownTimeout:            getEnvDuration("SHUTDOWN_TIMEOUT", 30*time.Second),
		SMTPAddress:                getEnv("GATEWAY_SMTP_ADDR", ""),
		POP3Address:                getEnv("GATEWAY_POP3_ADDR", ""),
		POP3Hostname:               getEnv("GATEWAY_POP3_HOSTNAME", "gateway.local"),
		POP3TLSCertFile:            getEnv("GATEWAY_POP3_TLS_CERT_FILE", ""),
		POP3TLSKeyFile:             getEnv("GATEWAY_POP3_TLS_KEY_FILE", ""),
		POP3MaxConnections:         getEnvInt("GATEWAY_POP3_MAX_CONNECTIONS", 1000),
		POP3AuthPerMinute:          getEnvInt("GATEWAY_POP3_AUTH_PER_MINUTE", 30),
		POP3AuthBurst:              getEnvInt("GATEWAY_POP3_AUTH_BURST", 5),
		POP3IdleTimeout:            getEnvDuration("GATEWAY_POP3_IDLE_TIMEOUT", 10*time.Minute),
		POP3PlannerTimeout:         getEnvDuration("GATEWAY_POP3_PLANNER_TIMEOUT", 10*time.Second),
		POP3UpstreamTimeout:        getEnvDuration("GATEWAY_POP3_UPSTREAM_TIMEOUT", 10*time.Second),
		IMAPAddress:                getEnv("GATEWAY_IMAP_ADDR", ""),
		IMAPHostname:               getEnv("GATEWAY_IMAP_HOSTNAME", "gateway.local"),
		IMAPTLSCertFile:            getEnv("GATEWAY_IMAP_TLS_CERT_FILE", ""),
		IMAPTLSKeyFile:             getEnv("GATEWAY_IMAP_TLS_KEY_FILE", ""),
		IMAPMaxConnections:         getEnvInt("GATEWAY_IMAP_MAX_CONNECTIONS", 1000),
		IMAPAuthPerMinute:          getEnvInt("GATEWAY_IMAP_AUTH_PER_MINUTE", 30),
		IMAPAuthBurst:              getEnvInt("GATEWAY_IMAP_AUTH_BURST", 5),
		IMAPIdleTimeout:            getEnvDuration("GATEWAY_IMAP_IDLE_TIMEOUT", 30*time.Minute),
		IMAPPlannerTimeout:         getEnvDuration("GATEWAY_IMAP_PLANNER_TIMEOUT", 10*time.Second),
		IMAPUpstreamTimeout:        getEnvDuration("GATEWAY_IMAP_UPSTREAM_TIMEOUT", 10*time.Second),
		SMTPHostname:               getEnv("GATEWAY_SMTP_HOSTNAME", "gateway.local"),
		SMTPMaxMessageSize:         getEnvInt64("GATEWAY_SMTP_MAX_MESSAGE_SIZE", 10*1024*1024),
		SMTPMaxRecipients:          getEnvInt("GATEWAY_SMTP_MAX_RECIPIENTS", 100),
		SMTPMaxConnections:         getEnvInt("GATEWAY_SMTP_MAX_CONNECTIONS", 1000),
		SMTPMessagesPerMinute:      getEnvInt("GATEWAY_SMTP_MESSAGES_PER_MINUTE", 120),
		SMTPMessageBurst:           getEnvInt("GATEWAY_SMTP_MESSAGE_BURST", 20),
		SMTPReadTimeout:            getEnvDuration("GATEWAY_SMTP_READ_TIMEOUT", 5*time.Minute),
		SMTPWriteTimeout:           getEnvDuration("GATEWAY_SMTP_WRITE_TIMEOUT", 30*time.Second),
		SMTPPlannerTimeout:         getEnvDuration("GATEWAY_SMTP_PLANNER_TIMEOUT", 30*time.Second),
		SMTPTLSCertFile:            getEnv("GATEWAY_SMTP_TLS_CERT_FILE", ""),
		SMTPTLSKeyFile:             getEnv("GATEWAY_SMTP_TLS_KEY_FILE", ""),
		SMTPImplicitTLS:            getEnvBool("GATEWAY_SMTP_IMPLICIT_TLS", false),
		SMTPAuthEnabled:            getEnvBool("GATEWAY_SMTP_AUTH_ENABLED", false),
		SMTPRecipientPreflight:     getEnvBool("GATEWAY_SMTP_RECIPIENT_PREFLIGHT", smtpRelayAddress != ""),
		EmailClamdNetwork:          getEnv("GATEWAY_EMAIL_CLAMD_NETWORK", "tcp"),
		EmailClamdAddress:          getEnv("GATEWAY_EMAIL_CLAMD_ADDR", ""),
		EmailClamdTimeout:          getEnvDuration("GATEWAY_EMAIL_CLAMD_TIMEOUT", 10*time.Second),
		EmailDomainResolverAddress: getEnv("GATEWAY_EMAIL_DOMAIN_RESOLVER_ADDR", ""),
		EmailDomainLookupTimeout:   getEnvDuration("GATEWAY_EMAIL_DOMAIN_LOOKUP_TIMEOUT", 3*time.Second),
		EmailQuarantinePath:        getEnv("GATEWAY_EMAIL_QUARANTINE_PATH", ""),
		EmailQuarantineRetention:   getEnvDuration("GATEWAY_EMAIL_QUARANTINE_RETENTION", 0),
		EmailDSNDomain:             getEnv("GATEWAY_EMAIL_DSN_DOMAIN", ""),
		SMTPRelayAddress:           smtpRelayAddress,
		SMTPRelayLocalName:         getEnv("GATEWAY_SMTP_RELAY_LOCAL_NAME", "gateway.local"),
		SMTPRelayServerName:        getEnv("GATEWAY_SMTP_RELAY_SERVER_NAME", ""),
		SMTPRelayUsername:          getEnv("GATEWAY_SMTP_RELAY_USERNAME", ""),
		SMTPRelayPassword:          getEnv("GATEWAY_SMTP_RELAY_PASSWORD", ""),
		SMTPRelayTLSMode:           strings.ToLower(getEnv("GATEWAY_SMTP_RELAY_TLS_MODE", "starttls")),
		SMTPRelayTimeout:           getEnvDuration("GATEWAY_SMTP_RELAY_TIMEOUT", 30*time.Second),
		MailboxSMTPResolverURL:     getEnv("GATEWAY_MAILBOX_SMTP_RESOLVER_URL", ""),
		OutboundResolverURL:        getEnv("GATEWAY_OUTBOUND_RESOLVER_URL", ""),
		HostedMailResolverURL:      getEnv("GATEWAY_HOSTED_MAIL_RESOLVER_URL", ""),
		HostedMailAllowedHosts:     getEnvList("GATEWAY_HOSTED_MAIL_ALLOWED_HOSTS"),
		EmailDKIMResolverURL:       getEnv("GATEWAY_EMAIL_DKIM_RESOLVER_URL", ""),
		DeliverySpoolPath:          getEnv("GATEWAY_DELIVERY_SPOOL_PATH", ""),
		DeliveryDatabaseURL:        getEnv("GATEWAY_DELIVERY_DATABASE_URL", ""),
		DeliveryAdminAddress:       getEnv("GATEWAY_DELIVERY_ADMIN_ADDR", ""),
		DeliveryAdminAdvertiseURL:  getEnv("GATEWAY_DELIVERY_ADMIN_ADVERTISE_URL", ""),
		DeliveryMaxAttempts:        getEnvInt("GATEWAY_DELIVERY_MAX_ATTEMPTS", 8),
		DeliveryInitialBackoff:     getEnvDuration("GATEWAY_DELIVERY_INITIAL_BACKOFF", 5*time.Second),
		DeliveryMaxBackoff:         getEnvDuration("GATEWAY_DELIVERY_MAX_BACKOFF", time.Hour),
		DeliveryPollInterval:       getEnvDuration("GATEWAY_DELIVERY_POLL_INTERVAL", time.Second),
		DeliveryReceiptRetention:   getEnvDuration("GATEWAY_DELIVERY_RECEIPT_RETENTION", 90*24*time.Hour),
		SchedulesEnabled:           getEnvBool("GATEWAY_SCHEDULES_ENABLED", runtime == "embedded"),
		SchedulePollInterval:       getEnvDuration("GATEWAY_SCHEDULE_POLL_INTERVAL", 5*time.Second),
		DNSAddress:                 dnsAddress,
		DNSPlannerTimeout:          getEnvDuration("GATEWAY_DNS_PLANNER_TIMEOUT", 750*time.Millisecond),
		DNSReadTimeout:             getEnvDuration("GATEWAY_DNS_READ_TIMEOUT", 2*time.Second),
		DNSWriteTimeout:            getEnvDuration("GATEWAY_DNS_WRITE_TIMEOUT", 2*time.Second),
		DNSMaxResponseRecords:      getEnvInt("GATEWAY_DNS_MAX_RESPONSE_RECORDS", 16),
		DNSRateLimit:               getEnvInt("GATEWAY_DNS_RATE_LIMIT", 5000),
		DNSRateBurst:               getEnvInt("GATEWAY_DNS_RATE_BURST", 10000),
		DNSZoneRefreshInterval:     getEnvDuration("GATEWAY_DNS_ZONE_REFRESH_INTERVAL", 5*time.Second),
		Provisioning:               provisioningConfig,
		ProvisioningRouteProxy:     getEnvBool("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED", false),
		ProvisioningRouteCIDRs:     getEnvList("GATEWAY_PROVISIONING_ROUTE_CIDRS"),
	}
	if config.PlannerSocket != "" && config.Runtime != "http" {
		return nil, fmt.Errorf("GATEWAY_PLANNER_SOCKET requires an external Planner HTTP connection")
	}
	if config.SchedulesEnabled && config.Runtime != "http" && config.Runtime != "embedded" {
		return nil, fmt.Errorf("GATEWAY_SCHEDULES_ENABLED requires an HTTP or bundled Router Planner connection")
	}
	if config.Runtime == "embedded" && config.PlannerURL != "" {
		return nil, fmt.Errorf("GATEWAY_PLANNER_URL is for an external Planner; the bundled Router uses its in-process Planner")
	}
	if config.ProvisioningRouteProxy && (config.Runtime != "http" && config.Runtime != "embedded" || config.Provisioning.Enabled) {
		return nil, fmt.Errorf("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED requires HTTP Spinner or bundled Router without a local provisioning worker")
	}
	if config.ProvisioningRouteProxy && (config.Runtime == "http" && config.PlannerURL == "" || config.InternalToken == "") {
		return nil, fmt.Errorf("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED requires a Planner connection and GATEWAY_INTERNAL_TOKEN")
	}
	if config.ProvisioningRouteProxy && len(config.ProvisioningRouteCIDRs) == 0 {
		return nil, fmt.Errorf("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED requires GATEWAY_PROVISIONING_ROUTE_CIDRS")
	}
	if config.SchedulePollInterval <= 0 {
		return nil, fmt.Errorf("GATEWAY_SCHEDULE_POLL_INTERVAL must be positive")
	}
	if config.AutomationExecutor != "direct" && config.AutomationExecutor != "goroutine" && config.AutomationExecutor != "ergo" {
		return nil, fmt.Errorf("GATEWAY_AUTOMATION_EXECUTOR must be direct, goroutine, or ergo")
	}
	if config.AutomationWorkers < 1 || config.AutomationQueueSize < 0 || config.AutomationTimeout <= 0 {
		return nil, fmt.Errorf("GATEWAY_AUTOMATION_WORKERS and GATEWAY_AUTOMATION_TIMEOUT must be positive and GATEWAY_AUTOMATION_QUEUE_SIZE must be non-negative")
	}
	if config.DNSZoneRefreshInterval <= 0 {
		return nil, fmt.Errorf("GATEWAY_DNS_ZONE_REFRESH_INTERVAL must be positive")
	}
	if config.WebstreamHeartbeat < 0 || config.WebstreamRetry <= 0 {
		return nil, fmt.Errorf("GATEWAY_WEBSTREAM_HEARTBEAT_INTERVAL must be non-negative and GATEWAY_WEBSTREAM_RETRY must be positive")
	}
	if config.WebstreamMaxConnections < 1 || config.WebstreamMaxDuration <= 0 {
		return nil, fmt.Errorf("GATEWAY_WEBSTREAM_MAX_CONNECTIONS and GATEWAY_WEBSTREAM_MAX_DURATION must be positive")
	}
	if config.WebSocketMaxConnections < 1 || config.WebSocketMaxDuration <= 0 {
		return nil, fmt.Errorf("GATEWAY_WEBSOCKET_MAX_CONNECTIONS and GATEWAY_WEBSOCKET_MAX_DURATION must be positive")
	}
	if config.SMTPMaxConnections < 1 {
		return nil, fmt.Errorf("GATEWAY_SMTP_MAX_CONNECTIONS must be positive")
	}
	if config.POP3Address != "" {
		if config.POP3TLSCertFile == "" || config.POP3TLSKeyFile == "" {
			return nil, fmt.Errorf("GATEWAY_POP3_ADDR requires a TLS certificate and private key")
		}
		if config.POP3MaxConnections < 1 || config.POP3AuthPerMinute < 1 || config.POP3AuthBurst < 1 || config.POP3IdleTimeout < 10*time.Minute || config.POP3PlannerTimeout <= 0 || config.POP3UpstreamTimeout <= 0 {
			return nil, fmt.Errorf("invalid POP3 connection or timeout limits")
		}
	}
	if config.IMAPAddress != "" {
		if config.IMAPTLSCertFile == "" || config.IMAPTLSKeyFile == "" {
			return nil, fmt.Errorf("GATEWAY_IMAP_ADDR requires a TLS certificate and private key")
		}
		if config.IMAPMaxConnections < 1 || config.IMAPAuthPerMinute < 1 || config.IMAPAuthBurst < 1 || config.IMAPIdleTimeout < 10*time.Minute || config.IMAPPlannerTimeout <= 0 || config.IMAPUpstreamTimeout <= 0 {
			return nil, fmt.Errorf("invalid IMAP connection or timeout limits")
		}
	}
	if config.SMTPMessagesPerMinute < 1 || config.SMTPMessageBurst < 1 {
		return nil, fmt.Errorf("GATEWAY_SMTP_MESSAGES_PER_MINUTE and GATEWAY_SMTP_MESSAGE_BURST must be positive")
	}
	if (config.SMTPRelayUsername == "") != (config.SMTPRelayPassword == "") {
		return nil, fmt.Errorf("GATEWAY_SMTP_RELAY_USERNAME and GATEWAY_SMTP_RELAY_PASSWORD must be configured together")
	}
	if config.SMTPRelayTLSMode != "none" && config.SMTPRelayTLSMode != "starttls" && config.SMTPRelayTLSMode != "implicit" {
		return nil, fmt.Errorf("unsupported GATEWAY_SMTP_RELAY_TLS_MODE %q (expected none, starttls, or implicit)", config.SMTPRelayTLSMode)
	}
	if config.SMTPRelayUsername != "" && config.SMTPRelayTLSMode == "none" {
		return nil, fmt.Errorf("GATEWAY_SMTP_RELAY_USERNAME requires outbound SMTP TLS")
	}
	if config.SMTPRelayTimeout <= 0 {
		return nil, fmt.Errorf("GATEWAY_SMTP_RELAY_TIMEOUT must be positive")
	}
	if config.EmailClamdAddress != "" && config.EmailClamdTimeout <= 0 {
		return nil, fmt.Errorf("GATEWAY_EMAIL_CLAMD_TIMEOUT must be positive")
	}
	if config.EmailDomainResolverAddress != "" && config.EmailDomainLookupTimeout <= 0 {
		return nil, fmt.Errorf("GATEWAY_EMAIL_DOMAIN_LOOKUP_TIMEOUT must be positive")
	}
	if config.EmailQuarantineRetention < 0 || (config.EmailQuarantineRetention > 0 && config.EmailQuarantinePath == "") {
		return nil, fmt.Errorf("GATEWAY_EMAIL_QUARANTINE_RETENTION requires a quarantine path and cannot be negative")
	}
	if config.EmailDSNDomain != "" && config.SMTPRelayAddress == "" {
		return nil, fmt.Errorf("GATEWAY_EMAIL_DSN_DOMAIN requires an outbound SMTP relay")
	}
	if config.EmailDKIMResolverURL != "" && (config.SMTPRelayAddress == "" && config.OutboundResolverURL == "" || config.InternalToken == "") {
		return nil, fmt.Errorf("GATEWAY_EMAIL_DKIM_RESOLVER_URL requires an outbound SMTP relay and internal token")
	}
	if strings.TrimSpace(config.DeliverySpoolPath) != "" && strings.TrimSpace(config.DeliveryDatabaseURL) != "" {
		return nil, fmt.Errorf("GATEWAY_DELIVERY_SPOOL_PATH and GATEWAY_DELIVERY_DATABASE_URL are mutually exclusive")
	}
	if config.SMTPRelayAddress != "" && strings.TrimSpace(config.DeliverySpoolPath) == "" && strings.TrimSpace(config.DeliveryDatabaseURL) == "" {
		return nil, fmt.Errorf("GATEWAY_DELIVERY_SPOOL_PATH or GATEWAY_DELIVERY_DATABASE_URL is required when GATEWAY_SMTP_RELAY_ADDR is set")
	}
	if config.MailboxSMTPResolverURL != "" && config.InternalToken == "" {
		return nil, fmt.Errorf("mailbox SMTP requires GATEWAY_INTERNAL_TOKEN")
	}
	if config.OutboundResolverURL != "" && (config.InternalToken == "" || strings.TrimSpace(config.DeliverySpoolPath) == "" && strings.TrimSpace(config.DeliveryDatabaseURL) == "") {
		return nil, fmt.Errorf("managed outbound SMTP requires internal token and durable delivery storage")
	}
	if config.MailboxSMTPResolverURL != "" && strings.TrimSpace(config.DeliverySpoolPath) == "" && strings.TrimSpace(config.DeliveryDatabaseURL) == "" {
		return nil, fmt.Errorf("mailbox SMTP requires durable delivery storage")
	}
	if config.HostedMailResolverURL != "" && (config.InternalToken == "" || len(config.HostedMailAllowedHosts) == 0) {
		return nil, fmt.Errorf("hosted mail requires a private resolver, internal token, and deployed allowed hosts")
	}
	if config.HostedMailResolverURL != "" && strings.TrimSpace(config.DeliverySpoolPath) == "" && strings.TrimSpace(config.DeliveryDatabaseURL) == "" {
		return nil, fmt.Errorf("hosted mail adapters require durable delivery storage")
	}
	if config.DeliveryAdminAddress != "" && config.InternalToken == "" {
		return nil, fmt.Errorf("GATEWAY_DELIVERY_ADMIN_ADDR requires GATEWAY_INTERNAL_TOKEN")
	}
	if config.DeliveryAdminAdvertiseURL != "" && (config.DeliveryAdminAddress == "" || config.DeliveryDatabaseURL == "") {
		return nil, fmt.Errorf("GATEWAY_DELIVERY_ADMIN_ADVERTISE_URL requires GATEWAY_DELIVERY_ADMIN_ADDR and shared PostgreSQL")
	}
	if config.DeliveryMaxAttempts < 1 || config.DeliveryInitialBackoff <= 0 || config.DeliveryMaxBackoff < config.DeliveryInitialBackoff || config.DeliveryPollInterval <= 0 || config.DeliveryReceiptRetention < 0 {
		return nil, fmt.Errorf("Gateway delivery spool attempts and durations are invalid")
	}

	return config, nil
}

func getEnvList(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}

	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}

	return parsed
}
