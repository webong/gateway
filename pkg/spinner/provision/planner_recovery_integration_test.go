package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Opt-in: this starts the real PHP Planner fixture, two Go worker subprocesses,
// and one disposable Docker workload. No existing Gateway database is used.
func TestDockerWorkerRecoversWithRealPlanner(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_PHP_DOCKER_RECOVERY") != "1" {
		t.Skip("set GATEWAY_TEST_PHP_DOCKER_RECOVERY=1 for PHP Planner/Docker recovery")
	}
	runPlannerDockerRecovery(t, false, false, "centrifugo")
}

func TestCentrifugoPluginRecoversWithRealPlanner(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_CENTRIFUGO_PLUGIN_RECOVERY") != "1" {
		t.Skip("set GATEWAY_TEST_CENTRIFUGO_PLUGIN_RECOVERY=1 for executable-extension recovery")
	}
	runPlannerDockerRecovery(t, true, true, "centrifugo")
}

func TestMercurePluginRecoversWithRealPlanner(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_MERCURE_PLUGIN_RECOVERY") != "1" {
		t.Skip("set GATEWAY_TEST_MERCURE_PLUGIN_RECOVERY=1 for Mercure executable-extension recovery")
	}
	runPlannerDockerRecovery(t, true, true, "mercure")
}

func TestReverbPluginRecoversWithRealPlanner(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_REVERB_PLUGIN_RECOVERY") != "1" {
		t.Skip("set GATEWAY_TEST_REVERB_PLUGIN_RECOVERY=1 for Reverb executable-extension recovery")
	}
	runPlannerDockerRecovery(t, true, true, "reverb")
}

