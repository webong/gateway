package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	protocol "github.com/webong/gateway/arranger/protocol"
	runtimecontract "github.com/webong/gateway/arranger/runtime"
	runtimeextensions "github.com/webong/gateway/pkg/spinner/extensions/runtime"
)

type runtimePlanner func(context.Context, protocol.IngressRequest) (protocol.RoutePlan, error)

func (f runtimePlanner) Plan(ctx context.Context, request protocol.IngressRequest) (protocol.RoutePlan, error) {
	return f(ctx, request)
}

type runtimeExecutor struct{}

func (runtimeExecutor) Deliver(context.Context, protocol.Delivery) (protocol.Response, error) {
	return protocol.Response{}, nil
}
func (runtimeExecutor) Enqueue(protocol.Delivery) bool               { return true }
func (runtimeExecutor) EnqueueGateway(protocol.GatewayDelivery) bool { return true }

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

	registry := runtimeextensions.NewRegistry()
	runtimeextensions.DefaultHost.Configure(runtimeextensions.Services{
		Planner: runtimePlanner(func(_ context.Context, request protocol.IngressRequest) (protocol.RoutePlan, error) {
			return protocol.RoutePlan{
				Version: protocol.ProtocolVersion, Action: protocol.ActionRespond,
				ImmediateResponse: &protocol.Response{StatusCode: http.StatusCreated, Body: []byte("runtime:" + request.Path)},
			}, nil
		}),
		Executor: runtimeExecutor{},
		Start:    runtimecontract.StartRequest{MaxBodySize: 1024},
	})
	if err := runtimeextensions.LoadExternalInstalled(context.Background(), registry, installed); err != nil {
		t.Fatal(err)
	}
	defer registry.Unregister("webong/web")

	response := httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://gateway.test/hooks/after-start", nil))
	if response.Code != http.StatusCreated || response.Body.String() != "runtime:/hooks/after-start" {
		t.Fatalf("installed runtime returned %d %q", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(destination, "plugin.json")); err != nil {
		t.Fatalf("runtime package was not materialized: %v", err)
	}
}
