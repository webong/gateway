package config

import (
	"testing"
	"time"
)

func TestLoadConfigSelectsHTTPRuntime(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_LARAVEL_BACKEND_URL", "http://127.0.0.1:8000")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "gateway-token")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Runtime != "http" || config.RoadRunnerEnabled || config.LaravelBackendURL != "http://127.0.0.1:8000" {
		t.Fatalf("unexpected HTTP runtime config: %+v", config)
	}
	if config.InternalToken != "gateway-token" {
		t.Fatalf("expected transport-neutral token, got %q", config.InternalToken)
	}
}

func TestLoadConfigRequiresExplicitPOP3TLSAndUpstream(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_POP3_ADDR", ":995")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected POP3 configuration to fail closed without TLS and upstreams")
	}
	t.Setenv("GATEWAY_POP3_TLS_CERT_FILE", "/cert.pem")
	t.Setenv("GATEWAY_POP3_TLS_KEY_FILE", "/key.pem")
	t.Setenv("GATEWAY_POP3_UPSTREAMS", `{"primary":{"address":"pop.internal:995","server_name":"pop.internal"}}`)
	settings, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if settings.POP3Upstreams["primary"].ServerName != "pop.internal" {
		t.Fatalf("POP3 upstreams = %+v", settings.POP3Upstreams)
	}
	t.Setenv("GATEWAY_POP3_IDLE_TIMEOUT", "1m")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("POP3 idle timeout below the protocol minimum was accepted")
	}
	t.Setenv("GATEWAY_POP3_IDLE_TIMEOUT", "10m")
	t.Setenv("GATEWAY_POP3_AUTH_BURST", "-1")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("negative POP3 authentication burst was accepted")
	}
}

func TestLoadConfigRequiresExplicitIMAPTLSAndUpstream(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_IMAP_ADDR", ":993")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected IMAP to fail closed without TLS and upstreams")
	}
	t.Setenv("GATEWAY_IMAP_TLS_CERT_FILE", "/cert.pem")
	t.Setenv("GATEWAY_IMAP_TLS_KEY_FILE", "/key.pem")
	t.Setenv("GATEWAY_IMAP_UPSTREAMS", `{"primary":{"address":"imap.internal:993","server_name":"imap.internal"}}`)
	settings, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if settings.IMAPUpstreams["primary"].ServerName != "imap.internal" {
		t.Fatalf("IMAP upstreams = %+v", settings.IMAPUpstreams)
	}
	t.Setenv("GATEWAY_IMAP_AUTH_BURST", "-1")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("negative IMAP authentication burst was accepted")
	}
}

func TestLoadConfigRecipientPreflightDefaultsOnForRelay(t *testing.T) {
	setConfigEnv(t, "http")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.SMTPRecipientPreflight {
		t.Fatal("recipient preflight should remain off without a relay")
	}
	t.Setenv("GATEWAY_SMTP_RELAY_ADDR", "127.0.0.1:2526")
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", t.TempDir())
	config, err = LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.SMTPRecipientPreflight {
		t.Fatal("relay-backed SMTP should preflight recipients by default")
	}
	t.Setenv("GATEWAY_SMTP_RECIPIENT_PREFLIGHT", "false")
	config, err = LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.SMTPRecipientPreflight {
		t.Fatal("custom SMTP planners must be able to disable preflight")
	}
	t.Setenv("GATEWAY_SMTP_RECIPIENT_PREFLIGHT", "true")
	config, err = LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.SMTPRecipientPreflight {
		t.Fatal("recipient preflight setting was not loaded")
	}
}

func TestLoadConfigSMTPConnectionLimit(t *testing.T) {
	setConfigEnv(t, "http")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.SMTPMaxConnections != 1000 {
		t.Fatalf("unexpected default SMTP connection limit: %d", config.SMTPMaxConnections)
	}
	t.Setenv("GATEWAY_SMTP_MAX_CONNECTIONS", "7")
	config, err = LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.SMTPMaxConnections != 7 {
		t.Fatalf("unexpected SMTP connection limit: %d", config.SMTPMaxConnections)
	}
	t.Setenv("GATEWAY_SMTP_MAX_CONNECTIONS", "0")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected zero SMTP connection limit to be rejected")
	}
}

