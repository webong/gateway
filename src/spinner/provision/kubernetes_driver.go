package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type KubernetesDriverConfig struct {
	KubectlBinary        string
	NodeID               string
	Namespace            string
	ServiceAccount       string
	EnvironmentSecret    string
	EnvironmentConfigMap string
	ImagePullPolicy      string
	RouteMode            string
	StartTimeout         time.Duration
	StopTimeout          time.Duration
}

type KubernetesDriver struct {
	config KubernetesDriverConfig
	runner commandRunner
}

func NewKubernetesDriver(config KubernetesDriverConfig) (*KubernetesDriver, error) {
	return newKubernetesDriver(config, execCommandRunner{})
}

func newKubernetesDriver(config KubernetesDriverConfig, runner commandRunner) (*KubernetesDriver, error) {
	if strings.TrimSpace(config.KubectlBinary) == "" {
		return nil, fmt.Errorf("provisioning Kubernetes kubectl binary is required")
	}
	if strings.TrimSpace(config.NodeID) == "" {
		return nil, fmt.Errorf("provisioning Kubernetes node ID is required")
	}
	if strings.TrimSpace(config.Namespace) == "" {
		return nil, fmt.Errorf("provisioning Kubernetes namespace is required")
	}
	if config.ImagePullPolicy != "Always" && config.ImagePullPolicy != "IfNotPresent" && config.ImagePullPolicy != "Never" {
		return nil, fmt.Errorf("provisioning Kubernetes image pull policy must be Always, IfNotPresent, or Never")
	}
	if config.RouteMode != "pod-ip" && config.RouteMode != "port-forward" {
		return nil, fmt.Errorf("provisioning Kubernetes route mode must be pod-ip or port-forward")
	}
	if config.StartTimeout <= 0 || config.StopTimeout <= 0 {
		return nil, fmt.Errorf("provisioning Kubernetes timeouts must be positive")
	}
	if runner == nil {
		return nil, fmt.Errorf("provisioning Kubernetes command runner is required")
	}

	return &KubernetesDriver{config: config, runner: runner}, nil
}

func (d *KubernetesDriver) Prepare(ctx context.Context) error {
	_, err := d.runner.Output(ctx, nil, d.config.KubectlBinary,
		"--namespace", d.config.Namespace,
		"delete", "pods",
		"--selector", "gateway.webong.dev/node="+runtimeLabelValue(d.config.NodeID),
		"--ignore-not-found=true", "--wait=true", "--timeout="+d.config.StopTimeout.String(),
	)

	return err
}

func (d *KubernetesDriver) Start(ctx context.Context, spec ServerSpec, instanceID string, workload Workload) (Process, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if spec.Driver != DriverKubernetes {
		return nil, fmt.Errorf("Kubernetes provisioning driver cannot start %q runtime", spec.Driver)
	}
	launch, err := workload.Launch(spec, DriverKubernetes, "0.0.0.0", 0)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(launch.Image) == "" || launch.Port < 1 || launch.Port > 65535 {
		return nil, fmt.Errorf("Kubernetes workload image and container port are required")
	}

	name := runtimeResourceName(instanceID)
	manifest, err := kubernetesPodManifest(d.config, spec, instanceID, name, launch)
	if err != nil {
		return nil, err
	}
	if _, err := d.runner.Output(ctx, manifest, d.config.KubectlBinary, "--namespace", d.config.Namespace, "create", "--filename", "-"); err != nil {
		return nil, fmt.Errorf("create provisioned Kubernetes pod: %w", err)
	}

	startupContext, cancel := context.WithTimeout(ctx, d.config.StartTimeout)
	defer cancel()
	host, err := d.waitForRunningPod(startupContext, name)
	if err != nil {
		d.remove(name)
		return nil, fmt.Errorf("wait for provisioned Kubernetes pod: %w", err)
	}
	port := launch.Port
	var forward *localProcess
	if d.config.RouteMode == "port-forward" {
		forward, err = d.startPortForward(name, launch.Port)
		if err != nil {
			d.remove(name)
			return nil, fmt.Errorf("start provisioned Kubernetes port-forward: %w", err)
		}
		host = forward.Host()
		port = forward.Port()
	}
	process := &kubernetesProcess{
		runner:      d.runner,
		binary:      d.config.KubectlBinary,
		namespace:   d.config.Namespace,
		name:        name,
		host:        host,
		port:        port,
		forward:     forward,
		stopTimeout: d.config.StopTimeout,
		done:        make(chan struct{}),
	}
	go process.watch()
	if err := waitForListener(startupContext, process); err != nil {
		stopContext, stopCancel := context.WithTimeout(context.Background(), d.config.StopTimeout)
		_ = process.Stop(stopContext)
		stopCancel()
		return nil, fmt.Errorf("wait for provisioned Kubernetes listener: %w", err)
	}

	return process, nil
}

