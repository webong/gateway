package integrationextensions

import (
	"bytes"
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
	if err := host.authorize(plugin.Identity{}, request); err != nil {
		t.Fatal(err)
	}
	result, err := host.handle(context.Background(), plugin.Identity{}, request)
	if err != nil {
		t.Fatal(err)
	}
	var plan integration.RoutePlan
	if err := json.Unmarshal(result, &plan); err != nil || plan.Metadata["path"] != "/hooks/example" {
		t.Fatalf("unexpected plan: %+v err=%v", plan, err)
	}
	request.Operation = integration.OperationPassThrough
	if err := host.authorize(plugin.Identity{}, request); err == nil {
		t.Fatal("host authorized an unconfigured pass-through service")
	}
}

func TestIntegrationHostRejectsInternalPlannerPassThrough(t *testing.T) {
	host := NewHost()
	host.Configure(Services{PassThrough: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("internal request reached Planner")
	})})
	payload, _ := json.Marshal(integration.PassThroughRequest{Method: http.MethodGet, Scheme: "http", Host: "gateway.test", Path: "/_internal/gateway/ready"})
	_, err := host.handle(context.Background(), plugin.Identity{}, plugin.Request{
		Contract: integration.PlannerContract, Operation: integration.OperationPassThrough,
		Deadline: time.Now().Add(time.Second), Payload: payload,
	})
	if err == nil {
		t.Fatal("internal Planner path was accepted")
	}
}

func TestIntegrationHostScopesPrivatePlannerPassThroughToPackageIdentity(t *testing.T) {
	called := false
	host := NewHost()
	host.Configure(Services{InternalToken: "host-secret", PassThrough: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		if request.Header.Get("X-Gateway-Internal") != "host-secret" {
			t.Errorf("private Planner token = %q", request.Header.Get("X-Gateway-Internal"))
		}
		writer.WriteHeader(http.StatusNoContent)
	})})
	identity := plugin.Identity{ID: "webong/email", Revision: "r1"}
	private := func(path string) plugin.Request {
		payload, _ := json.Marshal(integration.PassThroughRequest{Method: http.MethodPost, Scheme: "http", Host: "gateway.test", Path: path})
		return plugin.Request{Contract: integration.PlannerContract, Operation: integration.OperationPrivatePassThrough, Payload: payload}
	}
	request := private("/_internal/gateway/extensions/webong/email/mailbox-polls/one/claim")
	if err := host.authorize(identity, request); err != nil {
		t.Fatal(err)
	}
	if _, err := host.handle(context.Background(), identity, request); err != nil || !called {
		t.Fatalf("own private Planner route was unavailable: %v", err)
	}
	called = false
	if _, err := host.handle(context.Background(), identity, private("/_internal/gateway/extensions/webong/dns/zones")); err == nil || called {
		t.Fatalf("cross-extension private route was accepted: %v", err)
	}
}

func TestIntegrationHostClonesPlannerOwnedConfiguration(t *testing.T) {
	host := NewHost()
	host.Configure(Services{InternalToken: "host-internal-token"})
	configuration := json.RawMessage(`{"listener":{"address":"127.0.0.1:0"}}`)
	identity := plugin.Identity{ID: "webong/email", Revision: "r1", Version: "1.0.0"}
	request := host.StartRequest(identity, configuration)
	configuration[2] = 'X'

	if string(request.Configuration) != `{"listener":{"address":"127.0.0.1:0"}}` {
		t.Fatalf("integration configuration was not isolated from the desired-state buffer: %s", request.Configuration)
	}
	other := host.StartRequest(plugin.Identity{ID: "webong/web", Revision: "r1", Version: "1.0.0"}, nil)
	if len(request.InstanceKey) != 32 || bytes.Equal(request.InstanceKey, []byte("host-internal-token")) || bytes.Equal(request.InstanceKey, other.InstanceKey) {
		t.Fatal("runtime instance key was not opaque and identity-scoped")
	}
}

func TestIntegrationHostResolvesOnlyItsIdentityScopedSecret(t *testing.T) {
	host := NewHost()
	host.Configure(Services{InternalToken: "host-secret", PassThrough: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/_internal/gateway/extensions/webong/email/secrets/primary-relay" ||
			request.Header.Get("X-Gateway-Internal") != "host-secret" {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(integration.SecretValue{
			Reference: "primary-relay", Version: 2, Value: json.RawMessage(`{"username":"relay-user","password":"relay-secret"}`),
		})
	})})
	identity := plugin.Identity{ID: "webong/email", Revision: "r1"}
	payload, _ := json.Marshal(integration.SecretRequest{Reference: "primary-relay"})
	request := plugin.Request{Contract: integration.SecretContract, Operation: integration.OperationResolveSecret, Payload: payload}
	if err := host.authorize(identity, request); err != nil {
		t.Fatal(err)
	}
	result, err := host.handle(context.Background(), identity, request)
	if err != nil {
		t.Fatal(err)
	}
	var secret integration.SecretValue
	if err := json.Unmarshal(result, &secret); err != nil || secret.Reference != "primary-relay" || secret.Version != 2 ||
		!bytes.Contains(secret.Value, []byte("relay-secret")) {
		t.Fatalf("unexpected secret response: %+v err=%v", secret, err)
	}
	other := plugin.Identity{ID: "webong/dns", Revision: "r1"}
	if _, err := host.handle(context.Background(), other, request); err == nil {
		t.Fatal("another Extension identity resolved Email's secret")
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
	if err := host.authorize(plugin.Identity{}, request); err != nil {
		t.Fatal(err)
	}
	if _, err := host.handle(context.Background(), plugin.Identity{}, request); err != nil {
		t.Fatal(err)
	}
	if recorded.ID != observation.ID || recorded.Attributes["mode"] != "forwarded" {
		t.Fatalf("unexpected recorded observation: %+v", recorded)
	}

	observation.Version = "v2"
	payload, _ = json.Marshal(observation)
	request.Payload = payload
	if _, err := host.handle(context.Background(), plugin.Identity{}, request); err == nil {
		t.Fatal("invalid observation was accepted")
	}
}

type observationSinkFunc func(context.Context, integration.Observation) error

func (function observationSinkFunc) RecordObservation(ctx context.Context, observation integration.Observation) error {
	return function(ctx, observation)
}