func TestLoadConfigSMTPSourceRateLimit(t *testing.T) {
	setConfigEnv(t, "http")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.SMTPMessagesPerMinute != 120 || config.SMTPMessageBurst != 20 {
		t.Fatalf("unexpected default SMTP source rate limit: %d/minute burst %d", config.SMTPMessagesPerMinute, config.SMTPMessageBurst)
	}
	t.Setenv("GATEWAY_SMTP_MESSAGES_PER_MINUTE", "12")
	t.Setenv("GATEWAY_SMTP_MESSAGE_BURST", "3")
	config, err = LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.SMTPMessagesPerMinute != 12 || config.SMTPMessageBurst != 3 {
		t.Fatalf("unexpected SMTP source rate limit: %d/minute burst %d", config.SMTPMessagesPerMinute, config.SMTPMessageBurst)
	}
	t.Setenv("GATEWAY_SMTP_MESSAGE_BURST", "0")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected zero SMTP source burst to be rejected")
	}
}

func TestLoadConfigWebstreamBounds(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_WEBSTREAM_MAX_CONNECTIONS", "2")
	t.Setenv("GATEWAY_WEBSTREAM_MAX_DURATION", "30m")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.WebstreamMaxConnections != 2 || config.WebstreamMaxDuration != 30*time.Minute {
		t.Fatalf("unexpected Webstream bounds: %+v", config)
	}
	t.Setenv("GATEWAY_WEBSTREAM_MAX_CONNECTIONS", "0")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected zero Webstream capacity to be rejected")
	}
}

func TestLoadConfigWebSocketBounds(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_WEBSOCKET_MAX_CONNECTIONS", "2")
	t.Setenv("GATEWAY_WEBSOCKET_MAX_DURATION", "30m")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.WebSocketMaxConnections != 2 || config.WebSocketMaxDuration != 30*time.Minute {
		t.Fatalf("unexpected WebSocket bounds: %+v", config)
	}
	t.Setenv("GATEWAY_WEBSOCKET_MAX_DURATION", "0s")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected zero WebSocket duration to be rejected")
	}
}

func TestLoadConfigAllowsUnixSocketOnlyForHTTPRuntime(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_LARAVEL_BACKEND_URL", "http://gateway-planner")
	t.Setenv("GATEWAY_LARAVEL_BACKEND_SOCKET", "/run/gateway/planner.sock")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.LaravelBackendSocket != "/run/gateway/planner.sock" {
		t.Fatalf("unexpected backend socket %q", config.LaravelBackendSocket)
	}

	setConfigEnv(t, "roadrunner")
	t.Setenv("GATEWAY_LARAVEL_BACKEND_SOCKET", "/run/gateway/planner.sock")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected backend socket to require HTTP runtime")
	}
}

func TestLoadConfigDefaultsToHTTP(t *testing.T) {
	setConfigEnv(t, "")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Runtime != "http" || config.RoadRunnerEnabled || config.InternalToken != "" {
		t.Fatalf("unexpected default config: %+v", config)
	}
}

func TestLoadRouterConfigSelectsInProcessPlannerWithoutRuntimeSetting(t *testing.T) {
	setConfigEnv(t, "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "gateway-token")
	config, err := LoadRouterConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Runtime != "embedded" || config.RoadRunnerEnabled || config.InternalToken != "gateway-token" {
		t.Fatalf("unexpected Router config: %+v", config)
	}
}

func TestLoadRouterConfigRejectsStandaloneConnectionSettings(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "runtime", key: "GATEWAY_RUNTIME", value: "http"},
		{name: "backend URL", key: "GATEWAY_LARAVEL_BACKEND_URL", value: "http://planner:8080"},
		{name: "backend socket", key: "GATEWAY_LARAVEL_BACKEND_SOCKET", value: "/run/planner.sock"},
		{name: "provisioning", key: "GATEWAY_PROVISIONING_ENABLED", value: "true"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setConfigEnv(t, "")
			t.Setenv("GATEWAY_SCHEDULES_ENABLED", "false")
			t.Setenv(test.key, test.value)
			if _, err := LoadRouterConfig(); err == nil {
				t.Fatalf("expected Router to reject %s", test.key)
			}
		})
	}
}

