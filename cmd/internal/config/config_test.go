package config

import "testing"

func TestLoadConfigSelectsHTTPRuntime(t *testing.T) {
	setConfigEnv(t, "http")
	t.Setenv("LARAVEL_BACKEND_URL", "http://127.0.0.1:8000")
	t.Setenv("WEB_RELAY_INTERNAL_TOKEN", "web-token")
	t.Setenv("ROADRUNNER_INTERNAL_TOKEN", "legacy-token")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Runtime != "http" || config.RoadRunnerEnabled || config.LaravelBackendURL != "http://127.0.0.1:8000" {
		t.Fatalf("unexpected HTTP runtime config: %+v", config)
	}
	if config.InternalToken != "web-token" {
		t.Fatalf("expected transport-neutral token, got %q", config.InternalToken)
	}
}
func TestLoadConfigRetainsLegacyRoadRunnerSelector(t *testing.T) {
	setConfigEnv(t, "")
	t.Setenv("ROADRUNNER_ENABLED", "true")
	t.Setenv("ROADRUNNER_INTERNAL_TOKEN", "legacy-token")

	config, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Runtime != "roadrunner" || !config.RoadRunnerEnabled || config.InternalToken != "legacy-token" {
		t.Fatalf("unexpected legacy RoadRunner config: %+v", config)
	}
}

func setConfigEnv(t *testing.T, runtime string) {
	t.Helper()
	t.Setenv("WEB_RELAY_RUNTIME", runtime)
	t.Setenv("ROADRUNNER_ENABLED", "")
	t.Setenv("WEB_RELAY_INTERNAL_TOKEN", "")
	t.Setenv("ROADRUNNER_INTERNAL_TOKEN", "")
	t.Setenv("LARAVEL_BACKEND_URL", "")
}
