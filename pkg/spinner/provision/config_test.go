package provision

import "testing"

func TestLoadConfigOwnsGenericDriverSettings(t *testing.T) {
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "true")
	t.Setenv("GATEWAY_PROVISIONING_DOCKER_NETWORK", "gateway")
	t.Setenv("GATEWAY_PROVISIONING_KUBERNETES_NAMESPACE", "managed-servers")
	t.Setenv("GATEWAY_PROVISIONING_TARGET_ID", "target-a")
	t.Setenv("GATEWAY_PROVISIONING_TARGET_TOKEN", "test-only-token")

	config, err := LoadConfig("http")
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || config.DockerNetwork != "gateway" || config.KubernetesNamespace != "managed-servers" {
		t.Fatalf("unexpected provisioning config: %+v", config)
	}
	if config.TargetID != "target-a" || config.TargetToken != "test-only-token" {
		t.Fatal("missing target configuration")
	}
}

func TestLoadConfigRequiresHTTPRuntimeWhenEnabled(t *testing.T) {
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "true")
	if _, err := LoadConfig("diagnostic"); err == nil {
		t.Fatal("expected enabled provisioning to require HTTP runtime")
	}
}
