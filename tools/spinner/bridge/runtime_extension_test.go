package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	"github.com/webong/gateway/arranger/integration"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	integrationextensions "github.com/webong/gateway/pkg/spinner/extensions/integration"
)

type runtimePlanner func(context.Context, integration.IngressRequest) (integration.RoutePlan, error)

func (f runtimePlanner) Plan(ctx context.Context, request integration.IngressRequest) (integration.RoutePlan, error) {
	return f(ctx, request)
}

type runtimeExecutor struct{}

func (runtimeExecutor) Deliver(context.Context, integration.Delivery) (integration.Response, error) {
	return integration.Response{}, nil
}
func (runtimeExecutor) Enqueue(integration.Delivery) bool               { return true }
func (runtimeExecutor) EnqueueGateway(integration.GatewayDelivery) bool { return true }

func TestWebCanBeInstalledAfterSpinnerStarts(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	storeRoot := t.TempDir()
	destination := filepath.Join(storeRoot, "webong%2fweb")
	command := exec.Command(filepath.Join(root, "scripts", "build-gateway-runtime-extension"), filepath.Join(root, "pkg", "web"), destination, "0.1.0", "test-runtime-r1")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Web runtime extension: %v\n%s", err, output)
	}

	packages, err := store.New(storeRoot, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := packages.Load("webong/web")
	if err != nil {
		t.Fatal(err)
	}

	registry := integrationextensions.NewRegistry()
	planner := runtimePlanner(func(_ context.Context, request integration.IngressRequest) (integration.RoutePlan, error) {
		return integration.RoutePlan{
			Version: integration.ProtocolVersion, Action: integration.ActionRespond,
			ImmediateResponse: &integration.Response{StatusCode: http.StatusCreated, Body: []byte("core:" + request.Path)},
		}, nil
	})
	integrationextensions.DefaultHost.Configure(integrationextensions.Services{
		Planner:  planner,
		Executor: runtimeExecutor{},
		Start:    integration.RuntimeStartRequest{MaxBodySize: 1024},
	})
	handler := registry.HandlerWithFallback(bridge.NewEdge(planner, runtimeExecutor{}, 1024))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://gateway.test/hooks/before-install", nil))
	if response.Code != http.StatusCreated || response.Body.String() != "core:/hooks/before-install" {
		t.Fatalf("core HTTP before install returned %d %q", response.Code, response.Body.String())
	}
	configuration := json.RawMessage(`{
		"websocket":{"max_connections":1000,"max_duration_ms":86400000},
		"webstream":{"heartbeat_ms":15000,"retry_ms":3000,"max_connections":1000,"max_duration_ms":86400000,"private_network_cidrs":[]}
	}`)
	if err := integrationextensions.LoadExternalInstalledWithConfiguration(context.Background(), registry, installed, configuration); err != nil {
		t.Fatal(err)
	}
	defer registry.Unregister("webong/web")

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://gateway.test/hooks/after-start", nil))
	if response.Code != http.StatusCreated || response.Body.String() != "core:/hooks/after-start" {
		t.Fatalf("installed runtime returned %d %q", response.Code, response.Body.String())
	}
	upgrade := httptest.NewRequest(http.MethodGet, "http://gateway.test/hooks/after-start", nil)
	upgrade.Header.Set("Connection", "Upgrade")
	upgrade.Header.Set("Upgrade", "websocket")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, upgrade)
	if response.Code == http.StatusCreated {
		t.Fatal("installed Web runtime did not claim a WebSocket upgrade")
	}
	stream := httptest.NewRequest(http.MethodGet, "http://gateway.test/hooks/after-start", nil)
	stream.Header.Set("Accept", "text/event-stream")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, stream)
	if response.Code == http.StatusCreated {
		t.Fatal("installed Web runtime did not claim an SSE request")
	}
	if _, err := os.Stat(filepath.Join(destination, "plugin.json")); err != nil {
		t.Fatalf("runtime package was not materialized: %v", err)
	}
}
