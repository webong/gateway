package provisioning

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPControlPlaneLoadsSpecsAndReportsInstances(t *testing.T) {
	var reported InstanceReport
	var resetNode string
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Gateway-Internal") != "internal-token" {
			t.Fatalf("missing internal token")
		}
		switch request.URL.Path {
		case "/control/_internal/provisioning/servers":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []ServerSpec{validServerSpec()}})
		case "/control/_internal/provisioning/servers/server-1/instances/instance-1":
			if request.Method != http.MethodPut {
				t.Fatalf("expected PUT, got %s", request.Method)
			}
			if err := json.NewDecoder(request.Body).Decode(&reported); err != nil {
				t.Fatal(err)
			}
			writer.WriteHeader(http.StatusOK)
		case "/control/_internal/provisioning/instances/reset":
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			resetNode = payload["node_id"]
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer backend.Close()

	control, err := NewHTTPControlPlane(backend.URL+"/control", "internal-token", backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	specs, err := control.Servers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].ID != "server-1" {
		t.Fatalf("unexpected specs: %+v", specs)
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
}
