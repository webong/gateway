package reverb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/webong/gateway/internal/provisioning"
)

type RedisServerSpec struct {
	URL      *string `json:"url"`
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	Database *int    `json:"database"`
	Timeout  *int    `json:"timeout"`
}

type serverConfiguration struct {
	MaxRequestSize          int64           `json:"max_request_size"`
	ScalingEnabled          bool            `json:"scaling_enabled"`
	ScalingChannel          string          `json:"scaling_channel"`
	ScalingServer           RedisServerSpec `json:"scaling_server"`
	PulseIngestInterval     int             `json:"pulse_ingest_interval"`
	TelescopeIngestInterval int             `json:"telescope_ingest_interval"`
}

type Workload struct {
	config Config
}

func NewWorkload(config Config) *Workload {
	return &Workload{config: config}
}

func (*Workload) Type() string {
	return "reverb"
}

func (w *Workload) Supports(driver string) bool {
	switch driver {
	case provisioning.DriverLocal:
		return true
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
		return fmt.Errorf("Reverb workload cannot provision type %q", spec.Type)
	}
	config, err := decodeConfiguration(spec)
	if err != nil {
		return err
	}
	if config.MaxRequestSize < 1 {
		return fmt.Errorf("Reverb server %q maximum request size must be positive", spec.ID)
	}
	if config.ScalingEnabled && strings.TrimSpace(config.ScalingChannel) == "" {
		return fmt.Errorf("Reverb server %q scaling channel is required", spec.ID)
	}
	if config.PulseIngestInterval < 1 || config.TelescopeIngestInterval < 1 {
		return fmt.Errorf("Reverb server %q observability intervals must be positive", spec.ID)
	}
	if !w.Supports(spec.Driver) {
		return fmt.Errorf("Reverb %s driver is not configured on this Gateway node", spec.Driver)
	}

	return nil
}

func (w *Workload) Launch(spec provisioning.ServerSpec, driver, host string, port int) (provisioning.LaunchSpec, error) {
	config, err := decodeConfiguration(spec)
	if err != nil {
		return provisioning.LaunchSpec{}, err
	}

	launch := provisioning.LaunchSpec{
		Environment:   reverbEnvironment(spec, config),
		ContainerName: "reverb",
	}
	switch driver {
	case provisioning.DriverLocal:
		launch.Executable = w.config.PHPBinary
		launch.WorkingDirectory = w.config.WorkingDirectory
		launch.Port = port
		launch.Arguments = reverbArguments(w.config.ArtisanPath, spec, host, port)
	case provisioning.DriverDocker:
		launch.Image = w.config.DockerImage
		launch.Executable = w.config.DockerPHPBinary
		launch.Port = w.config.DockerContainerPort
		launch.Arguments = reverbArguments(w.config.DockerArtisanPath, spec, "0.0.0.0", launch.Port)
	case provisioning.DriverKubernetes:
		launch.Image = w.config.KubernetesImage
		launch.Executable = w.config.KubernetesPHPBinary
		launch.Port = w.config.KubernetesContainerPort
		launch.Arguments = reverbArguments(w.config.KubernetesArtisanPath, spec, "0.0.0.0", launch.Port)
	default:
		return provisioning.LaunchSpec{}, fmt.Errorf("unsupported Reverb runtime %q", driver)
	}

	return launch, nil
}

func decodeConfiguration(spec provisioning.ServerSpec) (serverConfiguration, error) {
	var config serverConfiguration
	if err := json.Unmarshal(spec.Configuration, &config); err != nil {
		return config, fmt.Errorf("decode Reverb server %q configuration: %w", spec.ID, err)
	}

	return config, nil
}

func reverbArguments(artisan string, spec provisioning.ServerSpec, host string, port int) []string {
	arguments := []string{
		artisan,
		"reverb:start",
		"--host=" + host,
		"--port=" + strconv.Itoa(port),
		"--hostname=" + spec.Hostname,
	}
	if spec.Path != "" {
		arguments = append(arguments, "--path="+spec.Path)
	}

	return arguments
}

var _ provisioning.Workload = (*Workload)(nil)
