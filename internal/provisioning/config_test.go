package provisioning

import "testing"

func TestLoadConfigOwnsGenericDriverSettings(t *testing.T) {
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "true")
	t.Setenv("GATEWAY_PROVISIONING_DOCKER_NETWORK", "gateway")
	t.Setenv("GATEWAY_PROVISIONING_KUBERNETES_NAMESPACE", "managed-servers")

	config, err := LoadConfig("http")
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || config.DockerNetwork != "gateway" || config.KubernetesNamespace != "managed-servers" {
		t.Fatalf("unexpected provisioning config: %+v", config)
	}
}

func TestLoadConfigRequiresHTTPRuntimeWhenEnabled(t *testing.T) {
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "true")
	if _, err := LoadConfig("standalone"); err == nil {
		t.Fatal("expected enabled provisioning to require HTTP runtime")
	}
}
