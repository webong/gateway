// Package runtime embeds the bundled PHP Planner with FrankenPHP's Go library.
// It does not own the Spinner listener or depend on its standalone command.
package runtime

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/dunglas/frankenphp"
)

const (
	plannerWorkerName           = "gateway-planner"
	plannerWorkerCountEnv       = "GATEWAY_PLANNER_WORKERS"
	legacyPlannerWorkerCountEnv = "GATEWAY_ROUTER_PHP_WORKERS"
	plannerMaxRequestsEnv       = "GATEWAY_PLANNER_MAX_REQUESTS"
	defaultPlannerWorkers       = 1
	defaultPlannerMaxRequests   = 500
)

// Runtime owns one process-wide FrankenPHP lifecycle and serves the Planner
// front controller as an in-process HTTP handler.
type Runtime struct {
	publicDir         string
	workerName        string
	configuredWorkers int
	readyWorkers      atomic.Int64
	closed            atomic.Bool
}

func Start(appDir string) (*Runtime, error) {
	publicDir, err := filepath.Abs(appDir)
	if err != nil {
		return nil, fmt.Errorf("resolve Planner public directory: %w", err)
	}
	index := filepath.Join(publicDir, "index.php")
	if info, err := os.Stat(index); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Planner front controller is missing: %s", index)
	}
	workerScript := filepath.Join(publicDir, "public", "frankenphp-worker.php")
	if info, err := os.Stat(workerScript); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Planner FrankenPHP worker is missing: %s", workerScript)
	}
	workerCount, err := plannerWorkerCount()
	if err != nil {
		return nil, err
	}
	maxRequests, err := plannerMaxRequests()
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		publicDir: publicDir, workerName: plannerWorkerName,
		configuredWorkers: workerCount,
	}
	if err := frankenphp.Init(
		frankenphp.WithMaxRequests(maxRequests),
		frankenphp.WithWorkers(
			plannerWorkerName,
			workerScript,
			workerCount,
			frankenphp.WithWorkerRequestOptions(
				frankenphp.WithRequestDocumentRoot(publicDir, false),
			),
			frankenphp.WithWorkerOnReady(func(int) { runtime.readyWorkers.Add(1) }),
			frankenphp.WithWorkerOnShutdown(func(int) { runtime.readyWorkers.Add(-1) }),
		)); err != nil {
		return nil, fmt.Errorf("initialize embedded FrankenPHP: %w", err)
	}
	return runtime, nil
}

func plannerWorkerCount() (int, error) {
	raw := strings.TrimSpace(os.Getenv(plannerWorkerCountEnv))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(legacyPlannerWorkerCountEnv))
	}
	if raw == "" {
		return defaultPlannerWorkers, nil
	}
	count, err := strconv.Atoi(raw)
	if err != nil || count < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", plannerWorkerCountEnv)
	}
	return count, nil
}

func plannerMaxRequests() (int, error) {
	raw := strings.TrimSpace(os.Getenv(plannerMaxRequestsEnv))
	if raw == "" {
		return defaultPlannerMaxRequests, nil
	}
	count, err := strconv.Atoi(raw)
	if err != nil || count < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", plannerMaxRequestsEnv)
	}
	return count, nil
}

// Ready reports whether at least one configured Planner worker can accept a
// request. Application-level readiness is still decided by Planner's endpoint.
func (r *Runtime) Ready() bool {
	return !r.closed.Load() && r.readyWorkers.Load() > 0
}

func (r *Runtime) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if readinessPath(request.URL.Path) && !r.Ready() {
		http.Error(w, fmt.Sprintf("Planner worker pool is not ready (%d/%d)", r.readyWorkers.Load(), r.configuredWorkers), http.StatusServiceUnavailable)
		return
	}
	// CGI maps hyphens and underscores to the same name. Discard ambiguous
	// client headers before they enter the PHP request environment.
	request = request.Clone(request.Context())
	for name := range request.Header {
		if strings.ContainsRune(name, '_') {
			delete(request.Header, name)
		}
	}
	requestURI := request.URL.RequestURI()
	request.URL.Path = "/index.php"
	request.URL.RawPath = ""

	prepared, err := frankenphp.NewRequestWithContext(request,
		frankenphp.WithRequestDocumentRoot(r.publicDir, false),
		frankenphp.WithRequestEnv(map[string]string{"REQUEST_URI": requestURI}),
		frankenphp.WithWorkerName(r.workerName),
	)
	if err != nil {
		http.Error(w, "Planner request preparation failed", http.StatusInternalServerError)
		return
	}
	if err := frankenphp.ServeHTTP(w, prepared); err != nil {
		http.Error(w, "Planner execution failed", http.StatusBadGateway)
	}
}

func readinessPath(path string) bool {
	return path == "/ready" || path == "/_internal/gateway/ready"
}

func (r *Runtime) Close() {
	if r.closed.Swap(true) {
		return
	}
	frankenphp.Shutdown()
}

var _ http.Handler = (*Runtime)(nil)
