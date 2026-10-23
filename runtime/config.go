package mercure

import (
	"os"
	"strconv"
)

type Config struct {
	Enabled                 bool
	Binary                  string
	WorkingDirectory        string
	ConfigPath              string
	DockerImage             string
	DockerBinary            string
	DockerConfigPath        string
	DockerContainerPort     int
	KubernetesImage         string
	KubernetesBinary        string
	KubernetesConfigPath    string
	KubernetesContainerPort int
}

func LoadConfig() Config {
	return Config{
		Enabled:                 envBool("GATEWAY_MERCURE_ENABLED", false),
		Binary:                  env("GATEWAY_MERCURE_BINARY", "mercure"),
		WorkingDirectory:        env("GATEWAY_MERCURE_WORKING_DIRECTORY", "."),
		ConfigPath:              env("GATEWAY_MERCURE_CONFIG", ""),
		DockerImage:             env("GATEWAY_MERCURE_DOCKER_IMAGE", ""),
		DockerBinary:            env("GATEWAY_MERCURE_DOCKER_BINARY", "caddy"),
		DockerConfigPath:        env("GATEWAY_MERCURE_DOCKER_CONFIG", "/etc/caddy/Caddyfile"),
		DockerContainerPort:     envInt("GATEWAY_MERCURE_DOCKER_CONTAINER_PORT", 80),
		KubernetesImage:         env("GATEWAY_MERCURE_KUBERNETES_IMAGE", ""),
		KubernetesBinary:        env("GATEWAY_MERCURE_KUBERNETES_BINARY", "caddy"),
		KubernetesConfigPath:    env("GATEWAY_MERCURE_KUBERNETES_CONFIG", "/etc/caddy/Caddyfile"),
		KubernetesContainerPort: envInt("GATEWAY_MERCURE_KUBERNETES_CONTAINER_PORT", 80),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}
