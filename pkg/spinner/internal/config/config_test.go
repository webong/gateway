package config

import (
	"testing"
	"time"
)

func TestLoadConfigSelectsHTTPRuntime(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_PLANNER_URL", "http://127.0.0.1:8000")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "gateway-token")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Runtime != "http" || config.PlannerURL != "http://127.0.0.1:8000" || config.InternalToken != "gateway-token" {
		t.Fatalf("unexpected HTTP runtime config: %+v", config)
	}
}

func TestLoadConfigIgnoresRemovedProductEnvironment(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_SMTP_ADDR", ":25")
	t.Setenv("GATEWAY_WEBSTREAM_MAX_CONNECTIONS", "0")
	if _, err := LoadConfig(); err != nil {
		t.Fatalf("product settings must be owned by extension desired state: %v", err)
	}
}

func TestLoadConfigAllowsUnixSocketOnlyForHTTPRuntime(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_PLANNER_URL", "http://gateway-planner")
	t.Setenv("GATEWAY_PLANNER_SOCKET", "/run/gateway/planner.sock")
	config, err := LoadConfig()
	if err != nil || config.PlannerSocket != "/run/gateway/planner.sock" {
		t.Fatalf("unexpected Planner socket: config=%+v err=%v", config, err)
	}
	setConfigEnv(t, "diagnostic")
	t.Setenv("GATEWAY_PLANNER_SOCKET", "/run/gateway/planner.sock")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected Planner socket to require HTTP runtime")
	}
}

func TestLoadConfigDefaultsToHTTP(t *testing.T) {
	setConfigEnv(t, "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Runtime != "http" || config.InternalToken != "" {
		t.Fatalf("unexpected default config: %+v", config)
	}
}

func TestLoadRouterConfigSelectsInProcessPlannerWithoutRuntimeSetting(t *testing.T) {
	setConfigEnv(t, "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "gateway-token")
	config, err := LoadRouterConfig()
	if err != nil || config.Runtime != "embedded" || config.InternalToken != "gateway-token" {
		t.Fatalf("unexpected Router config: %+v, %v", config, err)
	}
}

func TestAutomationActorDefaultsAndValidation(t *testing.T) {
	setConfigEnv(t, "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "gateway-token")
	config, err := LoadRouterConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.AutomationExecutor != "ergo" || config.AutomationWorkers != 8 || config.AutomationQueueSize != 32 || config.AutomationTimeout != 10*time.Second {
		t.Fatalf("unexpected automation actor defaults: %+v", config)
	}
	for _, mode := range []string{"direct", "goroutine", "ergo"} {
		t.Run(mode, func(t *testing.T) {
			setConfigEnv(t, "")
			t.Setenv("GATEWAY_INTERNAL_TOKEN", "gateway-token")
			t.Setenv("GATEWAY_AUTOMATION_EXECUTOR", mode)
			configured, err := LoadRouterConfig()
			if err != nil || configured.AutomationExecutor != mode {
				t.Fatalf("failed to select %q: config=%+v err=%v", mode, configured, err)
			}
		})
	}
}

func TestLoadRouterConfigRejectsStandaloneConnectionSettings(t *testing.T) {
	for _, test := range []struct{ name, key, value string }{
		{"runtime", "GATEWAY_RUNTIME", "http"},
		{"Planner URL", "GATEWAY_PLANNER_URL", "http://planner:8080"},
		{"Planner socket", "GATEWAY_PLANNER_SOCKET", "/run/planner.sock"},
		{"provisioning", "GATEWAY_PROVISIONING_ENABLED", "true"},
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
	if err != nil || !config.SchedulesEnabled || config.Runtime != "embedded" {
		t.Fatalf("unexpected embedded schedule config: %+v, %v", config, err)
	}
}

func TestLoadConfigRejectsUnsupportedRuntimes(t *testing.T) {
	for _, runtime := range []string{"embedded", "standalone", "roadrunner"} {
		t.Run(runtime, func(t *testing.T) {
			setConfigEnv(t, runtime)
			if _, err := LoadConfig(); err == nil {
				t.Fatalf("standalone Spinner accepted %q", runtime)
			}
		})
	}
}

func TestManagedRouteProxyRequiresPrivateHTTPPlannerWithoutLocalProvisioning(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED", "true")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("accepted managed route proxy without Planner credentials")
	}
	t.Setenv("GATEWAY_PLANNER_URL", "http://planner.internal")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "secret")
	t.Setenv("GATEWAY_PROVISIONING_ROUTE_CIDRS", "10.244.0.0/16")
	config, err := LoadConfig()
	if err != nil || !config.ProvisioningRouteProxy {
		t.Fatalf("managed route proxy not enabled: config=%+v err=%v", config, err)
	}
}

func TestLoadConfigSelectsOneDeliveryBackend(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_DELIVERY_DATABASE_URL", "postgres://gateway:secret@db.internal/gateway")
	config, err := LoadConfig()
	if err != nil || config.DeliveryDatabaseURL == "" || config.DeliverySpoolPath != "" {
		t.Fatalf("shared delivery database was not selected: %+v, %v", config, err)
	}
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", "/var/lib/gateway/deliveries")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("shared and local delivery backends were both accepted")
	}
}

