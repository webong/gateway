package provisioning

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type HostConfig struct {
	Enabled bool
	Config
}

func LoadConfig(gatewayRuntime string) (HostConfig, error) {
	config := HostConfig{
		Enabled: envBool("GATEWAY_PROVISIONING_ENABLED", false),
		Config: Config{
			PollInterval:                   envDuration("GATEWAY_PROVISIONING_POLL_INTERVAL", 5*time.Second),
			StartTimeout:                   envDuration("GATEWAY_PROVISIONING_START_TIMEOUT", 15*time.Second),
			StopTimeout:                    envDuration("GATEWAY_PROVISIONING_STOP_TIMEOUT", 15*time.Second),
			NodeID:                         env("GATEWAY_NODE_ID", defaultNodeID()),
			DockerBinary:                   env("GATEWAY_PROVISIONING_DOCKER_BINARY", "docker"),
			DockerNetwork:                  env("GATEWAY_PROVISIONING_DOCKER_NETWORK", ""),
			DockerEnvironmentFile:          env("GATEWAY_PROVISIONING_DOCKER_ENV_FILE", ""),
			DockerPublishHost:              env("GATEWAY_PROVISIONING_DOCKER_PUBLISH_HOST", "127.0.0.1"),
			DockerRouteHost:                env("GATEWAY_PROVISIONING_DOCKER_ROUTE_HOST", ""),
			KubernetesKubectl:              env("GATEWAY_PROVISIONING_KUBECTL_BINARY", "kubectl"),
			KubernetesNamespace:            env("GATEWAY_PROVISIONING_KUBERNETES_NAMESPACE", "default"),
			KubernetesServiceAccount:       env("GATEWAY_PROVISIONING_KUBERNETES_SERVICE_ACCOUNT", ""),
			KubernetesEnvironmentSecret:    env("GATEWAY_PROVISIONING_KUBERNETES_ENV_SECRET", ""),
			KubernetesEnvironmentConfigMap: env("GATEWAY_PROVISIONING_KUBERNETES_ENV_CONFIG_MAP", ""),
			KubernetesImagePullPolicy:      env("GATEWAY_PROVISIONING_KUBERNETES_IMAGE_PULL_POLICY", "IfNotPresent"),
			KubernetesRouteMode:            env("GATEWAY_PROVISIONING_KUBERNETES_ROUTE_MODE", "pod-ip"),
		},
	}
	if config.Enabled && gatewayRuntime != "http" {
		return HostConfig{}, fmt.Errorf("GATEWAY_PROVISIONING_ENABLED currently requires GATEWAY_RUNTIME=http")
	}

	return config, nil
}

func defaultNodeID() string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		return "gateway-local"
	}

	return hostname
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

func envDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}
