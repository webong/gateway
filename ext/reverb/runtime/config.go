package reverb

import (
	"os"
	"strconv"
)

// Config contains only settings needed to launch the Reverb workload. Generic
// reconciliation and driver settings belong to provisioning.HostConfig.
type Config struct {
	Enabled                 bool
	PHPBinary               string
	ArtisanPath             string
	WorkingDirectory        string
	DockerImage             string
	DockerPHPBinary         string
	DockerArtisanPath       string
	DockerContainerPort     int
	KubernetesImage         string
	KubernetesPHPBinary     string
	KubernetesArtisanPath   string
	KubernetesContainerPort int
}

func LoadConfig() Config {
	return Config{
		Enabled:                 envBool("GATEWAY_REVERB_ENABLED", false),
		PHPBinary:               env("GATEWAY_REVERB_PHP_BINARY", "php"),
		ArtisanPath:             env("GATEWAY_REVERB_ARTISAN", "artisan"),
		WorkingDirectory:        env("GATEWAY_REVERB_WORKING_DIRECTORY", "."),
		DockerImage:             env("GATEWAY_REVERB_DOCKER_IMAGE", ""),
		DockerPHPBinary:         env("GATEWAY_REVERB_DOCKER_PHP_BINARY", "php"),
		DockerArtisanPath:       env("GATEWAY_REVERB_DOCKER_ARTISAN", "artisan"),
		DockerContainerPort:     envInt("GATEWAY_REVERB_DOCKER_CONTAINER_PORT", 8080),
		KubernetesImage:         env("GATEWAY_REVERB_KUBERNETES_IMAGE", ""),
		KubernetesPHPBinary:     env("GATEWAY_REVERB_KUBERNETES_PHP_BINARY", "php"),
		KubernetesArtisanPath:   env("GATEWAY_REVERB_KUBERNETES_ARTISAN", "artisan"),
		KubernetesContainerPort: envInt("GATEWAY_REVERB_KUBERNETES_CONTAINER_PORT", 8080),
	}
}

func env(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func envBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func envInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}
