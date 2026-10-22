package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Run with GATEWAY_TEST_DOCKER_ADOPTION=1 when a local Docker daemon and the
// nginx:alpine image are available. The test owns only its uniquely named container.
func TestDockerDriverAdoptsRealContainer(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_DOCKER_ADOPTION") != "1" {
		t.Skip("set GATEWAY_TEST_DOCKER_ADOPTION=1 for a local Docker recovery smoke")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	spec := validServerSpec()
	spec.Driver = DriverDocker
	spec.AssignmentID = "recovery-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	nodeID := "gateway-recovery-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	name := runtimeResourceName(spec.AssignmentID)
	command := exec.CommandContext(ctx, "docker", "run", "--detach", "--rm", "--name", name,
		"--label", "gateway.provisioning.node="+nodeID,
		"--label", "gateway.provisioning.instance="+spec.AssignmentID,
		"--label", "gateway.provisioning.server="+spec.ID,
		"--label", "gateway.provisioning.type="+runtimeLabelValue(spec.Type),
		"--publish", "127.0.0.1::80", "nginx:alpine")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("start predecessor container: %v: %s", err, output)
	}
	id := strings.TrimSpace(string(output))
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = exec.CommandContext(cleanupCtx, "docker", "rm", "--force", id).Run()
	})
	driver, err := NewDockerDriver(DockerDriverConfig{
		Binary: "docker", NodeID: nodeID, PublishHost: "127.0.0.1",
		StartTimeout: 10 * time.Second, StopTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.PrepareDesired(ctx, map[string]ServerSpec{spec.AssignmentID: spec}); err != nil {
		t.Fatal(err)
	}
	process, err := driver.Start(ctx, spec, spec.AssignmentID, &recoveryWorkload{port: 80, image: "nginx:alpine"})
	if err != nil {
		t.Fatal(err)
	}
	if got := process.(*dockerProcess).containerID; !strings.HasPrefix(id, got) {
		t.Fatalf("recreated container: expected %s, got %s", id, got)
	}
	if err := process.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

// This exercises the full Go runtime across a hard process kill. The HTTP
// control-plane fixture enforces the same session lease shape as Planner, but
// deliberately does not claim to replace a real PHP Planner integration test.
func TestDockerWorkerRecoversAfterCrash(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_WORKER_CHILD") == "1" {
		runDockerRecoveryWorker(t)
		return
	}
	if os.Getenv("GATEWAY_TEST_DOCKER_WORKER_RECOVERY") != "1" {
		t.Skip("set GATEWAY_TEST_DOCKER_WORKER_RECOVERY=1 for a local Docker worker crash smoke")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	spec := validServerSpec()
	spec.Driver = DriverDocker
	spec.AssignmentID = "worker-recovery-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	nodeID := "gateway-worker-recovery-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	name := runtimeResourceName(spec.AssignmentID)
	container := exec.CommandContext(ctx, "docker", "run", "--detach", "--rm", "--name", name,
		"--label", "gateway.provisioning.node="+nodeID,
		"--label", "gateway.provisioning.instance="+spec.AssignmentID,
		"--label", "gateway.provisioning.server="+spec.ID,
		"--label", "gateway.provisioning.type="+runtimeLabelValue(spec.Type),
		"--publish", "127.0.0.1::80", "nginx:alpine")
	output, err := container.CombinedOutput()
	if err != nil {
		t.Fatalf("start predecessor workload: %v: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = exec.CommandContext(cleanupCtx, "docker", "rm", "--force", containerID).Run()
	})

	type report struct {
		session string
		pid     int
	}
	reports := make(chan report, 16)
	var mu sync.Mutex
	owner := ""
	leaseUntil := time.Time{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Internal") != "test-only" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		session := r.Header.Get("X-Gateway-Worker-Session")
		switch {
		case strings.HasSuffix(r.URL.Path, "/assignments"):
			var request struct {
				SessionID string `json:"session_id"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.SessionID != session || session == "" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			mu.Lock()
			if owner != "" && owner != session && time.Now().Before(leaseUntil) {
				mu.Unlock()
				w.WriteHeader(http.StatusLocked)
				return
			}
			owner = session
			leaseUntil = time.Now().Add(2 * time.Second)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []ServerSpec{spec}, "meta": map[string]string{"session_id": session, "leader_id": nodeID},
			})
		case strings.HasSuffix(r.URL.Path, "/instances/reset"):
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "/instances/"):
			var observed InstanceReport
			if json.NewDecoder(r.Body).Decode(&observed) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			valid := owner == session && time.Now().Before(leaseUntil)
			mu.Unlock()
			if !valid {
				w.WriteHeader(http.StatusConflict)
				return
			}
			if observed.State == "running" {
				select {
				case reports <- report{session: session, pid: observed.PID}:
				default:
				}
			}
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/session"):
			mu.Lock()
			if owner == session {
				owner = ""
			}
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer backend.Close()

	startWorker := func() (func() error, *synchronizedBuffer) {
		command := exec.Command(os.Args[0], "-test.run=^TestDockerWorkerRecoversAfterCrash$")
		command.Env = append(os.Environ(),
			"GATEWAY_TEST_WORKER_CHILD=1", "GATEWAY_TEST_BACKEND="+backend.URL,
			"GATEWAY_TEST_NODE="+nodeID, "GATEWAY_TEST_ASSIGNMENT="+spec.AssignmentID,
		)
		log := &synchronizedBuffer{}
		command.Stdout = log
		command.Stderr = log
		if err := command.Start(); err != nil {
			t.Fatalf("start recovery worker: %v", err)
		}
		var once sync.Once
		var waitErr error
		stop := func() error {
			once.Do(func() {
				_ = command.Process.Kill()
				waitErr = command.Wait()
			})
			return waitErr
		}
		t.Cleanup(func() { _ = stop() })
		return stop, log
	}
	awaitReport := func(avoid string, log *synchronizedBuffer) report {
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		for {
			select {
			case observed := <-reports:
				if observed.session != avoid {
					return observed
				}
			case <-timer.C:
				t.Fatalf("worker did not report adoption within 15s; log: %s", log.String())
			case <-ctx.Done():
				t.Fatalf("worker did not report adoption: %v; log: %s", ctx.Err(), log.String())
			}
		}
	}
	stopFirst, firstLog := startWorker()
	first := awaitReport("", firstLog)
	if first.pid < 1 {
		t.Fatalf("first worker did not adopt a running container: %+v", first)
	}
	if err := stopFirst(); err == nil {
		t.Fatal("expected a hard-killed worker")
	}
	if output, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", containerID).CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "true" {
		t.Fatalf("workload stopped with crashed worker: %v: %s", err, output)
	}
	stopSecond, secondLog := startWorker()
	second := awaitReport(first.session, secondLog)
	if second.pid != first.pid {
		t.Fatalf("worker recreated workload: old PID %d, new PID %d", first.pid, second.pid)
	}
	if output, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Id}}", containerID).CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != containerID {
		t.Fatalf("original container missing after recovery: %v: %s", err, output)
	}
	_ = stopSecond()
}

type synchronizedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(value)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func runDockerRecoveryWorker(t *testing.T) {
	config := Config{
		NodeID: os.Getenv("GATEWAY_TEST_NODE"), DockerBinary: "docker",
		DockerPublishHost: "127.0.0.1", CoordinationLease: 2 * time.Second,
		DockerEnvironmentFile: os.Getenv("GATEWAY_TEST_DOCKER_ENV_FILE"),
		PollInterval:          200 * time.Millisecond, StartTimeout: 10 * time.Second,
		StopTimeout: 3 * time.Second,
	}
	var workload Workload = &recoveryWorkload{port: 80, image: "nginx:alpine"}
	var plugins *PluginSet
	if os.Getenv("GATEWAY_TEST_EXECUTABLE_PLUGIN") == "1" {
		paths, err := PluginPaths()
		if err != nil {
			t.Fatalf("read executable extension paths: %v", err)
		}
		var workloads []Workload
		plugins, workloads, err = LoadPlugins(paths)
		if err != nil {
			t.Fatalf("load executable extension: %v", err)
		}
		defer plugins.Close()
		if len(workloads) != 1 {
			t.Fatalf("expected one executable workload, got %d", len(workloads))
		}
		workload = workloads[0]
	} else if os.Getenv("GATEWAY_TEST_REAL_PLANNER") == "1" {
		workload = &recoveryWorkload{
			name: "centrifugo", port: 80, image: "nginx:alpine",
			executable: "nginx", arguments: []string{"-g", "daemon off;"},
		}
	}
	runtime, err := Start(config, os.Getenv("GATEWAY_TEST_BACKEND"), "test-only", &http.Client{Timeout: 5 * time.Second}, &fakeLogger{}, workload)
	if err != nil {
		t.Fatalf("start worker runtime: %v", err)
	}
	_ = runtime
	select {}
}
