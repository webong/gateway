package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type workerImageFixture struct {
	image       string
	backend     string
	database    string
	targetID    string
	targetToken string
	ctx         context.Context
	client      *http.Client
	phpLog      *synchronizedBuffer
}

func newWorkerImageFixture(t *testing.T) *workerImageFixture {
	image := os.Getenv("GATEWAY_TEST_PROVISION_WORKER_IMAGE")
	if image == "" {
		t.Skip("set GATEWAY_TEST_PROVISION_WORKER_IMAGE to a locally built worker image")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "vendor", "autoload.php")); err != nil {
		t.Skip("Composer dependencies are required")
	}
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("the local Docker socket is required")
	}
	if output, err := exec.Command("docker", "image", "inspect", image).CombinedOutput(); err != nil {
		t.Fatalf("inspect worker image: %v: %s", err, output)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 55*time.Second)
	t.Cleanup(cancel)
	dataDir := t.TempDir()
	database := filepath.Join(dataDir, "planner.sqlite")
	if err := os.WriteFile(database, nil, 0600); err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(dataDir, "storage")
	for _, path := range []string{"framework/cache/data", "framework/sessions", "framework/views", "logs"} {
		if err := os.MkdirAll(filepath.Join(storage, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cache := filepath.Join(dataDir, "cache")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	backend := "http://127.0.0.1:" + strconv.Itoa(port)
	php := exec.Command("php", "-S", "127.0.0.1:"+strconv.Itoa(port), "-t", "public", "public/index.php")
	php.Dir = filepath.Join(root, "tests", "Fixtures", "octane-app")
	php.Env = append(os.Environ(),
		"APP_ENV=testing", "APP_KEY=base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"COMPOSER_VENDOR_DIR="+filepath.Join(root, "vendor"),
		"DB_CONNECTION=sqlite", "DB_DATABASE="+database,
		"GATEWAY_INTERNAL_TOKEN=test-only", "REGISTRY_TOKEN=registry-test-only",
		"GATEWAY_HTTP_REGISTRY_SMOKE=1", "GATEWAY_DNS_SMOKE=1",
		"GATEWAY_SMOKE_PROVIDERS=Webong\\Gateway\\Centrifugo\\CentrifugoExtensionServiceProvider",
		"GATEWAY_PROVISIONING_COORDINATION_LEASE_SECONDS=2",
		"LARAVEL_STORAGE_PATH="+storage,
		"APP_SERVICES_CACHE="+filepath.Join(cache, "services.php"),
		"APP_PACKAGES_CACHE="+filepath.Join(cache, "packages.php"),
		"APP_CONFIG_CACHE="+filepath.Join(cache, "config.php"),
		"APP_ROUTES_CACHE="+filepath.Join(cache, "routes.php"),
		"APP_EVENTS_CACHE="+filepath.Join(cache, "events.php"),
	)
	phpLog := &synchronizedBuffer{}
	php.Stdout, php.Stderr = phpLog, phpLog
	if err := php.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = php.Process.Kill()
		_ = php.Wait()
	})
	client := &http.Client{Timeout: 2 * time.Second}
	ready := false
	for attempt := 0; attempt < 60; attempt++ {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, backend+"/http-smoke/worker", nil)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("Planner fixture did not become ready: %s", phpLog.String())
	}
	payload, _ := json.Marshal(map[string]any{
		"name": "disposable-image-check", "driver": "docker",
		"connection": map[string]any{
			"context": "default", "publish_host": "127.0.0.1", "route_host": "127.0.0.1",
		},
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, backend+"/targets", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer registry-test-only")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("create disposable Docker target: status %d: %s; Planner: %s", response.StatusCode, body, phpLog.String())
	}
	var target struct {
		ID          string `json:"id"`
		WorkerToken string `json:"worker_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&target); err != nil || target.ID == "" || target.WorkerToken == "" {
		t.Fatalf("decode target credentials: %v", err)
	}
	return &workerImageFixture{
		image: image, backend: backend, database: database,
		targetID: target.ID, targetToken: target.WorkerToken,
		ctx: ctx, client: client, phpLog: phpLog,
	}
}

// Opt-in: checks a built worker image against an isolated real Planner and
// the local Docker daemon. The worker runs --check only; it cannot reconcile.
func TestProvisionWorkerImageChecksRealPlanner(t *testing.T) {
	fixture := newWorkerImageFixture(t)
	worker := exec.CommandContext(fixture.ctx, "docker", "run", "--rm", "--network", "host", "--user", "0:0",
		"--mount", "type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock",
		"--env", "GATEWAY_LARAVEL_BACKEND_URL="+fixture.backend,
		"--env", "GATEWAY_PROVISIONING_TARGET_ID="+fixture.targetID,
		"--env", "GATEWAY_PROVISIONING_TARGET_TOKEN="+fixture.targetToken,
		"--env", "GATEWAY_NODE_ID=disposable-image-check",
		fixture.image, "--check")
	output, err := worker.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Target connection checks passed. No workloads changed.") {
		t.Fatalf("containerized worker target check failed: %v: %s; Planner: %s", err, output, fixture.phpLog.String())
	}
	rows := exec.CommandContext(fixture.ctx, "php", "-r", `$db=new SQLite3($argv[1]); echo $db->querySingle('SELECT COUNT(*) FROM nodes');`, fixture.database)
	count, err := rows.CombinedOutput()
	if err != nil || strings.TrimSpace(string(count)) != "0" {
		t.Fatalf("read-only check registered a node: %v: %s", err, count)
	}
	t.Logf("worker image checked target %s without registering a node", fmt.Sprintf("%.8s", fixture.targetID))
}

func (fixture *workerImageFixture) post(t *testing.T, path string, body any, expected int) map[string]any {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(fixture.ctx, http.MethodPost, fixture.backend+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer registry-test-only")
	request.Header.Set("Content-Type", "application/json")
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	contents, _ := io.ReadAll(response.Body)
	if response.StatusCode != expected {
		t.Fatalf("Planner POST %s returned %d, want %d: %s; log: %s", path, response.StatusCode, expected, contents, fixture.phpLog.String())
	}
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

// Opt-in: uses the actual packaged worker to start a Centrifugo workload,
// hard-kills that worker, then verifies a replacement adopts the same healthy
// Docker container through the real Planner's target/session/report routes.
func TestProvisionWorkerImageRecoversCentrifugo(t *testing.T) {
	fixture := newWorkerImageFixture(t)
	const workloadImage = "centrifugo/centrifugo:v6"
	if output, err := exec.Command("docker", "image", "inspect", workloadImage).CombinedOutput(); err != nil {
		t.Skipf("local %s image is required: %v: %s", workloadImage, err, output)
	}
	server := fixture.post(t, "/servers", map[string]any{
		"type": "centrifugo", "name": "worker-image-recovery", "hostname": "recovery.example.test",
		"target_id": fixture.targetID, "replicas": 1,
		"configuration": map[string]any{
			"client":   map[string]any{"token_hmac_secret_key": "test-only-secret"},
			"http_api": map[string]any{"key": "test-only-api-key"},
		},
	}, http.StatusCreated)
	serverID, ok := server["id"].(string)
	if !ok || serverID == "" {
		t.Fatalf("Planner returned no server ID: %v", server)
	}
	fixture.post(t, "/servers/"+serverID+"/start", map[string]any{}, http.StatusAccepted)

	nodeID := "gateway-image-recovery-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	workerNames := []string{nodeID + "-first", nodeID + "-second"}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		for _, name := range workerNames {
			_ = exec.CommandContext(cleanupCtx, "docker", "rm", "--force", name).Run()
		}
		output, err := exec.CommandContext(cleanupCtx, "docker", "ps", "--all", "--quiet", "--filter",
			"label=gateway.provisioning.node="+runtimeLabelValue(nodeID)).CombinedOutput()
		if err != nil {
			t.Errorf("list disposable workload containers: %v: %s", err, output)
			return
		}
		for _, id := range strings.Fields(string(output)) {
			if result, err := exec.CommandContext(cleanupCtx, "docker", "rm", "--force", id).CombinedOutput(); err != nil {
				t.Errorf("remove disposable workload container %s: %v: %s", id, err, result)
			}
		}
	})
	startWorker := func(name string) {
		command := exec.CommandContext(fixture.ctx, "docker", "run", "--detach", "--rm", "--pull", "never",
			"--name", name, "--network", "host", "--user", "0:0",
			"--mount", "type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock",
			"--env", "GATEWAY_LARAVEL_BACKEND_URL="+fixture.backend,
			"--env", "GATEWAY_PROVISIONING_TARGET_ID="+fixture.targetID,
			"--env", "GATEWAY_PROVISIONING_TARGET_TOKEN="+fixture.targetToken,
			"--env", "GATEWAY_NODE_ID="+nodeID,
			"--env", "GATEWAY_CENTRIFUGO_DOCKER_IMAGE="+workloadImage,
			"--env", "GATEWAY_PROVISIONING_POLL_INTERVAL=200ms",
			fixture.image)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("start packaged worker %s: %v: %s", name, err, output)
		}
	}
	type instance struct {
		ID          string `json:"id"`
		PID         int    `json:"pid"`
		Host        string `json:"host"`
		Port        int    `json:"port"`
		State       string `json:"state"`
		Error       string `json:"error"`
		HeartbeatAt string `json:"heartbeat_at"`
	}
	readInstance := func() (instance, error) {
		request, _ := http.NewRequestWithContext(fixture.ctx, http.MethodGet, fixture.backend+"/instances?server_id="+serverID, nil)
		request.Header.Set("Authorization", "Bearer registry-test-only")
		response, err := fixture.client.Do(request)
		if err != nil {
			return instance{}, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			contents, _ := io.ReadAll(response.Body)
			return instance{}, fmt.Errorf("instances returned %d: %s", response.StatusCode, contents)
		}
		var document struct {
			Data []instance `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
			return instance{}, err
		}
		if len(document.Data) == 0 {
			return instance{}, nil
		}
		return document.Data[0], nil
	}
	awaitRunning := func(after string, workerName string) instance {
		deadline := time.Now().Add(22 * time.Second)
		var current instance
		var lastErr error
		for time.Now().Before(deadline) {
			current, lastErr = readInstance()
			if lastErr == nil && current.State == "running" && current.PID > 0 && current.HeartbeatAt != after {
				return current
			}
			time.Sleep(200 * time.Millisecond)
		}
		logs, _ := exec.CommandContext(fixture.ctx, "docker", "logs", "--tail", "80", workerName).CombinedOutput()
		t.Fatalf("packaged worker did not report a running instance: last=%+v err=%v worker=%s Planner=%s", current, lastErr, logs, fixture.phpLog.String())
		return instance{}
	}
	checkHealth := func(current instance) {
		url := "http://" + net.JoinHostPort(current.Host, strconv.Itoa(current.Port)) + "/health"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			response, err := fixture.client.Get(url)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return
				}
			}
			time.Sleep(150 * time.Millisecond)
		}
		t.Fatalf("packaged Centrifugo workload is not healthy at %s", url)
	}
	inspect := func(current instance) string {
		output, err := exec.CommandContext(fixture.ctx, "docker", "inspect", "--format", "{{.Id}} {{.State.Running}}", runtimeResourceName(current.ID)).CombinedOutput()
		if err != nil {
			t.Fatalf("inspect packaged workload: %v: %s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	startWorker(workerNames[0])
	first := awaitRunning("", workerNames[0])
	checkHealth(first)
	containerBefore := inspect(first)
	if output, err := exec.CommandContext(fixture.ctx, "docker", "rm", "--force", workerNames[0]).CombinedOutput(); err != nil {
		t.Fatalf("hard-kill first packaged worker: %v: %s", err, output)
	}
	if !strings.HasSuffix(inspect(first), " true") {
		t.Fatal("Centrifugo workload stopped when packaged worker crashed")
	}
	startWorker(workerNames[1])
	second := awaitRunning(first.HeartbeatAt, workerNames[1])
	if second.ID != first.ID || second.PID != first.PID || inspect(second) != containerBefore || second.Error != "" {
		t.Fatalf("replacement packaged worker did not adopt the same clean instance: first=%+v second=%+v before=%q after=%q", first, second, containerBefore, inspect(second))
	}
	checkHealth(second)
}
