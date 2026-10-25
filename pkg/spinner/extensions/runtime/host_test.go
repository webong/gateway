package runtimeextensions

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/protocol"
	runtimecontract "github.com/webong/gateway/arranger/runtime"
)

type plannerFunc func(context.Context, protocol.IngressRequest) (protocol.RoutePlan, error)

func (f plannerFunc) Plan(ctx context.Context, request protocol.IngressRequest) (protocol.RoutePlan, error) {
	return f(ctx, request)
}

func TestRuntimeHostExecutesOnlyConfiguredDeclaredService(t *testing.T) {
	host := NewHost()
	host.Configure(Services{Planner: plannerFunc(func(_ context.Context, request protocol.IngressRequest) (protocol.RoutePlan, error) {
		return protocol.RoutePlan{Version: protocol.ProtocolVersion, Action: protocol.ActionRespond, Metadata: map[string]string{"path": request.Path}, ImmediateResponse: &protocol.Response{StatusCode: http.StatusNoContent}}, nil
	})})
	payload, _ := json.Marshal(protocol.IngressRequest{Method: http.MethodPost, Path: "/hooks/example"})
	request := plugin.Request{
		Contract: runtimecontract.PlannerContract, Operation: runtimecontract.OperationPlanHTTP,
		Deadline: time.Now().Add(time.Second), Payload: payload,
	}
	if err := host.authorize(request); err != nil {
		t.Fatal(err)
	}
	result, err := host.handle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var plan protocol.RoutePlan
	if err := json.Unmarshal(result, &plan); err != nil || plan.Metadata["path"] != "/hooks/example" {
		t.Fatalf("unexpected plan: %+v err=%v", plan, err)
	}
	request.Operation = runtimecontract.OperationPassThrough
	if err := host.authorize(request); err == nil {
		t.Fatal("host authorized an unconfigured pass-through service")
	}
}

func TestRuntimeHostRejectsInternalPlannerPassThrough(t *testing.T) {
	host := NewHost()
	host.Configure(Services{PassThrough: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("internal request reached Planner")
	})})
	payload, _ := json.Marshal(runtimecontract.HTTPRequest{Method: http.MethodGet, Scheme: "http", Host: "gateway.test", Path: "/_internal/gateway/ready"})
	_, err := host.handle(context.Background(), plugin.Request{
		Contract: runtimecontract.PlannerContract, Operation: runtimecontract.OperationPassThrough,
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