func (d *KubernetesDriver) startPortForward(name string, remotePort int) (*localProcess, error) {
	host := "127.0.0.1"
	port, err := availablePort(host)
	if err != nil {
		return nil, err
	}
	command := exec.Command(d.config.KubectlBinary,
		"--namespace", d.config.Namespace,
		"port-forward", "pod/"+name,
		"--address="+host,
		strconv.Itoa(port)+":"+strconv.Itoa(remotePort),
	)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	process := &localProcess{
		command: command,
		host:    host,
		port:    port,
		done:    make(chan struct{}),
	}
	go process.wait()

	return process, nil
}

func (d *KubernetesDriver) waitForRunningPod(ctx context.Context, name string) (string, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := d.runner.Output(ctx, nil, d.config.KubectlBinary,
			"--namespace", d.config.Namespace,
			"get", "pod", name,
			"--output", `jsonpath={.status.phase}{" "}{.status.podIP}{" "}{.status.containerStatuses[0].ready}`,
		)
		if err == nil {
			fields := strings.Fields(status)
			if len(fields) == 3 && fields[0] == "Running" && fields[1] != "" && fields[2] == "true" {
				return fields[1], nil
			}
			if len(fields) > 0 && (fields[0] == "Failed" || fields[0] == "Succeeded") {
				return "", fmt.Errorf("pod entered terminal phase %s", fields[0])
			}
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func kubernetesPodManifest(config KubernetesDriverConfig, spec ServerSpec, instanceID, name string, launch LaunchSpec) ([]byte, error) {
	environment := make([]map[string]string, 0)
	for _, variable := range launch.Environment {
		key, value, _ := strings.Cut(variable, "=")
		environment = append(environment, map[string]string{"name": key, "value": value})
	}
	command := append([]string{launch.Executable}, launch.Arguments...)
	containerName := launch.ContainerName
	if containerName == "" {
		containerName = "server"
	}
	container := map[string]any{
		"name":            containerName,
		"image":           launch.Image,
		"imagePullPolicy": config.ImagePullPolicy,
		"command":         command,
		"env":             environment,
		"ports": []map[string]any{{
			"name":          "server",
			"containerPort": launch.Port,
			"protocol":      "TCP",
		}},
		"readinessProbe": map[string]any{
			"tcpSocket":      map[string]any{"port": launch.Port},
			"periodSeconds":  1,
			"timeoutSeconds": 1,
		},
	}
	environmentFrom := make([]map[string]any, 0, 2)
	if config.EnvironmentConfigMap != "" {
		environmentFrom = append(environmentFrom, map[string]any{"configMapRef": map[string]string{"name": config.EnvironmentConfigMap}})
	}
	if config.EnvironmentSecret != "" {
		environmentFrom = append(environmentFrom, map[string]any{"secretRef": map[string]string{"name": config.EnvironmentSecret}})
	}
	if len(environmentFrom) > 0 {
		container["envFrom"] = environmentFrom
	}
	podSpec := map[string]any{
		"restartPolicy":                 "Never",
		"terminationGracePeriodSeconds": int64(config.StopTimeout.Seconds()),
		"containers":                    []map[string]any{container},
	}
	if config.ServiceAccount != "" {
		podSpec["serviceAccountName"] = config.ServiceAccount
	}
	manifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name": name,
			"labels": map[string]string{
				"app.kubernetes.io/name":       "gateway-provisioning",
				"app.kubernetes.io/managed-by": "gateway",
				"gateway.webong.dev/type":      runtimeLabelValue(spec.Type),
				"gateway.webong.dev/server":    spec.ID,
				"gateway.webong.dev/instance":  instanceID,
				"gateway.webong.dev/node":      runtimeLabelValue(config.NodeID),
			},
		},
		"spec": podSpec,
	}

	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode provisioned Kubernetes pod: %w", err)
	}

	return encoded, nil
}

