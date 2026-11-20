package provision

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
