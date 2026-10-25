package runtimeextensions

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/integration"
)

type plannerFunc func(context.Context, integration.IngressRequest) (integration.RoutePlan, error)

func (f plannerFunc) Plan(ctx context.Context, request integration.IngressRequest) (integration.RoutePlan, error) {
	return f(ctx, request)
}

func TestRuntimeHostExecutesOnlyConfiguredDeclaredService(t *testing.T) {
	host := NewHost()
	host.Configure(Services{Planner: plannerFunc(func(_ context.Context, request integration.IngressRequest) (integration.RoutePlan, error) {
		return integration.RoutePlan{Version: integration.ProtocolVersion, Action: integration.ActionRespond, Metadata: map[string]string{"path": request.Path}, ImmediateResponse: &integration.Response{StatusCode: http.StatusNoContent}}, nil
	})})
	payload, _ := json.Marshal(integration.IngressRequest{Method: http.MethodPost, Path: "/hooks/example"})
	request := plugin.Request{
		Contract: integration.PlannerContract, Operation: integration.OperationPlanHTTP,
		Deadline: time.Now().Add(time.Second), Payload: payload,
	}
	if err := host.authorize(request); err != nil {
		t.Fatal(err)
	}
	result, err := host.handle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var plan integration.RoutePlan
	if err := json.Unmarshal(result, &plan); err != nil || plan.Metadata["path"] != "/hooks/example" {
		t.Fatalf("unexpected plan: %+v err=%v", plan, err)
	}
	request.Operation = integration.OperationPassThrough
	if err := host.authorize(request); err == nil {
		t.Fatal("host authorized an unconfigured pass-through service")
	}
}

func TestRuntimeHostRejectsInternalPlannerPassThrough(t *testing.T) {
	host := NewHost()
	host.Configure(Services{PassThrough: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("internal request reached Planner")
	})})
	payload, _ := json.Marshal(integration.PassThroughRequest{Method: http.MethodGet, Scheme: "http", Host: "gateway.test", Path: "/_internal/gateway/ready"})
	_, err := host.handle(context.Background(), plugin.Request{
		Contract: integration.PlannerContract, Operation: integration.OperationPassThrough,
		Deadline: time.Now().Add(time.Second), Payload: payload,
	})
	if err == nil {
		t.Fatal("internal Planner path was accepted")
	}
}

func TestRuntimeHostClonesPlannerOwnedConfiguration(t *testing.T) {
	host := NewHost()
	configuration := json.RawMessage(`{"listener":{"address":"127.0.0.1:0"}}`)
	request := host.StartRequest(configuration)
	configuration[2] = 'X'

	if string(request.Configuration) != `{"listener":{"address":"127.0.0.1:0"}}` {
		t.Fatalf("runtime configuration was not isolated from the desired-state buffer: %s", request.Configuration)
	}
}