func TestLoadRouterConfigAllowsEmbeddedSchedulePolling(t *testing.T) {
	setConfigEnv(t, "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "gateway-token")
	t.Setenv("GATEWAY_SCHEDULES_ENABLED", "true")
	config, err := LoadRouterConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.SchedulesEnabled || config.Runtime != "embedded" {
		t.Fatalf("unexpected embedded schedule config: %+v", config)
	}
}

func TestLoadConfigRejectsEmbeddedRuntimeForStandaloneSpinner(t *testing.T) {
	setConfigEnv(t, "embedded")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("standalone Spinner must not select the embedded Router adapter")
	}
}

func TestLoadConfigRejectsOldStandaloneName(t *testing.T) {
	setConfigEnv(t, "standalone")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected standalone to be reserved for the combined Gateway distribution")
	}
}

func setConfigEnv(t *testing.T, runtime string) {
	t.Helper()
	t.Setenv("GATEWAY_RUNTIME", runtime)
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "")
	t.Setenv("GATEWAY_LARAVEL_BACKEND_URL", "")
	t.Setenv("GATEWAY_LARAVEL_BACKEND_SOCKET", "")
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "false")
	t.Setenv("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED", "false")
	t.Setenv("GATEWAY_PROVISIONING_ROUTE_CIDRS", "")
	t.Setenv("GATEWAY_REVERB_ENABLED", "false")
	t.Setenv("GATEWAY_MERCURE_ENABLED", "false")
	t.Setenv("GATEWAY_CENTRIFUGO_ENABLED", "false")
	t.Setenv("GATEWAY_DNS_ADDR", "")
	t.Setenv("GATEWAY_DNS_ZONE", "")
	t.Setenv("GATEWAY_DNS_ZONES", "")
	t.Setenv("GATEWAY_DNS_NAMESERVERS", "")
	t.Setenv("GATEWAY_SMTP_RELAY_ADDR", "")
	t.Setenv("GATEWAY_POP3_ADDR", "")
	t.Setenv("GATEWAY_POP3_UPSTREAMS", "")
	t.Setenv("GATEWAY_POP3_TLS_CERT_FILE", "")
	t.Setenv("GATEWAY_POP3_TLS_KEY_FILE", "")
	t.Setenv("GATEWAY_POP3_AUTH_PER_MINUTE", "")
	t.Setenv("GATEWAY_POP3_AUTH_BURST", "")
	t.Setenv("GATEWAY_IMAP_ADDR", "")
	t.Setenv("GATEWAY_IMAP_UPSTREAMS", "")
	t.Setenv("GATEWAY_IMAP_TLS_CERT_FILE", "")
	t.Setenv("GATEWAY_IMAP_TLS_KEY_FILE", "")
	t.Setenv("GATEWAY_IMAP_AUTH_PER_MINUTE", "")
	t.Setenv("GATEWAY_IMAP_AUTH_BURST", "")
	t.Setenv("GATEWAY_WEBSTREAM_MAX_CONNECTIONS", "")
	t.Setenv("GATEWAY_WEBSTREAM_MAX_DURATION", "")
	t.Setenv("GATEWAY_SMTP_RECIPIENT_PREFLIGHT", "")
	t.Setenv("GATEWAY_SMTP_RELAY_LOCAL_NAME", "")
	t.Setenv("GATEWAY_SMTP_RELAY_SERVER_NAME", "")
	t.Setenv("GATEWAY_SMTP_RELAY_USERNAME", "")
	t.Setenv("GATEWAY_SMTP_RELAY_PASSWORD", "")
	t.Setenv("GATEWAY_SMTP_RELAY_TLS_MODE", "")
	t.Setenv("GATEWAY_SMTP_RELAY_TIMEOUT", "")
	t.Setenv("GATEWAY_EMAIL_CLAMD_ADDR", "")
	t.Setenv("GATEWAY_EMAIL_QUARANTINE_PATH", "")
	t.Setenv("GATEWAY_EMAIL_QUARANTINE_RETENTION", "")
	t.Setenv("GATEWAY_EMAIL_DSN_DOMAIN", "")
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", "")
	t.Setenv("GATEWAY_DELIVERY_DATABASE_URL", "")
	t.Setenv("GATEWAY_DELIVERY_ADMIN_ADDR", "")
	t.Setenv("GATEWAY_DELIVERY_MAX_ATTEMPTS", "")
	t.Setenv("GATEWAY_DELIVERY_INITIAL_BACKOFF", "")
	t.Setenv("GATEWAY_DELIVERY_MAX_BACKOFF", "")
	t.Setenv("GATEWAY_DELIVERY_POLL_INTERVAL", "")
	t.Setenv("GATEWAY_DELIVERY_RECEIPT_RETENTION", "")
}

