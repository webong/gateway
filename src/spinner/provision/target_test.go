package provision

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTargetControlPlaneIsScopedAndFailsClosed(t *testing.T) {
	wrongTarget := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Target") != "east" || r.Header.Get("X-Gateway-Internal") != "target-token" {
			t.Error("missing target credentials")
		}
		id := "east"
		if wrongTarget {
			id = "west"
		}
		if strings.Contains(r.URL.Path, "/targets/") {
			_ = json.NewEncoder(w).Encode(DeploymentTarget{ID: id, Driver: DriverLocal})
		} else {
			spec := validServerSpec()
			spec.TargetID = id
			var payload struct {
				SessionID string `json:"session_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []ServerSpec{spec}, "meta": map[string]string{"session_id": payload.SessionID}})
		}
	}))
	defer backend.Close()
	c, err := NewHTTPControlPlane(backend.URL, "target-token", backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	c.targetID = "east"
	if _, err := c.Target(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Assignments(t.Context(), "node", []string{DriverLocal}); err != nil {
		t.Fatal(err)
	}
	wrongTarget = true
	if _, err := c.Target(t.Context()); err == nil {
		t.Fatal("accepted another target")
	}
	if _, _, err := c.Assignments(t.Context(), "node", []string{DriverLocal}); err == nil {
		t.Fatal("accepted another target's assignment")
	}
}

func TestTargetChecksAreReadOnlyAndExplicit(t *testing.T) {
	cfg := Config{DockerBinary: "docker", KubernetesKubectl: "kubectl"}
	target := DeploymentTarget{Driver: DriverDocker, Connection: TargetConnection{Context: "remote-ssh", PublishHost: "10.0.0.4", RouteHost: "10.0.0.4"}}
	runner := &recordingCommandRunner{}
	if err := target.Check(t.Context(), cfg, runner); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "docker --context remote-ssh info --format {{.ID}}" {
		t.Fatalf("unexpected checks: %v", runner.calls)
	}
	target = DeploymentTarget{Driver: DriverKubernetes, Connection: TargetConnection{Context: "prod", Kubeconfig: "/run/kubeconfig", Namespace: "events", RouteMode: "port-forward"}}
	runner = &recordingCommandRunner{outputs: []string{"yes", "yes", "yes", "yes", "yes", "yes", "yes"}}
	if err := target.Check(t.Context(), cfg, runner); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 7 {
		t.Fatalf("missing checks: %v", runner.calls)
	}
	if !strings.HasSuffix(runner.calls[6], "auth can-i create pods --subresource=portforward") {
		t.Fatal("port-forward permission must check the subresource, not a pod name")
	}
	for _, call := range runner.calls {
		if !strings.HasPrefix(call, "kubectl --kubeconfig /run/kubeconfig --context prod --namespace events auth can-i ") {
			t.Fatalf("unexpected check: %s", call)
		}
	}
	denied := &recordingCommandRunner{outputs: []string{"no"}}
	if err := target.Check(t.Context(), cfg, denied); err == nil {
		t.Fatal("accepted denied permissions")
	}
	target.Connection.Kubeconfig = "relative"
	if err := target.Check(t.Context(), cfg, &recordingCommandRunner{}); err == nil {
		t.Fatal("accepted ambient kubeconfig")
	}
}

func TestRemoteDriversKeepContextForCleanupAndProcessStop(t *testing.T) {
	runner := &recordingCommandRunner{outputs: []string{
		"container-1",
		`{"Id":"container-1","Name":"/gateway-old","Config":{"Labels":{"gateway.provisioning.node":"node","gateway.provisioning.instance":"old"}},"State":{"Running":true}}`,
		"", "",
	}}
	docker, err := newDockerDriver(DockerDriverConfig{Context: "ssh-host", Binary: "docker", NodeID: "node", PublishHost: "10.0.0.4", StartTimeout: time.Second, StopTimeout: time.Second}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := docker.PrepareDesired(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	process := &dockerProcess{runner: docker.runner, binary: "docker", containerID: "container-1", stopTimeout: time.Second, done: make(chan struct{})}
	if err := process.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if !strings.HasPrefix(call, "docker --context ssh-host ") {
			t.Fatalf("lost Docker context: %s", call)
		}
	}
	runner = &recordingCommandRunner{outputs: []string{`{"items":[]}`}}
	kube, err := newKubernetesDriver(KubernetesDriverConfig{Context: "cluster-b", Kubeconfig: "/run/kubeconfig", KubectlBinary: "kubectl", NodeID: "node", Namespace: "events", ImagePullPolicy: "IfNotPresent", RouteMode: "pod-ip", StartTimeout: time.Second, StopTimeout: time.Second}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := kube.PrepareDesired(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	kube.remove("pod-1")
	kp := &kubernetesProcess{runner: kube.runner, binary: "kubectl", namespace: "events", name: "pod-1", done: make(chan struct{}), stopTimeout: time.Second}
	if err := kp.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if !strings.HasPrefix(call, "kubectl --kubeconfig /run/kubeconfig --context cluster-b --namespace events ") {
			t.Fatalf("lost Kubernetes context: %s", call)
		}
	}
}

func TestCheckTargetDoesNotRegisterResetOrPrepare(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/_internal/provisioning/targets/local" {
			t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(DeploymentTarget{ID: "local", Driver: DriverLocal})
	}))
	defer backend.Close()
	if err := CheckTarget(t.Context(), Config{TargetID: "local", TargetToken: "token"}, backend.URL, backend.Client()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected only target lookup, got %d requests", calls)
	}
}
