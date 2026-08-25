package config

import "testing"

func TestLoadConfigSelectsHTTPRuntime(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("NET_GATEWAY_LARAVEL_BACKEND_URL", "http://127.0.0.1:8000")
	t.Setenv("NET_GATEWAY_INTERNAL_TOKEN", "gateway-token")

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
	t.Setenv("NET_GATEWAY_RUNTIME", runtime)
	t.Setenv("NET_GATEWAY_INTERNAL_TOKEN", "")
	t.Setenv("NET_GATEWAY_LARAVEL_BACKEND_URL", "")
}
