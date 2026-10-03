package provision

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPControlPlaneLoadsSpecsAndReportsInstances(t *testing.T) {
	var reported InstanceReport
	var resetNode string
	var workerSession string
	var released bool
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Gateway-Internal") != "internal-token" {
			t.Fatalf("missing internal token")
		}
		switch request.URL.Path {
		case "/control/_internal/provisioning/nodes/node-a/assignments":
			if request.Method != http.MethodPut {
				t.Fatalf("expected PUT, got %s", request.Method)
			}
			var payload struct {
				SessionID string `json:"session_id"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || len(payload.SessionID) != 32 || request.Header.Get("X-Gateway-Worker-Session") != payload.SessionID {
				t.Fatalf("invalid worker session: %+v, %v", payload, err)
			}
			workerSession = payload.SessionID
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []ServerSpec{validServerSpec()}, "meta": map[string]string{"leader_id": "node-a", "session_id": workerSession}})
		case "/control/_internal/provisioning/servers/server-1/instances/instance-1":
			if request.Method != http.MethodPut {
				t.Fatalf("expected PUT, got %s", request.Method)
			}
			if request.Header.Get("X-Gateway-Worker-Session") != workerSession {
				t.Fatal("instance report lost the worker session")
			}
			if err := json.NewDecoder(request.Body).Decode(&reported); err != nil {
				t.Fatal(err)
			}
			writer.WriteHeader(http.StatusOK)
		case "/control/_internal/provisioning/instances/reset":
			if request.Header.Get("X-Gateway-Worker-Session") != workerSession {
				t.Fatal("node reset lost the worker session")
			}
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			resetNode = payload["node_id"]
			writer.WriteHeader(http.StatusOK)
		case "/control/_internal/provisioning/nodes/node-a/session":
			var payload map[string]string
			if request.Method != http.MethodDelete || json.NewDecoder(request.Body).Decode(&payload) != nil || payload["session_id"] != workerSession {
				t.Fatal("invalid worker session release")
			}
			released = true
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer backend.Close()

	control, err := NewHTTPControlPlane(backend.URL+"/control", "internal-token", backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	specs, leader, err := control.Assignments(t.Context(), "node-a", []string{"local"})
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].ID != "server-1" {
		t.Fatalf("unexpected specs: %+v", specs)
	}
	if leader != "node-a" {
		t.Fatalf("unexpected leader %q", leader)
	}

	report := InstanceReport{NodeID: "node-a", Runtime: "local", PID: 42, Host: "127.0.0.1", Port: 9001, State: "running", Revision: 1}
	if err := control.Report(t.Context(), "server-1", "instance-1", report); err != nil {
		t.Fatal(err)
	}
	if reported.NodeID != "node-a" || reported.Port != 9001 {
		t.Fatalf("unexpected report: %+v", reported)
	}
	if err := control.ResetNode(t.Context(), "node-a"); err != nil {
		t.Fatal(err)
	}
	if resetNode != "node-a" {
		t.Fatalf("unexpected reset node %q", resetNode)
	}
	if err := control.ReleaseNode(t.Context(), "node-a"); err != nil || !released {
		t.Fatalf("worker session was not released: %v", err)
	}
}

func TestHTTPControlPlaneRecognizesActiveWorkerSession(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusLocked)
	}))
	defer backend.Close()
	control, err := NewHTTPControlPlane(backend.URL, "internal-token", backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := control.Assignments(t.Context(), "node-a", []string{"local"}); !errors.Is(err, ErrNodeSessionActive) {
		t.Fatalf("expected active session error, got %v", err)
	}
}

func TestHTTPControlPlaneDoesNotRetryTargetConflict(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusConflict)
	}))
	defer backend.Close()
	control, err := NewHTTPControlPlane(backend.URL, "internal-token", backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := control.Assignments(t.Context(), "node-a", []string{"local"}); err == nil || errors.Is(err, ErrNodeSessionActive) {
		t.Fatalf("permanent target conflict was treated as a retryable session: %v", err)
	}
}

func TestHTTPControlPlaneRejectsPlannerWithoutSessionFence(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": []ServerSpec{}, "meta": map[string]string{"leader_id": "node-a"}})
	}))
	defer backend.Close()
	control, err := NewHTTPControlPlane(backend.URL, "internal-token", backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := control.Assignments(t.Context(), "node-a", []string{"local"}); err == nil || !strings.Contains(err.Error(), "upgrade Planner") {
		t.Fatalf("expected explicit old-Planner refusal, got %v", err)
	}
}
