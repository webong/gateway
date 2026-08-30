package mercure

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/webong/gateway/internal/provisioning"
)

type serverConfiguration struct {
	PublisherJWTKey        string   `json:"publisher_jwt_key"`
	PublisherJWTAlgorithm  string   `json:"publisher_jwt_algorithm"`
	SubscriberJWTKey       string   `json:"subscriber_jwt_key"`
	SubscriberJWTAlgorithm string   `json:"subscriber_jwt_algorithm"`
	Anonymous              bool     `json:"anonymous"`
	CORSOrigins            []string `json:"cors_origins"`
	PublishOrigins         []string `json:"publish_origins"`
	Subscriptions          bool     `json:"subscriptions"`
	Heartbeat              string   `json:"heartbeat"`
	Transport              string   `json:"transport"`
}

type Workload struct {
	config Config
}

func NewWorkload(config Config) *Workload {
	return &Workload{config: config}
}

func (*Workload) Type() string { return "mercure" }

func (w *Workload) Supports(driver string) bool {
	switch driver {
	case provisioning.DriverLocal:
		return strings.TrimSpace(w.config.Binary) != ""
	case provisioning.DriverDocker:
		return strings.TrimSpace(w.config.DockerImage) != ""
	case provisioning.DriverKubernetes:
		return strings.TrimSpace(w.config.KubernetesImage) != ""
	default:
		return false
	}
}

func (w *Workload) Validate(spec provisioning.ServerSpec) error {
	if spec.Type != w.Type() {
		return fmt.Errorf("Mercure workload cannot provision type %q", spec.Type)
	}
	config, err := decodeConfiguration(spec)
	if err != nil {
		return err
	}
	if spec.Replicas != 1 {
		return fmt.Errorf("Mercure Community servers require exactly one replica")
	}
	if strings.TrimSpace(config.PublisherJWTKey) == "" || strings.TrimSpace(config.SubscriberJWTKey) == "" {
		return fmt.Errorf("Mercure server %q publisher and subscriber JWT keys are required", spec.ID)
	}
	if !validAlgorithm(config.PublisherJWTAlgorithm) || !validAlgorithm(config.SubscriberJWTAlgorithm) {
		return fmt.Errorf("Mercure server %q uses an unsupported JWT algorithm", spec.ID)
	}
	if !w.Supports(spec.Driver) {
		return fmt.Errorf("Mercure %s driver is not configured on this Gateway node", spec.Driver)
	}

	return nil
}

func (w *Workload) Launch(spec provisioning.ServerSpec, driver, host string, port int) (provisioning.LaunchSpec, error) {
	configuration, err := decodeConfiguration(spec)
	if err != nil {
		return provisioning.LaunchSpec{}, err
	}

	launch := provisioning.LaunchSpec{ContainerName: "mercure"}
	switch driver {
	case provisioning.DriverLocal:
		launch.Executable = w.config.Binary
		launch.WorkingDirectory = w.config.WorkingDirectory
		launch.Port = port
		launch.Arguments = runArguments(w.config.ConfigPath)
		launch.Environment = mercureEnvironment(configuration, "http://"+net.JoinHostPort(host, strconv.Itoa(port)))
	case provisioning.DriverDocker:
		launch.Image = w.config.DockerImage
		launch.Executable = w.config.DockerBinary
		launch.Port = w.config.DockerContainerPort
		launch.Arguments = runArguments(w.config.DockerConfigPath)
		launch.Environment = mercureEnvironment(configuration, ":"+strconv.Itoa(launch.Port))
	case provisioning.DriverKubernetes:
		launch.Image = w.config.KubernetesImage
		launch.Executable = w.config.KubernetesBinary
		launch.Port = w.config.KubernetesContainerPort
		launch.Arguments = runArguments(w.config.KubernetesConfigPath)
		launch.Environment = mercureEnvironment(configuration, ":"+strconv.Itoa(launch.Port))
	default:
		return provisioning.LaunchSpec{}, fmt.Errorf("unsupported Mercure runtime %q", driver)
	}

	return launch, nil
}

func decodeConfiguration(spec provisioning.ServerSpec) (serverConfiguration, error) {
	var configuration serverConfiguration
	if err := json.Unmarshal(spec.Configuration, &configuration); err != nil {
		return configuration, fmt.Errorf("decode Mercure server %q configuration: %w", spec.ID, err)
	}
	return configuration, nil
}

func runArguments(configPath string) []string {
	arguments := []string{"run"}
	if strings.TrimSpace(configPath) != "" {
		arguments = append(arguments, "--config", configPath, "--adapter", "caddyfile")
	}
	return arguments
}

func mercureEnvironment(config serverConfiguration, serverName string) []string {
	return []string{
		"SERVER_NAME=" + serverName,
		"MERCURE_PUBLISHER_JWT_KEY=" + config.PublisherJWTKey,
		"MERCURE_PUBLISHER_JWT_ALG=" + config.PublisherJWTAlgorithm,
		"MERCURE_SUBSCRIBER_JWT_KEY=" + config.SubscriberJWTKey,
		"MERCURE_SUBSCRIBER_JWT_ALG=" + config.SubscriberJWTAlgorithm,
		"MERCURE_EXTRA_DIRECTIVES=" + extraDirectives(config),
	}
}

func extraDirectives(config serverConfiguration) string {
	directives := make([]string, 0, 6)
	if config.Transport != "" {
		directives = append(directives, "transport "+config.Transport)
	}
	if config.Anonymous {
		directives = append(directives, "anonymous")
	}
	if len(config.CORSOrigins) > 0 {
		directives = append(directives, "cors_origins "+strings.Join(config.CORSOrigins, " "))
	}
	if len(config.PublishOrigins) > 0 {
		directives = append(directives, "publish_origins "+strings.Join(config.PublishOrigins, " "))
	}
	if config.Subscriptions {
		directives = append(directives, "subscriptions")
	}
	if config.Heartbeat != "" {
		directives = append(directives, "heartbeat "+config.Heartbeat)
	}
	return strings.Join(directives, "\n")
}

func validAlgorithm(value string) bool {
	switch value {
	case "HS256", "HS384", "HS512", "RS256", "RS384", "RS512":
		return true
	default:
		return false
	}
}

var _ provisioning.Workload = (*Workload)(nil)