func TestLoadConfigRequiresTokenForDeliveryAdmin(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_DELIVERY_ADMIN_ADDR", "127.0.0.1:5052")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("admin listener should require an internal token")
	}
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "internal-secret")
	config, err := LoadConfig()
	if err != nil || config.DeliveryAdminAddress != "127.0.0.1:5052" {
		t.Fatalf("unexpected admin listener: %+v, %v", config, err)
	}
}

func TestLoadConfigUsesBoundedDeliveredReceiptRetention(t *testing.T) {
	setConfigEnv(t, "http")
	config, err := LoadConfig()
	if err != nil || config.DeliveryReceiptRetention != 90*24*time.Hour {
		t.Fatalf("unexpected receipt retention: %+v, %v", config, err)
	}
	t.Setenv("GATEWAY_DELIVERY_RECEIPT_RETENTION", "-1h")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("negative receipt retention must be rejected")
	}
}

func TestLoadConfigAcceptsIntegrationPaths(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "true")
	t.Setenv("GATEWAY_INTEGRATION_PATHS", "/opt/gateway/integrations/mercure:/opt/gateway/integrations/centrifugo")
	config, err := LoadConfig()
	if err != nil || !config.Provisioning.Enabled {
		t.Fatalf("unexpected integration configuration: %+v, %v", config, err)
	}
}

func setConfigEnv(t *testing.T, runtime string) {
	t.Helper()
	for key, value := range map[string]string{
		"GATEWAY_RUNTIME":                          runtime,
		"GATEWAY_AUTOMATION_EXECUTOR":              "",
		"GATEWAY_INTERNAL_TOKEN":                   "",
		"GATEWAY_PLANNER_URL":                      "",
		"GATEWAY_PLANNER_SOCKET":                   "",
		"GATEWAY_PROVISIONING_ENABLED":             "false",
		"GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED": "false",
		"GATEWAY_PROVISIONING_ROUTE_CIDRS":         "",
		"GATEWAY_REVERB_ENABLED":                   "false",
		"GATEWAY_MERCURE_ENABLED":                  "false",
		"GATEWAY_CENTRIFUGO_ENABLED":               "false",
		"GATEWAY_DELIVERY_SPOOL_PATH":              "",
		"GATEWAY_DELIVERY_DATABASE_URL":            "",
		"GATEWAY_DELIVERY_ADMIN_ADDR":              "",
		"GATEWAY_DELIVERY_ADMIN_ADVERTISE_URL":     "",
		"GATEWAY_DELIVERY_RECEIPT_RETENTION":       "",
	} {
		t.Setenv(key, value)
	}
}
