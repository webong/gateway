package provision

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDriverRegistryDispatchesByServerDriverAndWorkload(t *testing.T) {
	local := &recordingRuntimeDriver{}
	docker := &recordingRuntimeDriver{}
	kubernetes := &recordingRuntimeDriver{}
	workload := &fakeWorkload{workloadType: "test"}
	registry, err := NewDriverRegistry(map[string]Driver{
		DriverLocal:      local,
		DriverDocker:     docker,
		DriverKubernetes: kubernetes,
	}, []Provisioner{workload})
	if err != nil {
		t.Fatal(err)
	}

	for driver, expected := range map[string]*recordingRuntimeDriver{
		DriverLocal: local, DriverDocker: docker, DriverKubernetes: kubernetes,
	} {
		spec := validServerSpec()
		spec.Driver = driver
		if _, err := registry.Start(t.Context(), spec, "instance-1"); err != nil {
			t.Fatal(err)
		}
		if expected.starts != 1 || expected.workload != workload {
			t.Fatalf("expected %s driver and workload to receive start", driver)
		}
		expected.starts = 0
	}

	spec := validServerSpec()
	spec.Driver = DriverDocker
	localOnly, err := NewDriverRegistry(map[string]Driver{DriverLocal: local}, []Provisioner{workload})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := localOnly.Start(t.Context(), spec, "instance-1"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected unconfigured driver error, got %v", err)
	}
}

