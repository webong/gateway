package integrationextensions

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

func TestIntegrationHostExecutesOnlyConfiguredDeclaredService(t *testing.T) {
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

func TestIntegrationHostRejectsInternalPlannerPassThrough(t *testing.T) {
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

func TestIntegrationHostClonesPlannerOwnedConfiguration(t *testing.T) {
	host := NewHost()
	configuration := json.RawMessage(`{"listener":{"address":"127.0.0.1:0"}}`)
	request := host.StartRequest(configuration)
	configuration[2] = 'X'

	if string(request.Configuration) != `{"listener":{"address":"127.0.0.1:0"}}` {
		t.Fatalf("integration configuration was not isolated from the desired-state buffer: %s", request.Configuration)
	}
}

func TestIntegrationHostRecordsOnlyValidatedObservations(t *testing.T) {
	var recorded integration.Observation
	host := NewHost()
	host.Configure(Services{Observations: observationSinkFunc(func(_ context.Context, observation integration.Observation) error {
		recorded = observation
		return nil
	})})
	observation := integration.Observation{
		Version: integration.ObservationVersion, ID: "dns-query-1", Type: "dns.query",
		OccurredAt: time.Now().UTC(), Attributes: map[string]string{"mode": "forwarded"},
	}
	payload, _ := json.Marshal(observation)
	request := plugin.Request{Contract: integration.ObservationContract, Operation: integration.OperationRecordObservation, Payload: payload}
	if err := host.authorize(request); err != nil {
		t.Fatal(err)
	}
	if _, err := host.handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if recorded.ID != observation.ID || recorded.Attributes["mode"] != "forwarded" {
		t.Fatalf("unexpected recorded observation: %+v", recorded)
	}

	observation.Version = "v2"
	payload, _ = json.Marshal(observation)
	request.Payload = payload
	if _, err := host.handle(context.Background(), request); err == nil {
		t.Fatal("invalid observation was accepted")
	}
}

type observationSinkFunc func(context.Context, integration.Observation) error

func (function observationSinkFunc) RecordObservation(ctx context.Context, observation integration.Observation) error {
	return function(ctx, observation)
}
