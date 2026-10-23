package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

type DockerDriverConfig struct {
	TargetID        string
	Context         string
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

	if config.Context != "" {
		runner = prefixedCommandRunner{runner: runner, arguments: []string{"--context", config.Context}}
	}
	return &DockerDriver{config: config, runner: runner}, nil
}

func (d *DockerDriver) PrepareDesired(ctx context.Context, desired map[string]ServerSpec) error {
	args := []string{
		"ps", "--all", "--quiet",
		"--filter", "label=gateway.provisioning.node=" + runtimeLabelValue(d.config.NodeID),
	}
	if d.config.TargetID != "" {
		args = append(args, "--filter", "label=gateway.provisioning.target="+d.config.TargetID)
	}
	containers, err := d.runner.Output(ctx, nil, d.config.Binary, args...)
	if err != nil {
		return err
	}
	for _, id := range strings.Fields(containers) {
		resource, inspectErr := d.inspect(ctx, id)
		if inspectErr != nil {
			return fmt.Errorf("inspect owned Docker container %s: %w", id, inspectErr)
		}
		if !d.owns(resource.Config.Labels) {
			continue
		}
		instanceID := resource.Config.Labels["gateway.provisioning.instance"]
		if spec, ok := desired[instanceID]; ok {
			if resource.Name != "/"+runtimeResourceName(instanceID) ||
				resource.Config.Labels["gateway.provisioning.server"] != spec.ID ||
				resource.Config.Labels["gateway.provisioning.type"] != runtimeLabelValue(spec.Type) {
				return fmt.Errorf("Docker container %s conflicts with desired assignment %s", id, instanceID)
			}
			if resource.State.Running {
				continue
			}
		}
		if _, err := d.runner.Output(ctx, nil, d.config.Binary, "rm", "--force", id); err != nil {
			return fmt.Errorf("remove stale Docker container %s: %w", id, err)
		}
	}
	return nil
}

type dockerInspection struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
		Pid     int  `json:"Pid"`
	} `json:"State"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

func (d *DockerDriver) inspect(ctx context.Context, id string) (dockerInspection, error) {
	output, err := d.runner.Output(ctx, nil, d.config.Binary, "inspect", "--format", "{{json .}}", id)
	if err != nil {
		return dockerInspection{}, err
	}
	var resource dockerInspection
	if err := json.Unmarshal([]byte(output), &resource); err != nil {
		return dockerInspection{}, err
	}
	return resource, nil
}

func (d *DockerDriver) owns(labels map[string]string) bool {
	return labels["gateway.provisioning.node"] == runtimeLabelValue(d.config.NodeID) &&
		labels["gateway.provisioning.target"] == d.config.TargetID
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
	existing, err := d.runner.Output(ctx, nil, d.config.Binary,
		"ps", "--all", "--quiet", "--filter", "name=^/"+name+"$")
	if err != nil {
		return nil, fmt.Errorf("find provisioned Docker container: %w", err)
	}
	if id := strings.TrimSpace(existing); id != "" {
		if len(strings.Fields(id)) != 1 {
			return nil, fmt.Errorf("multiple Docker containers match %s", name)
		}
		resource, inspectErr := d.inspect(ctx, id)
		if inspectErr != nil {
			return nil, fmt.Errorf("inspect existing Docker container: %w", inspectErr)
		}
		if resource.Name != "/"+name || !d.owns(resource.Config.Labels) ||
			resource.Config.Labels["gateway.provisioning.instance"] != instanceID ||
			resource.Config.Labels["gateway.provisioning.server"] != spec.ID ||
			resource.Config.Labels["gateway.provisioning.type"] != runtimeLabelValue(spec.Type) ||
			resource.Config.Image != launch.Image || !resource.State.Running || resource.State.Pid < 1 {
			return nil, fmt.Errorf("existing Docker container %s does not match running assignment", id)
		}
		bindings := resource.NetworkSettings.Ports[strconv.Itoa(launch.Port)+"/tcp"]
		if len(bindings) != 1 {
			return nil, fmt.Errorf("existing Docker container %s has no unique published endpoint", id)
		}
		port, parseErr := strconv.Atoi(bindings[0].HostPort)
		if parseErr != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("existing Docker container %s has an invalid published port", id)
		}
		process := &dockerProcess{
			runner: d.runner, binary: d.config.Binary, containerID: id,
			host: dockerRouteHost(d.config.RouteHost, bindings[0].HostIP), port: port,
			pid: resource.State.Pid, stopTimeout: d.config.StopTimeout, done: make(chan struct{}),
		}
		startupContext, cancel := context.WithTimeout(ctx, d.config.StartTimeout)
		defer cancel()
		if err := waitForListener(startupContext, process); err != nil {
			return nil, fmt.Errorf("wait for existing Docker listener: %w", err)
		}
		go process.watch()
		return process, nil
	}
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
	if config.TargetID != "" {
		arguments = append(arguments, "--label", "gateway.provisioning.target="+config.TargetID)
	}
	if config.EnvironmentFile != "" {
		arguments = append(arguments, "--env-file", config.EnvironmentFile)
	}
	for _, variable := range launch.Environment {
		arguments = append(arguments, "--env", variable)
	}
	// A launch spec names the process to run, not arguments for an image's
	// entrypoint. Images such as Laravel Sail interpret the first argument as
	// entrypoint configuration unless Docker explicitly overrides it.
	arguments = append(arguments, "--entrypoint", launch.Executable)
	arguments = append(arguments,
		launch.Image,
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
