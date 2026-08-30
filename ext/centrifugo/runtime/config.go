package centrifugo

import (
	"os"
	"strconv"
)

type Config struct {
	Enabled                 bool
	Binary                  string
	WorkingDirectory        string
	DockerImage             string
	DockerBinary            string
	DockerContainerPort     int
	KubernetesImage         string
	KubernetesBinary        string
	KubernetesContainerPort int
}

func LoadConfig() Config {
	return Config{
		Enabled:                 envBool("GATEWAY_CENTRIFUGO_ENABLED", false),
		Binary:                  env("GATEWAY_CENTRIFUGO_BINARY", "centrifugo"),
		WorkingDirectory:        env("GATEWAY_CENTRIFUGO_WORKING_DIRECTORY", "."),
		DockerImage:             env("GATEWAY_CENTRIFUGO_DOCKER_IMAGE", ""),
		DockerBinary:            env("GATEWAY_CENTRIFUGO_DOCKER_BINARY", "centrifugo"),
		DockerContainerPort:     envInt("GATEWAY_CENTRIFUGO_DOCKER_CONTAINER_PORT", 8000),
		KubernetesImage:         env("GATEWAY_CENTRIFUGO_KUBERNETES_IMAGE", ""),
		KubernetesBinary:        env("GATEWAY_CENTRIFUGO_KUBERNETES_BINARY", "centrifugo"),
		KubernetesContainerPort: envInt("GATEWAY_CENTRIFUGO_KUBERNETES_CONTAINER_PORT", 8000),
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
