package provision

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDockerDriverAdoptsRunningContainer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	spec := validServerSpec()
	spec.Driver = DriverDocker
	runner := &recoveryCommandRunner{
		container:   fmt.Sprintf(`{"Id":"existing-container","Name":"/%s","Config":{"Image":"server:v1","Labels":{"gateway.provisioning.node":"node-a","gateway.provisioning.instance":"%s","gateway.provisioning.server":"%s","gateway.provisioning.type":"test"}},"State":{"Running":true,"Pid":42},"NetworkSettings":{"Ports":{"8080/tcp":[{"HostIp":"127.0.0.1","HostPort":"%d"}]}}}`, runtimeResourceName(spec.AssignmentID), spec.AssignmentID, spec.ID, port),
		waitStopped: make(chan struct{}),
	}
	driver, err := newDockerDriver(DockerDriverConfig{
		Binary: "docker", NodeID: "node-a", PublishHost: "127.0.0.1",
		StartTimeout: time.Second, StopTimeout: time.Second,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	process, err := driver.Start(t.Context(), spec, spec.AssignmentID, &recoveryWorkload{port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if process.PID() != 42 || process.Port() != port || process.Host() != "127.0.0.1" {
		t.Fatalf("wrong adopted endpoint: pid=%d %s:%d", process.PID(), process.Host(), process.Port())
	}
	if err := process.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runner.called("run ") || !runner.called("stop --time") {
		t.Fatalf("container was recreated or not stopped: %v", runner.snapshot())
	}
}

func TestKubernetesDriverAdoptsRunningPod(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	spec := validServerSpec()
	spec.Driver = DriverKubernetes
	runner := &recoveryCommandRunner{pod: fmt.Sprintf(`{"metadata":{"name":"%s","uid":"pod-uid","labels":{"gateway.webong.dev/node":"node-a","gateway.webong.dev/instance":"%s","gateway.webong.dev/server":"%s","gateway.webong.dev/type":"test"}},"spec":{"containers":[{"image":"server:v1","ports":[{"containerPort":%d}]}]},"status":{"phase":"Running"}}`, runtimeResourceName(spec.AssignmentID), spec.AssignmentID, spec.ID, port)}
	driver, err := newKubernetesDriver(KubernetesDriverConfig{
		KubectlBinary: "kubectl", NodeID: "node-a", Namespace: "servers",
		ImagePullPolicy: "IfNotPresent", RouteMode: "pod-ip",
		StartTimeout: time.Second, StopTimeout: time.Second,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	process, err := driver.Start(t.Context(), spec, spec.AssignmentID, &recoveryWorkload{port: port})
	if err != nil {
		t.Fatal(err)
	}
	if process.Port() != port || process.Host() != "127.0.0.1" {
		t.Fatalf("wrong adopted endpoint: %s:%d", process.Host(), process.Port())
	}
	if err := process.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runner.called("create --filename") || !runner.called("delete pod "+runtimeResourceName(spec.AssignmentID)) {
		t.Fatalf("pod was recreated or not stopped: %v", runner.snapshot())
	}
}

func TestKubernetesProcessRefusesToDeleteReplacementPod(t *testing.T) {
	runner := &recordingCommandRunner{outputs: []string{"replacement-uid"}}
	process := &kubernetesProcess{
		runner: runner, binary: "kubectl", namespace: "servers", name: "gateway-server-instance-1",
		uid: "original-uid", done: make(chan struct{}), stopTimeout: time.Second,
	}
	if err := process.Stop(t.Context()); err == nil || !strings.Contains(err.Error(), "changed identity") {
		t.Fatalf("expected identity conflict, got %v", err)
	}
	if strings.Contains(strings.Join(runner.calls, "\n"), "delete pod") {
		t.Fatalf("deleted a replacement Pod: %v", runner.calls)
	}
}

func TestKubernetesWatcherRejectsReplacementPod(t *testing.T) {
	runner := &recordingCommandRunner{outputs: []string{"replacement-uid Running"}}
	process := &kubernetesProcess{
		runner: runner, binary: "kubectl", namespace: "servers", name: "gateway-server-instance-1",
		uid: "original-uid", done: make(chan struct{}),
	}
	go process.watch()
	select {
	case <-process.Done():
		if process.Err() == nil || !strings.Contains(process.Err().Error(), "changed identity") {
			t.Fatalf("watcher accepted replacement Pod: %v", process.Err())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not detect replacement Pod")
	}
}

func TestKubernetesDriverRefusesPodReplacedDuringAdoption(t *testing.T) {
	spec := validServerSpec()
	spec.Driver = DriverKubernetes
	runner := &recoveryCommandRunner{
		pod:      fmt.Sprintf(`{"metadata":{"name":"%s","uid":"original-uid","labels":{"gateway.webong.dev/node":"node-a","gateway.webong.dev/instance":"%s","gateway.webong.dev/server":"%s","gateway.webong.dev/type":"test"}},"spec":{"containers":[{"image":"server:v1","ports":[{"containerPort":8080}]}]},"status":{"phase":"Running"}}`, runtimeResourceName(spec.AssignmentID), spec.AssignmentID, spec.ID),
		readyUID: "replacement-uid",
	}
	driver, err := newKubernetesDriver(KubernetesDriverConfig{
		KubectlBinary: "kubectl", NodeID: "node-a", Namespace: "servers",
		ImagePullPolicy: "IfNotPresent", RouteMode: "pod-ip",
		StartTimeout: time.Second, StopTimeout: time.Second,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Start(t.Context(), spec, spec.AssignmentID, &recoveryWorkload{port: 8080}); err == nil || !strings.Contains(err.Error(), "changed identity") {
		t.Fatalf("expected Pod identity conflict, got %v", err)
	}
	if runner.called("delete pod") || runner.called("create --filename") {
		t.Fatalf("modified replacement Pod: %v", runner.snapshot())
	}
}

type recoveryWorkload struct {
	port       int
	image      string
	name       string
	executable string
	arguments  []string
}

func (w *recoveryWorkload) Type() string {
	if w.name != "" {
		return w.name
	}
	return "test"
}
func (*recoveryWorkload) Supports(driver string) bool { return driver == DriverDocker }
func (*recoveryWorkload) Validate(ServerSpec) error   { return nil }
func (w *recoveryWorkload) Launch(ServerSpec, string, string, int) (LaunchSpec, error) {
	image := w.image
	if image == "" {
		image = "server:v1"
	}
	executable := w.executable
	if executable == "" {
		executable = "server"
	}
	return LaunchSpec{Image: image, Port: w.port, Executable: executable, Arguments: w.arguments}, nil
}

type recoveryCommandRunner struct {
	mu          sync.Mutex
	calls       []string
	container   string
	pod         string
	readyUID    string
	waitStopped chan struct{}
	stopOnce    sync.Once
}

func (r *recoveryCommandRunner) Output(_ context.Context, _ []byte, binary string, arguments ...string) (string, error) {
	call := strings.Join(append([]string{binary}, arguments...), " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	switch {
	case strings.Contains(call, "ps --all --quiet --filter name="):
		return "existing-container", nil
	case strings.Contains(call, "inspect --format {{json .}}"):
		return r.container, nil
	case strings.Contains(call, "wait existing-container"):
		<-r.waitStopped
		return "0", nil
	case strings.Contains(call, "stop --time"):
		r.stopOnce.Do(func() { close(r.waitStopped) })
		return "", nil
	case strings.Contains(call, "get pod ") && strings.Contains(call, "--ignore-not-found=true"):
		if strings.Contains(call, "jsonpath={.metadata.uid}") {
			return "pod-uid", nil
		}
		return r.pod, nil
	case strings.Contains(call, "get pod ") && strings.Contains(call, "containerStatuses[0].ready"):
		uid := r.readyUID
		if uid == "" {
			uid = "pod-uid"
		}
		return "Running 127.0.0.1 true " + uid, nil
	case strings.Contains(call, "get pod ") && strings.Contains(call, "jsonpath={.metadata.uid}"):
		return "pod-uid Running", nil
	case strings.Contains(call, "delete pod "):
		return "", nil
	case strings.Contains(call, "run ") || strings.Contains(call, "create --filename"):
		return "", fmt.Errorf("unexpected resource creation: %s", call)
	default:
		return "", fmt.Errorf("unexpected command: %s", call)
	}
}

func (r *recoveryCommandRunner) called(fragment string) bool {
	for _, call := range r.snapshot() {
		if strings.Contains(call, fragment) {
			return true
		}
	}
	return false
}

func (r *recoveryCommandRunner) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}