func TestManagedRouteProxyRequiresPrivateHTTPPlannerWithoutLocalProvisioning(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED", "true")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("accepted managed route proxy without Planner credentials")
	}
	t.Setenv("GATEWAY_LARAVEL_BACKEND_URL", "http://planner.internal")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "secret")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("accepted managed route proxy without destination networks")
	}
	t.Setenv("GATEWAY_PROVISIONING_ROUTE_CIDRS", "10.244.0.0/16")
	config, err := LoadConfig()
	if err != nil || !config.ProvisioningRouteProxy {
		t.Fatalf("managed route proxy not enabled: config=%+v err=%v", config, err)
	}
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "true")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("accepted both remote route proxy and local provisioning")
	}
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "false")
	t.Setenv("GATEWAY_RUNTIME", "roadrunner")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("accepted remote route proxy outside standalone HTTP mode")
	}
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_LARAVEL_BACKEND_URL", "")
	config, err = LoadRouterConfig()
	if err != nil || !config.ProvisioningRouteProxy {
		t.Fatalf("bundled Router did not accept embedded managed lookup: config=%+v err=%v", config, err)
	}
}

func TestLoadConfigLoadsOutboundSMTPRelay(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_SMTP_RELAY_ADDR", "smtp.example.test:587")
	t.Setenv("GATEWAY_SMTP_RELAY_LOCAL_NAME", "gateway.example.test")
	t.Setenv("GATEWAY_SMTP_RELAY_USERNAME", "gateway")
	t.Setenv("GATEWAY_SMTP_RELAY_PASSWORD", "secret")
	t.Setenv("GATEWAY_SMTP_RELAY_TLS_MODE", "starttls")
	t.Setenv("GATEWAY_SMTP_RELAY_TIMEOUT", "12s")
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", "/var/lib/gateway/deliveries")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.SMTPRelayAddress != "smtp.example.test:587" || config.SMTPRelayLocalName != "gateway.example.test" {
		t.Fatalf("unexpected outbound SMTP relay config: %+v", config)
	}
	if config.SMTPRelayTLSMode != "starttls" || config.SMTPRelayTimeout.String() != "12s" {
		t.Fatalf("unexpected outbound SMTP security config: %+v", config)
	}
	if config.DeliverySpoolPath != "/var/lib/gateway/deliveries" || config.DeliveryMaxAttempts != 8 {
		t.Fatalf("unexpected delivery spool config: %+v", config)
	}
}

func TestLoadConfigRequiresDurableSpoolForOutboundSMTP(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_SMTP_RELAY_ADDR", "smtp.example.test:587")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected outbound SMTP relay to require a durable spool")
	}
}

