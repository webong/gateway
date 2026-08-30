package provisioning

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DockerDriverConfig struct {
	Binary          string
	NodeID          string
	Network         string
	EnvironmentFile string
	PublishHost     string
	RouteHost       string
	StartTimeout    time.Duration
	StopTimeout     time.Duration
}

type DockerDriver struct {
	config DockerDriverConfig
	runner commandRunner
}

func NewDockerDriver(config DockerDriverConfig) (*DockerDriver, error) {
	return newDockerDriver(config, execCommandRunner{})
}

func newDockerDriver(config DockerDriverConfig, runner commandRunner) (*DockerDriver, error) {
	if strings.TrimSpace(config.Binary) == "" {
		return nil, fmt.Errorf("provisioning Docker binary is required")
	}
	if strings.TrimSpace(config.NodeID) == "" {
		return nil, fmt.Errorf("provisioning Docker node ID is required")
	}
	if strings.TrimSpace(config.PublishHost) == "" {
		return nil, fmt.Errorf("provisioning Docker publish host is required")
	}
	if config.StartTimeout <= 0 || config.StopTimeout <= 0 {
		return nil, fmt.Errorf("provisioning Docker timeouts must be positive")
	}
	if runner == nil {
		return nil, fmt.Errorf("provisioning Docker command runner is required")
	}

	return &DockerDriver{config: config, runner: runner}, nil
}

func (d *DockerDriver) Prepare(ctx context.Context) error {
	containers, err := d.runner.Output(ctx, nil, d.config.Binary,
		"ps", "--all", "--quiet",
		"--filter", "label=gateway.provisioning.node="+runtimeLabelValue(d.config.NodeID),
	)
	if err != nil {
		return err
	}
	ids := strings.Fields(containers)
	if len(ids) == 0 {
		return nil
	}
	arguments := append([]string{"rm", "--force"}, ids...)
	_, err = d.runner.Output(ctx, nil, d.config.Binary, arguments...)

	return err
}

func (d *DockerDriver) Start(ctx context.Context, spec ServerSpec, instanceID string, workload Workload) (Process, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if spec.Driver != DriverDocker {
		return nil, fmt.Errorf("Docker provisioning driver cannot start %q runtime", spec.Driver)
	}
	launch, err := workload.Launch(spec, DriverDocker, "0.0.0.0", 0)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(launch.Image) == "" || launch.Port < 1 || launch.Port > 65535 {
		return nil, fmt.Errorf("Docker workload image and container port are required")
	}

	name := runtimeResourceName(instanceID)
	containerID, err := d.runner.Output(ctx, nil, d.config.Binary, dockerRunArguments(d.config, spec, instanceID, name, launch)...)
	if err != nil {
		return nil, fmt.Errorf("start provisioned Docker container: %w", err)
	}
	containerID = dockerContainerID(containerID)
	if containerID == "" {
		return nil, fmt.Errorf("start provisioned Docker container: Docker returned an empty container ID")
	}

	inspectFormat := fmt.Sprintf(`{{.State.Pid}} {{(index (index .NetworkSettings.Ports %q) 0).HostIp}} {{(index (index .NetworkSettings.Ports %q) 0).HostPort}}`, strconv.Itoa(launch.Port)+"/tcp", strconv.Itoa(launch.Port)+"/tcp")
	inspection, err := d.runner.Output(ctx, nil, d.config.Binary, "inspect", "--format", inspectFormat, containerID)
	if err != nil {
		d.remove(containerID)
		return nil, fmt.Errorf("inspect provisioned Docker container: %w", err)
	}
	fields := strings.Fields(inspection)
	if len(fields) != 3 {
		d.remove(containerID)
		return nil, fmt.Errorf("inspect provisioned Docker container: unexpected endpoint %q", inspection)
	}
	pid, pidErr := strconv.Atoi(fields[0])
	port, portErr := strconv.Atoi(fields[2])
	if pidErr != nil || portErr != nil || port < 1 || port > 65535 {
		d.remove(containerID)
		return nil, fmt.Errorf("inspect provisioned Docker container: invalid endpoint %q", inspection)
	}
	host := dockerRouteHost(d.config.RouteHost, fields[1])
	process := &dockerProcess{
		runner:      d.runner,
		binary:      d.config.Binary,
		containerID: containerID,
		host:        host,
		port:        port,
		pid:         pid,
		stopTimeout: d.config.StopTimeout,
		done:        make(chan struct{}),
	}
	go process.watch()

	startupContext, cancel := context.WithTimeout(ctx, d.config.StartTimeout)
	defer cancel()
	if err := waitForListener(startupContext, process); err != nil {
		stopContext, stopCancel := context.WithTimeout(context.Background(), d.config.StopTimeout)
		_ = process.Stop(stopContext)
		stopCancel()
		return nil, fmt.Errorf("wait for provisioned Docker listener: %w", err)
	}

	return process, nil
}

