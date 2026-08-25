package roadrunner

// RoadRunner embedding tests.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bridge "github.com/webong/gateway/cmd/bridge"
)

type recordingExecutor struct {
	queued []bridge.Delivery
}

func (e *recordingExecutor) Deliver(_ context.Context, _ bridge.Delivery) (bridge.Response, error) {
	return bridge.Response{StatusCode: http.StatusAccepted}, nil
}

func (e *recordingExecutor) Enqueue(delivery bridge.Delivery) bool {
	e.queued = append(e.queued, delivery)
	return true
}

func TestRoadRunnerPluginPassesNonRelayPathsBackToPHP(t *testing.T) {
	executor := &recordingExecutor{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == bridge.PHPPlannerPath {
			if r.Header.Get(bridge.InternalTokenHeader) != "test-token" {
				t.Fatalf("expected configured internal planner token")
			}
			var ingress bridge.IngressRequest
			if err := json.NewDecoder(r.Body).Decode(&ingress); err != nil {
				t.Fatalf("decode planner request: %v", err)
			}
			plan := bridge.RoutePlan{Version: bridge.ProtocolVersion, Action: bridge.ActionPassThrough}
			if ingress.Path == "/owned/by/subscriber" {
				plan = bridge.RoutePlan{
					Version: bridge.ProtocolVersion,
					Action:  bridge.ActionRelay,
					Relays:  []bridge.Delivery{{URL: "https://subscriber.example.test/receive"}},
				}
			}
			_ = json.NewEncoder(w).Encode(plan)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Laravel application"))
	})
	middleware := NewRoadRunnerPlugin(executor, 1024, "test-token").Middleware(next)

	owned := httptest.NewRecorder()
	middleware.ServeHTTP(owned, httptest.NewRequest(http.MethodPost, "/owned/by/subscriber", nil))
	if owned.Code != http.StatusAccepted || len(executor.queued) != 1 {
		t.Fatalf("expected arbitrary path to be handled by Go transport, got %d and %d deliveries", owned.Code, len(executor.queued))
	}

	application := httptest.NewRecorder()
	middleware.ServeHTTP(application, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if application.Code != http.StatusOK || application.Body.String() != "Laravel application" {
		t.Fatalf("expected PHP pass-through, got %d %q", application.Code, application.Body.String())
	}
}

func TestRoadRunnerPluginExposesProtocolPlanner(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != bridge.PHPProtocolEventPath {
			t.Fatalf("expected protocol event path %q, got %q", bridge.PHPProtocolEventPath, r.URL.Path)
		}
		if r.Header.Get(bridge.InternalTokenHeader) != "test-token" {
			t.Fatalf("expected protocol planner token")
		}
		_ = json.NewEncoder(w).Encode(bridge.GatewayDecision{
			Version:  bridge.ProtocolVersion,
			Protocol: bridge.ProtocolSMTP,
			Action:   bridge.GatewayAccept,
		})
	})
	plugin := NewRoadRunnerPlugin(&recordingExecutor{}, 1024, "test-token")
	plugin.Middleware(next)

	planner, err := plugin.WaitProtocolPlanner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	decision, err := planner.PlanEvent(context.Background(), bridge.GatewayEvent{
		ID:       "smtp-event-1",
		Protocol: bridge.ProtocolSMTP,
		Kind:     bridge.EventTransaction,
		Route:    "recipient@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != bridge.GatewayAccept {
		t.Fatalf("expected accept decision, got %+v", decision)
	}
}

func TestEmbeddedRoadRunnerRegistersGatewayPlugin(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".rr.yaml")
	config := `version: "3"
server:
  command: "php worker.php"
http:
  address: "127.0.0.1:19091"
logs:
  level: error
`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	runner, err := NewEmbeddedRoadRunner(configPath, nil, &recordingExecutor{}, 1024, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	runner.Stop()

	plugins := strings.Join(runner.Plugins(), ",")
	if !strings.Contains(plugins, RoadRunnerPluginName) {
		t.Fatalf("expected %q in RoadRunner plugins, got %s", RoadRunnerPluginName, plugins)
	}
}

func TestEmbeddedRoadRunnerRequiresInternalToken(t *testing.T) {
	_, err := NewEmbeddedRoadRunner(
		filepath.Join(t.TempDir(), ".rr.yaml"),
		nil,
		&recordingExecutor{},
		1024,
		"",
	)
	if err == nil {
		t.Fatal("expected embedded RoadRunner to require an internal token")
	}
}