func (d *KubernetesDriver) remove(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), d.config.StopTimeout)
	defer cancel()
	_, _ = d.runner.Output(ctx, nil, d.config.KubectlBinary,
		"--namespace", d.config.Namespace,
		"delete", "pod", name,
		"--ignore-not-found=true", "--wait=false",
	)
}

type kubernetesProcess struct {
	runner      commandRunner
	binary      string
	namespace   string
	name        string
	host        string
	port        int
	forward     *localProcess
	stopTimeout time.Duration
	done        chan struct{}
	doneOnce    sync.Once
	errMu       sync.RWMutex
	err         error
}

func (p *kubernetesProcess) watch() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-processDone(p.forward):
			forwardErr := p.forward.Err()
			if forwardErr == nil {
				forwardErr = fmt.Errorf("process exited")
			}
			p.finish(fmt.Errorf("provisioned Kubernetes port-forward exited: %w", forwardErr))
			return
		case <-ticker.C:
		}
		phase, err := p.runner.Output(context.Background(), nil, p.binary,
			"--namespace", p.namespace,
			"get", "pod", p.name,
			"--output", "jsonpath={.status.phase}",
		)
		if err != nil {
			p.finish(fmt.Errorf("inspect provisioned Kubernetes pod: %w", err))
			return
		}
		if phase == "Failed" || phase == "Succeeded" {
			p.finish(fmt.Errorf("provisioned Kubernetes pod entered terminal phase %s", phase))
			return
		}
	}
}

func processDone(process Process) <-chan struct{} {
	if process == nil {
		return nil
	}

	return process.Done()
}

func (p *kubernetesProcess) finish(err error) {
	p.doneOnce.Do(func() {
		p.errMu.Lock()
		p.err = err
		p.errMu.Unlock()
		close(p.done)
	})
}

func (p *kubernetesProcess) PID() int              { return 0 }
func (p *kubernetesProcess) Host() string          { return p.host }
func (p *kubernetesProcess) Port() int             { return p.port }
func (p *kubernetesProcess) Done() <-chan struct{} { return p.done }
func (p *kubernetesProcess) Err() error {
	p.errMu.RLock()
	defer p.errMu.RUnlock()
	return p.err
}

func (p *kubernetesProcess) Stop(ctx context.Context) error {
	p.finish(nil)
	var forwardErr error
	if p.forward != nil {
		forwardErr = p.forward.Stop(ctx)
	}
	_, err := p.runner.Output(ctx, nil, p.binary,
		"--namespace", p.namespace,
		"delete", "pod", p.name,
		"--ignore-not-found=true", "--wait=true", "--timeout="+p.stopTimeout.String(),
	)
	if err != nil {
		return err
	}
	if forwardErr != nil && forwardErr != context.Canceled {
		return forwardErr
	}

	return nil
}

var _ Driver = (*KubernetesDriver)(nil)
var _ Process = (*kubernetesProcess)(nil)
