package centrifugo

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/webong/gateway/src/spinner/provision"
)

type serverConfiguration struct {
	ClientTokenHMACSecretKey       string   `json:"client_token_hmac_secret_key"`
	ClientAllowedOrigins           []string `json:"client_allowed_origins"`
	ClientInsecure                 bool     `json:"client_insecure"`
	ChannelAllowSubscribeForClient bool     `json:"channel_without_namespace_allow_subscribe_for_client"`
	HTTPAPIKey                     string   `json:"http_api_key"`
	EngineType                     string   `json:"engine_type"`
	EngineRedisAddress             *string  `json:"engine_redis_address"`
	PrometheusEnabled              bool     `json:"prometheus_enabled"`
	LogLevel                       string   `json:"log_level"`
}

type Workload struct {
	config Config
}

func NewWorkload(config Config) *Workload { return &Workload{config: config} }
func (*Workload) Type() string            { return "centrifugo" }

func (w *Workload) Supports(driver string) bool {
	switch driver {
	case provision.DriverLocal:
		return strings.TrimSpace(w.config.Binary) != ""
	case provision.DriverDocker:
		return strings.TrimSpace(w.config.DockerImage) != ""
	case provision.DriverKubernetes:
		return strings.TrimSpace(w.config.KubernetesImage) != ""
	default:
		return false
	}
}

func (w *Workload) Validate(spec provision.ServerSpec) error {
	if spec.Type != w.Type() {
		return fmt.Errorf("Centrifugo workload cannot provision type %q", spec.Type)
	}
	config, err := decodeConfiguration(spec)
	if err != nil {
		return err
	}
	if strings.TrimSpace(config.ClientTokenHMACSecretKey) == "" || strings.TrimSpace(config.HTTPAPIKey) == "" {
		return fmt.Errorf("Centrifugo server %q client token secret and HTTP API key are required", spec.ID)
	}
	if config.EngineType != "memory" && config.EngineType != "redis" {
		return fmt.Errorf("Centrifugo server %q uses unsupported engine %q", spec.ID, config.EngineType)
	}
	if config.EngineType == "redis" && (config.EngineRedisAddress == nil || strings.TrimSpace(*config.EngineRedisAddress) == "") {
		return fmt.Errorf("Centrifugo server %q Redis address is required", spec.ID)
	}
	if spec.Replicas > 1 && config.EngineType != "redis" {
		return fmt.Errorf("Centrifugo server %q multiple replicas require the Redis engine", spec.ID)
	}
	if !w.Supports(spec.Driver) {
		return fmt.Errorf("Centrifugo %s driver is not configured on this Gateway node", spec.Driver)
	}
	return nil
}

func (w *Workload) Launch(spec provision.ServerSpec, driver, host string, port int) (provision.LaunchSpec, error) {
	configuration, err := decodeConfiguration(spec)
	if err != nil {
		return provision.LaunchSpec{}, err
	}

	launch := provision.LaunchSpec{ContainerName: "centrifugo"}
	switch driver {
	case provision.DriverLocal:
		launch.Executable = w.config.Binary
		launch.WorkingDirectory = w.config.WorkingDirectory
		launch.Port = port
	case provision.DriverDocker:
		launch.Image = w.config.DockerImage
		launch.Executable = w.config.DockerBinary
		launch.Port = w.config.DockerContainerPort
		host = "0.0.0.0"
	case provision.DriverKubernetes:
		launch.Image = w.config.KubernetesImage
		launch.Executable = w.config.KubernetesBinary
		launch.Port = w.config.KubernetesContainerPort
		host = "0.0.0.0"
	default:
		return provision.LaunchSpec{}, fmt.Errorf("unsupported Centrifugo runtime %q", driver)
	}
	launch.Environment = centrifugoEnvironment(configuration, host, launch.Port)

	return launch, nil
}

func decodeConfiguration(spec provision.ServerSpec) (serverConfiguration, error) {
	var configuration serverConfiguration
	if err := json.Unmarshal(spec.Configuration, &configuration); err != nil {
		return configuration, fmt.Errorf("decode Centrifugo server %q configuration: %w", spec.ID, err)
	}
	return configuration, nil
}

func centrifugoEnvironment(config serverConfiguration, host string, port int) []string {
	environment := []string{
		"CENTRIFUGO_HTTP_SERVER_ADDRESS=" + host,
		"CENTRIFUGO_HTTP_SERVER_PORT=" + strconv.Itoa(port),
		"CENTRIFUGO_CLIENT_TOKEN_HMAC_SECRET_KEY=" + config.ClientTokenHMACSecretKey,
		"CENTRIFUGO_CLIENT_ALLOWED_ORIGINS=" + strings.Join(config.ClientAllowedOrigins, " "),
		"CENTRIFUGO_CLIENT_INSECURE=" + strconv.FormatBool(config.ClientInsecure),
		"CENTRIFUGO_CHANNEL_WITHOUT_NAMESPACE_ALLOW_SUBSCRIBE_FOR_CLIENT=" + strconv.FormatBool(config.ChannelAllowSubscribeForClient),
		"CENTRIFUGO_HTTP_API_KEY=" + config.HTTPAPIKey,
		"CENTRIFUGO_ENGINE_TYPE=" + config.EngineType,
		"CENTRIFUGO_PROMETHEUS_ENABLED=" + strconv.FormatBool(config.PrometheusEnabled),
		"CENTRIFUGO_HEALTH_ENABLED=true",
		"CENTRIFUGO_LOG_LEVEL=" + config.LogLevel,
	}
	if config.EngineRedisAddress != nil {
		environment = append(environment, "CENTRIFUGO_ENGINE_REDIS_ADDRESS="+*config.EngineRedisAddress)
	}
	return environment
}

var _ provision.Workload = (*Workload)(nil)