func runPlannerDockerRecovery(t *testing.T, realWorkload, pluginWorkload bool, technology string) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "vendor", "autoload.php")); err != nil {
		t.Skip("Composer dependencies are required")
	}
	image := "nginx:alpine"
	if realWorkload {
		image = "centrifugo/centrifugo:v6"
		if technology == "mercure" {
			image = "dunglas/mercure:latest"
		} else if technology == "reverb" {
			image = "sail-8.4/app:latest"
		}
	}
	if output, err := exec.Command("docker", "image", "inspect", image).CombinedOutput(); err != nil {
		t.Skipf("local %s image is required: %v: %s", image, err, output)
	}
	testTimeout := 55 * time.Second
	if technology == "reverb" {
		testTimeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()
	dataDir := t.TempDir()
	pluginPath := ""
	if pluginWorkload {
		pluginPath = filepath.Join(dataDir, "gateway-"+technology)
		build := exec.CommandContext(ctx, "go", "build", "-mod=mod", "-o", pluginPath, "./cmd/plugin")
		build.Dir = filepath.Join(root, "ext", technology)
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s executable extension: %v: %s", technology, err, output)
		}
	}
	database := filepath.Join(dataDir, "planner.sqlite")
	if err := os.WriteFile(database, nil, 0600); err != nil {
		t.Fatal(err)
	}
	storage := filepath.Join(dataDir, "storage")
	for _, path := range []string{
		"framework/cache/data", "framework/sessions", "framework/views", "logs",
	} {
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
	backendURL := "http://127.0.0.1:" + strconv.Itoa(port)
	provider := "Webong\\Gateway\\Centrifugo\\CentrifugoExtensionServiceProvider"
	if technology == "mercure" {
		provider = "Webong\\Gateway\\Mercure\\MercureExtensionServiceProvider"
	} else if technology == "reverb" {
		provider = strings.Join([]string{
			"Laravel\\Reverb\\ApplicationManagerServiceProvider",
			"Laravel\\Reverb\\ReverbServiceProvider",
			"Webong\\Gateway\\Reverb\\ReverbExtensionServiceProvider",
			"Webong\\Gateway\\Reverb\\Tests\\Fixtures\\ReverbSmokeServiceProvider",
		}, ",")
	}
	php := exec.Command("php", "-S", "127.0.0.1:"+strconv.Itoa(port), "-t", "public", "public/index.php")
	php.Dir = filepath.Join(root, "tests", "Fixtures", "octane-app")
	php.Env = append(os.Environ(),
		"APP_ENV=testing", "APP_KEY=base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"COMPOSER_VENDOR_DIR="+filepath.Join(root, "vendor"),
		"DB_CONNECTION=sqlite", "DB_DATABASE="+database,
		"GATEWAY_INTERNAL_TOKEN=test-only", "REGISTRY_TOKEN=registry-test-only",
		"GATEWAY_HTTP_REGISTRY_SMOKE=1", "GATEWAY_DNS_SMOKE=1",
		"GATEWAY_REVERB_SMOKE=1",
		"GATEWAY_SMOKE_PROVIDERS="+provider,
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
	var stopPHP sync.Once
	t.Cleanup(func() {
		stopPHP.Do(func() {
			_ = php.Process.Kill()
			_ = php.Wait()
		})
	})
	client := &http.Client{Timeout: 3 * time.Second}
	ready := false
	for attempt := 0; attempt < 60; attempt++ {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, backendURL+"/http-smoke/worker", nil)
		response, err := client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("PHP Planner did not become ready: %s", phpLog.String())
	}

	post := func(path string, body any, expected int) map[string]any {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, backendURL+path, bytes.NewReader(payload))
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
		contents, _ := io.ReadAll(response.Body)
		if response.StatusCode != expected {
			t.Fatalf("Planner POST %s returned %d, want %d: %s; log: %s", path, response.StatusCode, expected, contents, phpLog.String())
		}
		var document map[string]any
		if err := json.Unmarshal(contents, &document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	configuration := map[string]any{
		"client":   map[string]any{"token_hmac_secret_key": "test-only-secret"},
		"http_api": map[string]any{"key": "test-only-api-key"},
	}
	if technology == "mercure" {
		configuration = map[string]any{
			"publisher_jwt":  map[string]any{"key": "test-only-publisher", "algorithm": "HS256"},
			"subscriber_jwt": map[string]any{"key": "test-only-subscriber", "algorithm": "HS256"},
			"anonymous":      true,
		}
	} else if technology == "reverb" {
		configuration = map[string]any{"scaling": map[string]any{"enabled": false}}
	}
	serverPath := "/"
	if technology == "reverb" {
		serverPath = "/reverb"
	}
	server := post("/servers", map[string]any{
		"type": technology, "name": "recovery-smoke", "hostname": "recovery.example.test",
		"driver": "docker", "replicas": 1,
		"path":          serverPath,
		"configuration": configuration,
	}, http.StatusCreated)
	serverID, ok := server["id"].(string)
	if !ok || serverID == "" {
		t.Fatalf("Planner returned no server ID: %v", server)
	}
	post("/servers/"+serverID+"/start", map[string]any{}, http.StatusAccepted)
	if technology == "reverb" {
		post("/applications", map[string]any{
			"server_id": serverID, "app_id": "reverb-recovery-app",
			"key": "reverb-recovery-key", "secret": "reverb-recovery-secret",
			"allowed_origins": []string{"client.example.test"},
		}, http.StatusCreated)
		image = "gateway-reverb-recovery:" + strconv.FormatInt(time.Now().UnixNano(), 36)
		smokeDir := filepath.Join(dataDir, "reverb-image")
		if err := os.MkdirAll(smokeDir, 0700); err != nil {
			t.Fatal(err)
		}
		snapshot, err := os.ReadFile(database)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(smokeDir, "reverb.sqlite"), snapshot, 0600); err != nil {
			t.Fatal(err)
		}
		build := exec.CommandContext(ctx, "docker", "build",
			"--build-context", "dependencies="+filepath.Join(root, "vendor"),
			"--build-context", "smoke="+smokeDir,
			"--file", filepath.Join(root, "pkg/reverb/tests/Fixtures/reverb-kubernetes.Dockerfile"),
			"--tag", image, root)
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build disposable Reverb image: %v: %s", err, output)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cleanupCancel()
			if output, err := exec.CommandContext(cleanupCtx, "docker", "image", "rm", image).CombinedOutput(); err != nil {
				t.Errorf("remove disposable Reverb image: %v: %s", err, output)
			}
		})
		envFile := filepath.Join(dataDir, "reverb-container.env")
		environment := strings.Join([]string{
			"APP_ENV=testing",
			"APP_KEY=base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			"COMPOSER_VENDOR_DIR=/workspace/vendor",
			"DB_CONNECTION=sqlite",
			"DB_DATABASE=/workspace/tests/Fixtures/octane-app/database/reverb.sqlite",
			"GATEWAY_HTTP_REGISTRY_SMOKE=1",
			"GATEWAY_REVERB_SMOKE=1",
			"GATEWAY_SMOKE_SKIP_MIGRATIONS=1",
			"GATEWAY_SMOKE_PROVIDERS=" + provider,
		}, "\n") + "\n"
		if err := os.WriteFile(envFile, []byte(environment), 0600); err != nil {
			t.Fatal(err)
		}
		preflightName := "gateway-reverb-preflight-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		preflight := exec.CommandContext(ctx, "docker", "run", "--detach", "--name", preflightName,
			"--env-file", envFile, "--env", "GATEWAY_REVERB_SERVER_ID="+serverID,
			"--entrypoint", "php", image, "artisan", "reverb:start", "--host=0.0.0.0", "--port=8080", "--hostname=recovery.example.test", "--path=/reverb")
		if output, err := preflight.CombinedOutput(); err != nil {
			t.Fatalf("start Reverb image preflight: %v: %s", err, output)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			_, _ = exec.CommandContext(cleanupCtx, "docker", "rm", "--force", preflightName).CombinedOutput()
		})
		time.Sleep(time.Second)
		state, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", preflightName).CombinedOutput()
		if err != nil || strings.TrimSpace(string(state)) != "true" {
			logs, _ := exec.CommandContext(ctx, "docker", "logs", preflightName).CombinedOutput()
			t.Fatalf("Reverb image preflight exited: inspect=%v state=%s logs=%s", err, state, logs)
		}
		t.Setenv("GATEWAY_TEST_DOCKER_ENV_FILE", envFile)
	}

	nodeID := "gateway-real-planner-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		output, err := exec.CommandContext(cleanupCtx, "docker", "ps", "--all", "--quiet", "--filter",
			"label=gateway.provisioning.node="+runtimeLabelValue(nodeID)).CombinedOutput()
		if err != nil {
			t.Errorf("list disposable recovery containers: %v: %s", err, output)
			return
		}
		for _, id := range strings.Fields(string(output)) {
			if result, err := exec.CommandContext(cleanupCtx, "docker", "rm", "--force", id).CombinedOutput(); err != nil {
				t.Errorf("remove disposable recovery container %s: %v: %s", id, err, result)
			}
		}
	})
	startWorker := func() (func() error, *synchronizedBuffer) {
		command := exec.Command(os.Args[0], "-test.run=^TestDockerWorkerRecoversAfterCrash$")
		workerEnv := append(os.Environ(),
			"GATEWAY_TEST_WORKER_CHILD=1", "GATEWAY_TEST_REAL_PLANNER=1",
			"GATEWAY_TEST_BACKEND="+backendURL, "GATEWAY_TEST_NODE="+nodeID,
		)
		if pluginWorkload {
			workerEnv = append(workerEnv,
				"GATEWAY_TEST_EXECUTABLE_PLUGIN=1",
				"GATEWAY_EXTENSION_PATHS="+pluginPath,
			)
			if technology == "mercure" {
				workerEnv = append(workerEnv, "GATEWAY_MERCURE_DOCKER_IMAGE="+image)
			} else if technology == "reverb" {
				workerEnv = append(workerEnv, "GATEWAY_REVERB_DOCKER_IMAGE="+image)
			} else {
				workerEnv = append(workerEnv, "GATEWAY_CENTRIFUGO_DOCKER_IMAGE="+image)
			}
		}
		command.Env = workerEnv
		log := &synchronizedBuffer{}
		command.Stdout, command.Stderr = log, log
		if err := command.Start(); err != nil {
			t.Fatal(err)
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
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, backendURL+"/instances?server_id="+serverID, nil)
		request.Header.Set("Authorization", "Bearer registry-test-only")
		response, err := client.Do(request)
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
	awaitRunning := func(after string, log *synchronizedBuffer) instance {
		deadline := time.NewTimer(18 * time.Second)
		defer deadline.Stop()
		for {
			current, err := readInstance()
			if err == nil && current.State == "running" && current.PID > 0 && current.HeartbeatAt != after {
				return current
			}
			select {
			case <-deadline.C:
				t.Fatalf("Planner did not report a recovered instance: last=%+v err=%v worker=%s php=%s", current, err, log.String(), phpLog.String())
			case <-ctx.Done():
				t.Fatalf("Planner recovery timed out: %v; worker=%s php=%s", ctx.Err(), log.String(), phpLog.String())
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	stopFirst, firstLog := startWorker()
	first := awaitRunning("", firstLog)
	checkHealth := func(current instance) {
		if !realWorkload {
			return
		}
		if technology == "reverb" {
			url := "ws://" + net.JoinHostPort(current.Host, strconv.Itoa(current.Port)) + "/reverb/app/reverb-recovery-key?protocol=7&client=js&version=8.4.0&flash=false"
			headers := http.Header{"Host": []string{"recovery.example.test"}, "Origin": []string{"https://client.example.test"}}
			deadline := time.Now().Add(12 * time.Second)
			var lastErr error
			for time.Now().Before(deadline) {
				connection, response, err := websocket.DefaultDialer.Dial(url, headers)
				if err == nil {
					_ = connection.Close()
					return
				}
				lastErr = err
				if response != nil {
					lastErr = fmt.Errorf("WebSocket status %d: %w", response.StatusCode, err)
					_ = response.Body.Close()
				}
				time.Sleep(150 * time.Millisecond)
			}
			logs, _ := exec.CommandContext(ctx, "docker", "logs", "--tail", "80", runtimeResourceName(current.ID)).CombinedOutput()
			t.Fatalf("Reverb WebSocket did not become healthy at %s: %v; container logs: %s", url, lastErr, logs)
		}
		healthPath := "/health"
		if technology == "mercure" {
			healthPath = "/healthz"
		}
		url := "http://" + net.JoinHostPort(current.Host, strconv.Itoa(current.Port)) + healthPath
		deadline := time.Now().Add(10 * time.Second)
		var lastErr error
		for time.Now().Before(deadline) {
			response, err := client.Get(url)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return
				}
				lastErr = fmt.Errorf("status %d", response.StatusCode)
			} else {
				lastErr = err
			}
			time.Sleep(150 * time.Millisecond)
		}
		logs, _ := exec.CommandContext(ctx, "docker", "logs", "--tail", "80", runtimeResourceName(current.ID)).CombinedOutput()
		t.Fatalf("%s did not become healthy at %s: %v; container logs: %s", technology, url, lastErr, logs)
	}
	checkHealth(first)
	var checkManagedRoute func()
	if realWorkload {
		managedRouter, err := NewRemoteRouter(backendURL, "test-only", client, []string{"127.0.0.0/8"})
		if err != nil {
			t.Fatal(err)
		}
		public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !managedRouter.ServeIfMatched(w, r) {
				http.NotFound(w, r)
			}
		}))
		defer public.Close()
		checkManagedRoute = func() {
			if technology == "reverb" {
				address := "ws" + public.URL[len("http"):] + "/reverb/app/reverb-recovery-key?protocol=7&client=js&version=8.4.0&flash=false"
				headers := http.Header{"Host": []string{"recovery.example.test"}, "Origin": []string{"https://client.example.test"}}
				connection, response, dialErr := websocket.DefaultDialer.Dial(address, headers)
				if dialErr != nil {
					status := 0
					if response != nil {
						status = response.StatusCode
						_ = response.Body.Close()
					}
					t.Fatalf("public Spinner managed WebSocket route failed: status=%d err=%v", status, dialErr)
				}
				_ = connection.Close()
				return
			}
			healthPath := "/health"
			if technology == "mercure" {
				healthPath = "/healthz"
			}
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, public.URL+healthPath, nil)
			request.Host = "recovery.example.test"
			response, routeErr := client.Do(request)
			if routeErr != nil {
				t.Fatalf("public Spinner managed HTTP route failed: %v", routeErr)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(response.Body)
				lookupRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, backendURL+"/_internal/provisioning/routes/resolve?hostname=recovery.example.test&path="+healthPath, nil)
				lookupRequest.Header.Set("X-Gateway-Internal", "test-only")
				lookupResponse, lookupErr := client.Do(lookupRequest)
				lookupStatus, lookupBody := 0, ""
				if lookupErr == nil {
					lookupStatus = lookupResponse.StatusCode
					contents, _ := io.ReadAll(lookupResponse.Body)
					lookupBody = string(contents)
					_ = lookupResponse.Body.Close()
				}
				t.Fatalf("public Spinner managed HTTP route returned %d: %s; Planner lookup=%d %s err=%v server=%v first=%+v", response.StatusCode, body, lookupStatus, lookupBody, lookupErr, server, first)
			}
		}
		checkManagedRoute()
	}
	name := runtimeResourceName(first.ID)
	inspect := func() string {
		output, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Id}} {{.State.Running}}", name).CombinedOutput()
		if err != nil {
			t.Fatalf("inspect provisioned container: %v: %s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	containerBefore := inspect()
	if err := stopFirst(); err == nil {
		t.Fatal("expected a hard-killed worker")
	}
	if !strings.HasSuffix(inspect(), " true") {
		t.Fatal("workload stopped when its worker crashed")
	}
	stopSecond, secondLog := startWorker()
	second := awaitRunning(first.HeartbeatAt, secondLog)
	if second.ID != first.ID || second.PID != first.PID || inspect() != containerBefore {
		t.Fatalf("replacement did not adopt the same instance and container: first=%+v second=%+v before=%q after=%q", first, second, containerBefore, inspect())
	}
	if second.Error != "" {
		t.Fatalf("recovered running instance retained stale error: %q", second.Error)
	}
	checkHealth(second)
	if checkManagedRoute != nil {
		checkManagedRoute()
	}
	_ = stopSecond()
}