func TestLoadConfigSelectsSharedDeliveryDatabaseWithoutLocalSpool(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_SMTP_RELAY_ADDR", "smtp.example.test:587")
	t.Setenv("GATEWAY_DELIVERY_DATABASE_URL", "postgres://gateway:secret@db.internal/gateway")
	config, err := LoadConfig()
	if err != nil || config.DeliveryDatabaseURL == "" || config.DeliverySpoolPath != "" {
		t.Fatalf("shared delivery database was not selected: %+v, %v", config, err)
	}
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", "/var/lib/gateway/deliveries")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("shared and local delivery backends were both accepted")
	}
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", "")
	t.Setenv("GATEWAY_EMAIL_DSN_DOMAIN", "dsn.example.test")
	config, err = LoadConfig()
	if err != nil || config.EmailDSNDomain != "dsn.example.test" {
		t.Fatalf("shared failure notice domain was rejected: %+v, %v", config, err)
	}
}

func TestLoadConfigRequiresSpoolAndTokenForDeliveryAdmin(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_DELIVERY_ADMIN_ADDR", "127.0.0.1:5052")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("admin listener should require spool and token")
	}
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", "/var/lib/gateway/deliveries")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("admin listener should require internal token")
	}
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "internal-secret")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.DeliveryAdminAddress != "127.0.0.1:5052" {
		t.Fatalf("unexpected admin listener %q", config.DeliveryAdminAddress)
	}
}

func TestLoadConfigUsesBoundedDeliveredReceiptRetention(t *testing.T) {
	setConfigEnv(t, "http")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.DeliveryReceiptRetention != 90*24*time.Hour {
		t.Fatalf("unexpected receipt retention %s", config.DeliveryReceiptRetention)
	}
	t.Setenv("GATEWAY_DELIVERY_RECEIPT_RETENTION", "0")
	config, err = LoadConfig()
	if err != nil || config.DeliveryReceiptRetention != 0 {
		t.Fatalf("zero retention should disable pruning: %+v, %v", config, err)
	}
	t.Setenv("GATEWAY_DELIVERY_RECEIPT_RETENTION", "-1h")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("negative receipt retention must be rejected")
	}
}

func TestLoadConfigRequiresQuarantineForAutomaticRetention(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_EMAIL_QUARANTINE_RETENTION", "720h")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("retention without a quarantine path was accepted")
	}
	t.Setenv("GATEWAY_EMAIL_QUARANTINE_PATH", "/var/lib/gateway/email-quarantine")
	config, err := LoadConfig()
	if err != nil || config.EmailQuarantineRetention != 720*time.Hour || config.EmailClamdAddress != "" {
		t.Fatalf("quarantine without scanner was not accepted: %+v, %v", config, err)
	}
	t.Setenv("GATEWAY_EMAIL_CLAMD_ADDR", "127.0.0.1:3310")
	config, err = LoadConfig()
	if err != nil || config.EmailQuarantineRetention != 720*time.Hour {
		t.Fatalf("quarantine retention was not loaded: %+v, %v", config, err)
	}
	t.Setenv("GATEWAY_EMAIL_QUARANTINE_RETENTION", "-1h")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("negative quarantine retention was accepted")
	}
}

func TestLoadConfigConfiguresAuthorDomainResolver(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_EMAIL_DOMAIN_RESOLVER_ADDR", "127.0.0.1:5353")
	t.Setenv("GATEWAY_EMAIL_DOMAIN_LOOKUP_TIMEOUT", "2s")
	config, err := LoadConfig()
	if err != nil || config.EmailDomainResolverAddress != "127.0.0.1:5353" || config.EmailDomainLookupTimeout != 2*time.Second {
		t.Fatalf("author-domain resolver was not configured: %+v, %v", config, err)
	}
	t.Setenv("GATEWAY_EMAIL_DOMAIN_LOOKUP_TIMEOUT", "0s")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("non-positive author-domain lookup timeout was accepted")
	}
}

