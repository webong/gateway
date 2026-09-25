package config

import "testing"

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

func TestLoadConfigDefaultsToStandalone(t *testing.T) {
	setConfigEnv(t, "")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Runtime != "standalone" || config.RoadRunnerEnabled || config.InternalToken != "" {
		t.Fatalf("unexpected default config: %+v", config)
	}
}

func setConfigEnv(t *testing.T, runtime string) {
	t.Helper()
	t.Setenv("GATEWAY_RUNTIME", runtime)
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "")
	t.Setenv("GATEWAY_LARAVEL_BACKEND_URL", "")
	t.Setenv("GATEWAY_LARAVEL_BACKEND_SOCKET", "")
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "false")
	t.Setenv("GATEWAY_REVERB_ENABLED", "false")
	t.Setenv("GATEWAY_MERCURE_ENABLED", "false")
	t.Setenv("GATEWAY_CENTRIFUGO_ENABLED", "false")
	t.Setenv("GATEWAY_DNS_ADDR", "")
	t.Setenv("GATEWAY_DNS_ZONE", "")
	t.Setenv("GATEWAY_DNS_NAMESERVERS", "")
	t.Setenv("GATEWAY_SMTP_RELAY_ADDR", "")
	t.Setenv("GATEWAY_SMTP_RELAY_LOCAL_NAME", "")
	t.Setenv("GATEWAY_SMTP_RELAY_SERVER_NAME", "")
	t.Setenv("GATEWAY_SMTP_RELAY_USERNAME", "")
	t.Setenv("GATEWAY_SMTP_RELAY_PASSWORD", "")
	t.Setenv("GATEWAY_SMTP_RELAY_TLS_MODE", "")
	t.Setenv("GATEWAY_SMTP_RELAY_TIMEOUT", "")
}

func TestLoadConfigLoadsOutboundSMTPRelay(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_SMTP_RELAY_ADDR", "smtp.example.test:587")
	t.Setenv("GATEWAY_SMTP_RELAY_LOCAL_NAME", "gateway.example.test")
	t.Setenv("GATEWAY_SMTP_RELAY_USERNAME", "gateway")
	t.Setenv("GATEWAY_SMTP_RELAY_PASSWORD", "secret")
	t.Setenv("GATEWAY_SMTP_RELAY_TLS_MODE", "starttls")
	t.Setenv("GATEWAY_SMTP_RELAY_TIMEOUT", "12s")

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

func TestLoadConfigRegistersAllProvisionedWorkloadSettings(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "true")
	t.Setenv("GATEWAY_MERCURE_ENABLED", "true")
	t.Setenv("GATEWAY_CENTRIFUGO_ENABLED", "true")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.Provisioning.Enabled || !config.Mercure.Enabled || !config.Centrifugo.Enabled {
		t.Fatalf("unexpected workload configuration: %+v", config)
	}
}

func TestLoadConfigRequiresProvisioningForReverbWorkload(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_REVERB_ENABLED", "true")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected Reverb workload to require provisioning")
	}
}
