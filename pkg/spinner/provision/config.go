package provision

import (
	"fmt"
	"os"
	"path/filepath"
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
			StateDirectory:                 env("GATEWAY_PROVISIONING_STATE_DIR", defaultStateDirectory()),
			SupervisorBinary:               env("GATEWAY_PROVISIONING_SUPERVISOR_BINARY", defaultSupervisorBinary()),
			LegacyDrained:                  envBool("GATEWAY_PROVISIONING_LEGACY_DRAINED", false),
			TargetID:                       env("GATEWAY_PROVISIONING_TARGET_ID", ""),
			TargetToken:                    env("GATEWAY_PROVISIONING_TARGET_TOKEN", ""),
			CoordinationLease:              envSeconds("GATEWAY_PROVISIONING_COORDINATION_LEASE_SECONDS", 15*time.Second),
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
		return HostConfig{}, fmt.Errorf("GATEWAY_PROVISIONING_ENABLED currently requires an external Planner HTTP connection")
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

func envSeconds(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return defaultValue
}

func defaultStateDirectory() string {
	if root := os.Getenv("GATEWAY_ROUTER_DATA_DIR"); root != "" {
		return filepath.Join(root, "workload")
	}
	if root := os.Getenv("GATEWAY_DATA_DIR"); root != "" {
		return filepath.Join(root, "workload")
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(root, "gateway", "workload")
}
func defaultSupervisorBinary() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(executable), "gateway-workload-supervisor")
}