func TestLoadConfigRequiresRelayForFailureNotices(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_EMAIL_DSN_DOMAIN", "gateway.example.test")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("failure notice domain without outbound relay was accepted")
	}
	t.Setenv("GATEWAY_SMTP_RELAY_ADDR", "127.0.0.1:2526")
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", t.TempDir())
	config, err := LoadConfig()
	if err != nil || config.EmailDSNDomain != "gateway.example.test" {
		t.Fatalf("failure notice domain was not loaded: %+v, %v", config, err)
	}
}

func TestLoadConfigRequiresRelayForDKIMSigning(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_EMAIL_DKIM_SIGNING_CONFIG", "/private/dkim.json")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("DKIM signing configuration without outbound relay was accepted")
	}
	t.Setenv("GATEWAY_SMTP_RELAY_ADDR", "127.0.0.1:2526")
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", t.TempDir())
	config, err := LoadConfig()
	if err != nil || config.EmailDKIMSigningConfig != "/private/dkim.json" {
		t.Fatalf("DKIM signing configuration was not loaded: %+v, %v", config, err)
	}
}

func TestLoadConfigRejectsOutboundSMTPCredentialsWithoutTLS(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_SMTP_RELAY_USERNAME", "gateway")
	t.Setenv("GATEWAY_SMTP_RELAY_PASSWORD", "secret")
	t.Setenv("GATEWAY_SMTP_RELAY_TLS_MODE", "none")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected outbound SMTP credentials to require TLS")
	}
}

func TestLoadConfigLoadsAuthoritativeDNSSettings(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_DNS_ADDR", ":5353")
	t.Setenv("GATEWAY_DNS_ZONE", "dns.example.test")
	t.Setenv("GATEWAY_DNS_NAMESERVERS", "ns1.example.test, ns2.example.test")
	t.Setenv("GATEWAY_DNS_TTL", "15")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.DNSAddress != ":5353" || config.DNSZone != "dns.example.test" || config.DNSTTL != 15 {
		t.Fatalf("unexpected DNS config: %+v", config)
	}
	if len(config.DNSNameservers) != 2 || config.DNSNameservers[1] != "ns2.example.test" {
		t.Fatalf("unexpected DNS nameservers: %+v", config.DNSNameservers)
	}
}

func TestLoadConfigRequiresDNSZoneAndNameservers(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_DNS_ADDR", ":5353")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected enabled DNS listener to require zone settings")
	}
}

func TestLoadConfigAcceptsMultipleDNSZonesWithoutLegacySettings(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_DNS_ADDR", ":5353")
	t.Setenv("GATEWAY_DNS_ZONES", `[{"name":"one.example.test","nameservers":["ns1.example.test"]},{"name":"two.example.test","nameservers":["ns2.example.test"],"ttl":15}]`)
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(config.DNSZones) != 2 || config.DNSZones[1].Name != "two.example.test" || config.DNSZones[1].TTL != 15 {
		t.Fatalf("unexpected multi-zone configuration: %+v", config.DNSZones)
	}
	t.Setenv("GATEWAY_DNS_ZONE", "legacy.example.test")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("mixed legacy and multi-zone configuration was accepted")
	}
	t.Setenv("GATEWAY_DNS_ZONE", "")
	t.Setenv("GATEWAY_DNS_ZONES", `[{"name":"one.example.test","nameservers":[]}]`)
	if _, err := LoadConfig(); err == nil {
		t.Fatal("zone without nameservers was accepted")
	}
}

func TestLoadConfigAcceptsExecutableWorkloadPaths(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "true")
	t.Setenv("GATEWAY_EXTENSION_PATHS", "/opt/gateway/extensions/mercure:/opt/gateway/extensions/centrifugo")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.Provisioning.Enabled {
		t.Fatalf("unexpected workload configuration: %+v", config)
	}
}

func TestLoadConfigRejectsLegacyWorkloadToggleWithoutExecutable(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "true")
	t.Setenv("GATEWAY_CENTRIFUGO_ENABLED", "true")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected executable path requirement")
	}
}

func TestLoadConfigRequiresProvisioningForReverbWorkload(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_REVERB_ENABLED", "true")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected Reverb workload to require provisioning")
	}
}