func TestDriverRegistryPreparesOnlyDesiredAssignmentsPerRuntime(t *testing.T) {
	docker := &recordingRuntimeDriver{}
	kubernetes := &recordingRuntimeDriver{}
	registry, err := NewDriverRegistry(map[string]Driver{
		DriverDocker: docker, DriverKubernetes: kubernetes,
	}, []Provisioner{&fakeWorkload{workloadType: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	dockerSpec := validServerSpec()
	dockerSpec.Driver = DriverDocker
	kubeSpec := validServerSpec()
	kubeSpec.Driver = DriverKubernetes
	kubeSpec.AssignmentID = "second-instance"
	if err := registry.PrepareDesired(t.Context(), []ServerSpec{dockerSpec, kubeSpec}); err != nil {
		t.Fatal(err)
	}
	if len(docker.prepared) != 1 || docker.prepared[dockerSpec.AssignmentID].Driver != DriverDocker ||
		len(kubernetes.prepared) != 1 || kubernetes.prepared[kubeSpec.AssignmentID].Driver != DriverKubernetes {
		t.Fatalf("desired assignments crossed drivers: docker=%v kubernetes=%v", docker.prepared, kubernetes.prepared)
	}
}

func TestDockerRunArgumentsDescribeManagedWorkloadContainer(t *testing.T) {
	spec := validServerSpec()
	spec.Driver = DriverDocker
	config := DockerDriverConfig{
		TargetID: "target-a",
		NodeID:   "node-a", Network: "gateway", EnvironmentFile: "/run/gateway/server.env", PublishHost: "127.0.0.1",
	}
	launch := LaunchSpec{
		Image: "registry.example.test/server:v1", Executable: "server",
		Arguments: []string{"serve", "--port=8080"}, Environment: []string{"SERVER_ID=server-1"}, Port: 8080,
	}
	arguments := strings.Join(dockerRunArguments(config, spec, "instance-1", "gateway-instance-1", launch), " ")
	for _, expected := range []string{
		"--name gateway-instance-1",
		"--network gateway",
		"--env-file /run/gateway/server.env",
		"--publish 127.0.0.1::8080",
		"--env SERVER_ID=server-1",
		"--label gateway.provisioning.type=test",
		"--label gateway.provisioning.node=node-a",
		"--label gateway.provisioning.target=target-a",
		"--entrypoint server registry.example.test/server:v1 serve --port=8080",
	} {
		if !strings.Contains(arguments, expected) {
			t.Fatalf("expected Docker arguments to contain %q: %s", expected, arguments)
		}
	}
}

func TestDockerContainerIDIgnoresFirstPullProgress(t *testing.T) {
	output := "Unable to find image locally\nlayer: Pull complete\nsha256: digest\n0123456789abcdef"
	if id := dockerContainerID(output); id != "0123456789abcdef" {
		t.Fatalf("unexpected container ID %q", id)
	}
}

func TestKubernetesManifestDescribesManagedWorkloadPod(t *testing.T) {
	spec := validServerSpec()
	spec.Driver = DriverKubernetes
	config := KubernetesDriverConfig{
		TargetID: "target-a",
		NodeID:   "node-a", Namespace: "servers", ServiceAccount: "gateway",
		EnvironmentSecret: "server-secrets", EnvironmentConfigMap: "server-config",
		ImagePullPolicy: "IfNotPresent", StopTimeout: 15 * time.Second,
	}
	launch := LaunchSpec{
		Image: "registry.example.test/server:v1", Executable: "server",
		Arguments: []string{"serve", "--port=8080"}, Environment: []string{"SERVER_ID=server-1"},
		Port: 8080, ContainerName: "server",
	}
	manifest, err := kubernetesPodManifest(config, spec, "instance-1", "gateway-instance-1", launch)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(manifest)
	for _, expected := range []string{
		`"kind":"Pod"`,
		`"name":"gateway-instance-1"`,
		`"image":"registry.example.test/server:v1"`,
		`"serviceAccountName":"gateway"`,
		`"configMapRef":{"name":"server-config"}`,
		`"secretRef":{"name":"server-secrets"}`,
		`"name":"SERVER_ID","value":"server-1"`,
		`"gateway.webong.dev/type":"test"`,
		`"gateway.webong.dev/node":"node-a"`,
		`"gateway.webong.dev/target":"target-a"`,
		`"containerPort":8080`,
	} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("expected Kubernetes manifest to contain %q: %s", expected, encoded)
		}
	}
}

func TestSupportedDriverValidation(t *testing.T) {
	for _, driver := range []string{DriverLocal, DriverDocker, DriverKubernetes} {
		spec := validServerSpec()
		spec.Driver = driver
		if err := spec.Validate(); err != nil {
			t.Fatalf("expected %s to be supported: %v", driver, err)
		}
	}
	spec := validServerSpec()
	spec.Driver = "nomad"
	if err := spec.Validate(); err == nil {
		t.Fatal("expected unsupported driver validation error")
	}
}

func TestPersistentDriversRemoveOnlyStaleResourcesOwnedByNode(t *testing.T) {
	dockerRunner := &recordingCommandRunner{outputs: []string{
		"container-a\ncontainer-b",
		`{"Id":"container-a","Name":"/gateway-server-instance-1","Config":{"Image":"server:v1","Labels":{"gateway.provisioning.node":"node-a","gateway.provisioning.instance":"instance-1","gateway.provisioning.server":"server-1","gateway.provisioning.type":"test"}},"State":{"Running":true,"Pid":42}}`,
		`{"Id":"container-b","Name":"/gateway-old","Config":{"Labels":{"gateway.provisioning.node":"node-a","gateway.provisioning.instance":"old"}},"State":{"Running":true}}`,
		"",
	}}
	docker, err := newDockerDriver(DockerDriverConfig{
		Binary: "docker", NodeID: "Node A", PublishHost: "127.0.0.1",
		StartTimeout: time.Second, StopTimeout: time.Second,
	}, dockerRunner)
	if err != nil {
		t.Fatal(err)
	}
	spec := validServerSpec()
	spec.Driver = DriverDocker
	if err := docker.PrepareDesired(t.Context(), map[string]ServerSpec{"instance-1": spec}); err != nil {
		t.Fatal(err)
	}
	dockerCalls := strings.Join(dockerRunner.calls, "\n")
	if !strings.Contains(dockerCalls, "label=gateway.provisioning.node=node-a") || !strings.Contains(dockerCalls, "rm --force container-b") || strings.Contains(dockerCalls, "rm --force container-a") {
		t.Fatalf("unexpected Docker cleanup commands: %s", dockerCalls)
	}

	kubernetesRunner := &recordingCommandRunner{outputs: []string{
		`{"items":[{"metadata":{"name":"gateway-server-instance-1","labels":{"gateway.webong.dev/node":"node-a","gateway.webong.dev/instance":"instance-1","gateway.webong.dev/server":"server-1","gateway.webong.dev/type":"test"}},"status":{"phase":"Running"}},{"metadata":{"name":"gateway-old","labels":{"gateway.webong.dev/node":"node-a","gateway.webong.dev/instance":"old"}},"status":{"phase":"Running"}}]}`,
		"",
	}}
	kubernetes, err := newKubernetesDriver(KubernetesDriverConfig{
		KubectlBinary: "kubectl", NodeID: "Node A", Namespace: "servers",
		ImagePullPolicy: "IfNotPresent", RouteMode: "pod-ip",
		StartTimeout: time.Second, StopTimeout: time.Second,
	}, kubernetesRunner)
	if err != nil {
		t.Fatal(err)
	}
	spec.Driver = DriverKubernetes
	if err := kubernetes.PrepareDesired(t.Context(), map[string]ServerSpec{"instance-1": spec}); err != nil {
		t.Fatal(err)
	}
	kubernetesCalls := strings.Join(kubernetesRunner.calls, "\n")
	if !strings.Contains(kubernetesCalls, "delete pod gateway-old") || !strings.Contains(kubernetesCalls, "gateway.webong.dev/node=node-a") || strings.Contains(kubernetesCalls, "delete pod gateway-instance-1") {
		t.Fatalf("unexpected Kubernetes cleanup command: %s", kubernetesCalls)
	}
}

type recordingRuntimeDriver struct {
	starts   int
	workload Provisioner
	prepared map[string]ServerSpec
}

func (d *recordingRuntimeDriver) PrepareDesired(_ context.Context, desired map[string]ServerSpec) error {
	d.prepared = desired
	return nil
}

func (d *recordingRuntimeDriver) Start(_ context.Context, _ ServerSpec, _ string, workload Provisioner) (Process, error) {
	d.starts++
	d.workload = workload
	return &fakeProcess{done: make(chan struct{})}, nil
}

type fakeWorkload struct {
	workloadType string
}

func (w *fakeWorkload) Type() string            { return w.workloadType }
func (*fakeWorkload) Supports(string) bool      { return true }
func (*fakeWorkload) Validate(ServerSpec) error { return nil }
func (*fakeWorkload) Launch(ServerSpec, string, string, int) (LaunchSpec, error) {
	return LaunchSpec{}, nil
}

type recordingCommandRunner struct {
	outputs []string
	calls   []string
}

func (r *recordingCommandRunner) Output(_ context.Context, _ []byte, binary string, arguments ...string) (string, error) {
	r.calls = append(r.calls, strings.Join(append([]string{binary}, arguments...), " "))
	if len(r.outputs) == 0 {
		return "", nil
	}
	output := r.outputs[0]
	r.outputs = r.outputs[1:]
	return output, nil
}
