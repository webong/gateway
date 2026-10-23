package reverb

import "testing"

func TestLoadConfigOwnsOnlyReverbWorkloadSettings(t *testing.T) {
	t.Setenv("GATEWAY_REVERB_ENABLED", "true")
	t.Setenv("GATEWAY_REVERB_DOCKER_IMAGE", "gateway-reverb:latest")
	t.Setenv("GATEWAY_REVERB_KUBERNETES_IMAGE", "registry.example.test/gateway-reverb:v1")

	config := LoadConfig()
	if !config.Enabled || config.ArtisanPath != "artisan" {
		t.Fatalf("unexpected Reverb configuration: %+v", config)
	}
	if config.DockerImage != "gateway-reverb:latest" || config.DockerContainerPort != 8080 {
		t.Fatalf("unexpected Reverb Docker configuration: %+v", config)
	}
	if config.KubernetesImage != "registry.example.test/gateway-reverb:v1" || config.KubernetesContainerPort != 8080 {
		t.Fatalf("unexpected Reverb Kubernetes configuration: %+v", config)
	}
}