func dockerContainerID(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if id := strings.TrimSpace(lines[index]); id != "" {
			return id
		}
	}

	return ""
}

func dockerRunArguments(config DockerDriverConfig, spec ServerSpec, instanceID, name string, launch LaunchSpec) []string {
	arguments := []string{
		"run", "--detach", "--rm",
		"--name", name,
		"--label", "gateway.provisioning.type=" + runtimeLabelValue(spec.Type),
		"--label", "gateway.provisioning.server=" + spec.ID,
		"--label", "gateway.provisioning.instance=" + instanceID,
		"--label", "gateway.provisioning.node=" + runtimeLabelValue(config.NodeID),
		"--publish", config.PublishHost + "::" + strconv.Itoa(launch.Port),
	}
	if config.Network != "" {
		arguments = append(arguments, "--network", config.Network)
	}
	if config.EnvironmentFile != "" {
		arguments = append(arguments, "--env-file", config.EnvironmentFile)
	}
	for _, variable := range launch.Environment {
		arguments = append(arguments, "--env", variable)
	}
	arguments = append(arguments,
		launch.Image,
		launch.Executable,
	)
	arguments = append(arguments, launch.Arguments...)

	return arguments
}

func dockerRouteHost(configured, published string) string {
	if configured != "" {
		return configured
	}
	if published == "0.0.0.0" || published == "::" || published == "" {
		return "127.0.0.1"
	}

	return published
}

func (d *DockerDriver) remove(containerID string) {
	ctx, cancel := context.WithTimeout(context.Background(), d.config.StopTimeout)
	defer cancel()
	_, _ = d.runner.Output(ctx, nil, d.config.Binary, "rm", "--force", containerID)
}

type dockerProcess struct {
	runner      commandRunner
	binary      string
	containerID string
	host        string
	port        int
	pid         int
	stopTimeout time.Duration
	done        chan struct{}
	doneOnce    sync.Once
	errMu       sync.RWMutex
	err         error
}

func (p *dockerProcess) watch() {
	output, err := p.runner.Output(context.Background(), nil, p.binary, "wait", p.containerID)
	if err == nil && strings.TrimSpace(output) != "0" {
		err = fmt.Errorf("provisioned Docker container exited with status %s", strings.TrimSpace(output))
	}
	p.finish(err)
}

func (p *dockerProcess) finish(err error) {
	p.doneOnce.Do(func() {
		p.errMu.Lock()
		p.err = err
		p.errMu.Unlock()
		close(p.done)
	})
}

func (p *dockerProcess) PID() int              { return p.pid }
func (p *dockerProcess) Host() string          { return p.host }
func (p *dockerProcess) Port() int             { return p.port }
func (p *dockerProcess) Done() <-chan struct{} { return p.done }
func (p *dockerProcess) Err() error {
	p.errMu.RLock()
	defer p.errMu.RUnlock()
	return p.err
}

func (p *dockerProcess) Stop(ctx context.Context) error {
	seconds := int(math.Ceil(p.stopTimeout.Seconds()))
	_, err := p.runner.Output(ctx, nil, p.binary, "stop", "--time", strconv.Itoa(seconds), p.containerID)
	if err != nil {
		return err
	}
	p.finish(nil)

	return nil
}

var _ Driver = (*DockerDriver)(nil)
var _ Process = (*dockerProcess)(nil)
