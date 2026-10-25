package runtime

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestRuntimeServesFrontControllerWithoutASecondPHPListener(t *testing.T) {
	t.Setenv(plannerWorkerCountEnv, "1")
	t.Setenv(legacyPlannerWorkerCountEnv, "")
	appDir := writeWorkerApp(t, false)
	if err := os.WriteFile(filepath.Join(appDir, "config.php"), []byte("<?php echo 'private configuration';"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime, err := Start(appDir)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if !runtime.Ready() {
		t.Fatal("Planner worker pool was not ready after startup")
	}

	request := httptest.NewRequest(http.MethodGet, "http://router.test/health?check=1", nil)
	request.Header.Set("X_Test_Header", "spoofed")
	response := httptest.NewRecorder()
	runtime.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if got := strings.TrimSpace(response.Body.String()); got != "/health?check=1|missing|1" {
		t.Fatalf("unexpected Planner response %q", got)
	}
	response = httptest.NewRecorder()
	runtime.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://router.test/config.php", nil))
	if got := strings.TrimSpace(response.Body.String()); got != "/config.php|missing|2" {
		t.Fatalf("request bypassed the flat front controller: %q", got)
	}
}

func TestRuntimeWorkerPoolHonorsConfiguredSize(t *testing.T) {
	for _, test := range []struct {
		name    string
		workers string
		want    []string
	}{
		{name: "one persistent worker", workers: "1", want: []string{"1", "2"}},
		{name: "two pooled workers", workers: "2", want: []string{"1", "1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(plannerWorkerCountEnv, test.workers)
			t.Setenv(legacyPlannerWorkerCountEnv, "")
			runtime, err := Start(writeWorkerApp(t, true))
			if err != nil {
				t.Fatal(err)
			}

			responses := make([]string, 2)
			var wait sync.WaitGroup
			wait.Add(len(responses))
			for i := range responses {
				go func() {
					defer wait.Done()
					response := httptest.NewRecorder()
					runtime.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://router.test/slow", nil))
					if response.Code != http.StatusOK {
						t.Errorf("status %d: %s", response.Code, response.Body.String())
						return
					}
					parts := strings.Split(strings.TrimSpace(response.Body.String()), "|")
					responses[i] = parts[len(parts)-1]
				}()
			}
			wait.Wait()
			runtime.Close()

			sort.Strings(responses)
			if strings.Join(responses, ",") != strings.Join(test.want, ",") {
				t.Fatalf("worker counters %v, want %v", responses, test.want)
			}
		})
	}
}

func TestPlannerWorkerCountRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"0", "-1", "many"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(plannerWorkerCountEnv, value)
			t.Setenv(legacyPlannerWorkerCountEnv, "")
			if _, err := plannerWorkerCount(); err == nil {
				t.Fatalf("expected %q to be rejected", value)
			}
		})
	}
}

func TestPlannerWorkerCountUsesLegacyFallbackButPrefersPlannerSetting(t *testing.T) {
	t.Setenv(plannerWorkerCountEnv, "")
	t.Setenv(legacyPlannerWorkerCountEnv, "3")
	if count, err := plannerWorkerCount(); err != nil || count != 3 {
		t.Fatalf("legacy worker count = %d, %v; want 3", count, err)
	}

	t.Setenv(plannerWorkerCountEnv, "2")
	if count, err := plannerWorkerCount(); err != nil || count != 2 {
		t.Fatalf("Planner worker count = %d, %v; want 2", count, err)
	}
}

func TestPlannerMaxRequestsRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"0", "-1", "many"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(plannerMaxRequestsEnv, value)
			if _, err := plannerMaxRequests(); err == nil {
				t.Fatalf("expected %q to be rejected", value)
			}
		})
	}
}

func TestRuntimeReadinessFailsClosedWithoutAWorker(t *testing.T) {
	runtime := &Runtime{configuredWorkers: 2}
	for _, path := range []string{"/ready", "/_internal/gateway/ready"} {
		response := httptest.NewRecorder()
		runtime.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d; want %d", path, response.Code, http.StatusServiceUnavailable)
		}
	}
}

func TestRuntimeRecyclesWorkerAfterMaxRequests(t *testing.T) {
	t.Setenv(plannerWorkerCountEnv, "1")
	t.Setenv(legacyPlannerWorkerCountEnv, "")
	t.Setenv(plannerMaxRequestsEnv, "2")
	runtime, err := Start(writeWorkerApp(t, false))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	var calls []string
	for range 3 {
		response := httptest.NewRecorder()
		runtime.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://router.test/plan", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status %d: %s", response.Code, response.Body.String())
		}
		parts := strings.Split(strings.TrimSpace(response.Body.String()), "|")
		calls = append(calls, parts[len(parts)-1])
	}
	if got := strings.Join(calls, ","); got != "1,2,1" {
		t.Fatalf("worker calls = %s; want 1,2,1", got)
	}
}

func BenchmarkRuntimeWorkerPool(b *testing.B) {
	for _, workers := range []int{1, 4} {
		b.Run(strconv.Itoa(workers)+"-workers", func(b *testing.B) {
			b.Setenv(plannerWorkerCountEnv, strconv.Itoa(workers))
			b.Setenv(legacyPlannerWorkerCountEnv, "")
			runtime, err := Start(writeWorkerApp(b, true))
			if err != nil {
				b.Fatal(err)
			}
			defer runtime.Close()

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					response := httptest.NewRecorder()
					runtime.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://router.test/slow", nil))
					if response.Code != http.StatusOK {
						b.Fatalf("status %d: %s", response.Code, response.Body.String())
					}
				}
			})
		})
	}
}

func writeWorkerApp(t testing.TB, slow bool) string {
	t.Helper()
	appDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(appDir, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	publicDir := filepath.Join(appDir, "public")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	delay := ""
	if slow {
		delay = "usleep(100000);"
	}
	worker := `<?php
$calls = 0;
do {
    $ok = frankenphp_handle_request(static function () use (&$calls): void {
        ` + delay + `
        $calls++;
        echo ($_SERVER['REQUEST_URI'] ?? '').'|'.($_SERVER['HTTP_X_TEST_HEADER'] ?? 'missing').'|'.$calls;
    });
} while ($ok);
`
	if err := os.WriteFile(filepath.Join(publicDir, "frankenphp-worker.php"), []byte(worker), 0o644); err != nil {
		t.Fatal(err)
	}
	return appDir
}
