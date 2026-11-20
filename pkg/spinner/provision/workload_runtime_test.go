//go:build linux || darwin

package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestWorkloadServiceHelper(t *testing.T) {
	if os.Getenv("GATEWAY_WORKLOAD_TEST_SERVICE") != "1" {
		return
	}
	listener, err := net.Listen("tcp", os.Getenv("GATEWAY_WORKLOAD_TEST_LISTEN"))
	if err != nil {
		os.Exit(2)
	}
	_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "managed by Workload") }))
	os.Exit(0)
}

type runtimeTestDefinition struct{}

func (*runtimeTestDefinition) Type() string                { return "test" }
func (*runtimeTestDefinition) Supports(driver string) bool { return driver == DriverLocal }
func (*runtimeTestDefinition) Validate(ServerSpec) error   { return nil }
func (*runtimeTestDefinition) Launch(_ ServerSpec, _, host string, port int) (LaunchSpec, error) {
	directory, err := os.Getwd()
	if err != nil {
		return LaunchSpec{}, err
	}
	return LaunchSpec{WorkingDirectory: directory, Executable: os.Args[0], Arguments: []string{"-test.run=^TestWorkloadServiceHelper$"},
		Environment: []string{"GATEWAY_WORKLOAD_TEST_SERVICE=1", "GATEWAY_WORKLOAD_TEST_LISTEN=" + net.JoinHostPort(host, strconv.Itoa(port))}, Port: port}, nil
}

func TestWorkloadRuntimeRoutesReplacesAndWithdrawsOnPermissionLoss(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	supervisor := filepath.Join(t.TempDir(), "gateway-workload-supervisor")
	build := exec.Command("go", "build", "-o", supervisor, "../cmd/workload-supervisor")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build supervisor: %v\n%s", err, output)
	}
	var mu sync.Mutex
	spec := validServerSpec()
	spec.Type = "test"
	online := true
	releases := 0
	reports := make(chan InstanceReport, 100)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mu.Lock()
			releases++
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/_internal/provisioning/nodes/node-workload/assignments" {
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			mu.Lock()
			current, available := spec, online
			mu.Unlock()
			if !available {
				w.WriteHeader(503)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []ServerSpec{current}, "meta": map[string]any{
				"session_id": request["session_id"], "request_nonce": request["request_nonce"], "owner_id": request["owner_id"], "execution_protocol": "workload/v1",
				"issued_at_ms": 1000000, "permission_expires_at_ms": 1003000,
			}})
			return
		}
		var report InstanceReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		reports <- report
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	runtime, err := Start(Config{NodeID: "node-workload", StateDirectory: t.TempDir(), SupervisorBinary: supervisor,
		CoordinationLease: 3 * time.Second, PollInterval: 50 * time.Millisecond, StartTimeout: 5 * time.Second, StopTimeout: 3 * time.Second}, backend.URL, "token", backend.Client(), &fakeLogger{}, &runtimeTestDefinition{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.StopContext(ctx); err != nil {
			t.Error(err)
		}
	}()
	waitRunning := func(revision int64) InstanceReport {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case report := <-reports:
				t.Logf("observed: %+v", report)
				if report.State == "running" && report.Revision == revision {
					return report
				}
			case <-timer.C:
				t.Fatalf("no running report for revision %d", revision)
				return InstanceReport{}
			}
		}
	}
	first := waitRunning(1)
	assertRoute := func(want bool) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "http://"+spec.Hostname+spec.Path, nil)
		response := httptest.NewRecorder()
		if matched := runtime.ServeIfMatched(response, request); matched != want {
			t.Fatalf("route matched=%v want=%v", matched, want)
		}
		if want && response.Body.String() != "managed by Workload" {
			t.Fatalf("route response: %s", response.Body.String())
		}
	}
	// Route publication follows the running observation in the same reconcile.
	time.Sleep(100 * time.Millisecond)
	assertRoute(true)
	mu.Lock()
	spec.Revision = 2
	mu.Unlock()
	second := waitRunning(2)
	if first.PID == second.PID || first.PID == 0 {
		t.Fatalf("revision did not replace process: %d -> %d", first.PID, second.PID)
	}
	time.Sleep(100 * time.Millisecond)
	assertRoute(true)
	mu.Lock()
	online = false
	mu.Unlock()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for {
		select {
		case report := <-reports:
			if report.State == "stopped" && report.Revision == 2 {
				assertRoute(false)
				return
			}
		case <-timer.C:
			t.Fatal("permission loss did not stop execution")
			return
		}
	}
}
