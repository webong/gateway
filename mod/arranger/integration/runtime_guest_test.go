package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

type reverseCallerFunc func(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)

func (f reverseCallerFunc) Call(ctx context.Context, ref plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	return f(ctx, ref, operation, payload)
}

func TestGuestStartsOnceWithSessionScopedHost(t *testing.T) {
	descriptor := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "example/web", Revision: "r1"},
		Contracts:  []plugin.Contract{{ContractRef: RuntimeContract, Operations: []plugin.Operation{{Name: OperationStart, Surface: "spinner"}}}},
		Reverse:    []plugin.Contract{{ContractRef: PlannerContract, Operations: []plugin.Operation{{Name: OperationPlanEvent}}}},
	}
	started := 0
	guest, err := RuntimeGuest(descriptor, RuntimeStarterFunc(func(_ context.Context, request RuntimeStartRequest, caller plugin.ReverseCaller) (RuntimeStartResponse, error) {
		started++
		if request.MaxBodySize != 1024 || string(request.Configuration) != `{"listener":{"address":"127.0.0.1:0"}}` || caller == nil {
			t.Fatalf("unexpected start request: %+v caller=%v", request, caller)
		}
		return RuntimeStartResponse{HTTP: []HTTPMount{{Prefix: "/", Address: "http://127.0.0.1:1234"}}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(RuntimeStartRequest{
		Configuration: json.RawMessage(`{"listener":{"address":"127.0.0.1:0"}}`),
		MaxBodySize:   1024,
	})
	request := plugin.Request{
		APIVersion: plugin.APIVersion, ID: "request-1", Plugin: descriptor.Identity,
		Contract: RuntimeContract, Operation: OperationStart, Surface: "spinner",
		Deadline: time.Now().Add(time.Second), Payload: payload,
	}
	ctx := plugin.WithSessionReverse(context.Background(), reverseCallerFunc(func(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage("null"), nil
	}))
	response, err := guest.Invoke(ctx, request)
	if err != nil || response.Error != nil {
		t.Fatalf("start runtime: response=%+v err=%v", response, err)
	}
	var result RuntimeStartResponse
	if err := json.Unmarshal(response.Payload, &result); err != nil || len(result.HTTP) != 1 || result.HTTP[0].Address != "http://127.0.0.1:1234" || result.HTTP[0].Prefix != "/" {
		t.Fatalf("unexpected advertisement: %+v err=%v", result, err)
	}
	response, err = guest.Invoke(ctx, request)
	if err != nil || response.Error == nil || started != 1 {
		t.Fatalf("second start did not fail closed: response=%+v err=%v started=%d", response, err, started)
	}
}

func TestGuestRequiresDeclaredRuntimeContract(t *testing.T) {
	_, err := RuntimeGuest(plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "example/web", Revision: "r1"},
		Contracts:  []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "example.other", Version: "v1"}, Operations: []plugin.Operation{{Name: "run"}}}},
	}, RuntimeStarterFunc(func(context.Context, RuntimeStartRequest, plugin.ReverseCaller) (RuntimeStartResponse, error) {
		return RuntimeStartResponse{}, nil
	}))
	if err == nil {
		t.Fatal("guest accepted a descriptor without gateway.runtime/v1")
	}
}

func TestRuntimeHostResolvesTypedSecretThroughDeclaredReverseContract(t *testing.T) {
	caller := reverseCallerFunc(func(_ context.Context, ref plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
		if ref != SecretContract || operation != OperationResolveSecret {
			t.Fatalf("unexpected reverse call %s@%s/%s", ref.Name, ref.Version, operation)
		}
		var request SecretRequest
		if err := json.Unmarshal(payload, &request); err != nil || request.Reference != "provider-token" {
			t.Fatalf("unexpected secret request: %+v err=%v", request, err)
		}
		return json.Marshal(SecretValue{Reference: request.Reference, Version: 3, Value: json.RawMessage(`{"token":"private"}`)})
	})
	secret, err := NewRuntimeHost(caller, nil).Secrets().Resolve(context.Background(), "provider-token")
	if err != nil || secret.Version != 3 || string(secret.Value) != `{"token":"private"}` {
		t.Fatalf("unexpected secret: %+v err=%v", secret, err)
	}
	if _, err := NewRuntimeHost(caller, nil).Secrets().Resolve(context.Background(), "../other"); err == nil {
		t.Fatal("invalid secret reference reached the host")
	}
}

func TestPrivatePlannerPassThroughAcceptsBodylessGET(t *testing.T) {
	var forwarded PassThroughRequest
	caller := reverseCallerFunc(func(_ context.Context, ref plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
		if ref != PlannerContract || operation != OperationPrivatePassThrough {
			return nil, fmt.Errorf("unexpected reverse call %s@%s/%s", ref.Name, ref.Version, operation)
		}
		if err := json.Unmarshal(payload, &forwarded); err != nil {
			return nil, err
		}
		return json.Marshal(PassThroughResponse{
			Status:  http.StatusOK,
			Headers: http.Header{"Content-Type": {"application/json"}},
			Body:    []byte(`{"mailbox":"ready"}`),
		})
	})
	handler := NewRuntimeHost(caller, nil).PrivatePlanner()
	request := httptest.NewRequest(http.MethodGet, "http://planner.internal/_internal/gateway/extensions/webong/email/mailbox-connections/11111111-1111-4111-8111-111111111111/smtp?fresh=1", nil)
	request.Body = nil
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != `{"mailbox":"ready"}` {
		t.Fatalf("private Planner response = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("private Planner content type = %q", response.Header().Get("Content-Type"))
	}
	if forwarded.Method != http.MethodGet || forwarded.Scheme != "http" || forwarded.Host != "planner.internal" ||
		forwarded.Path != request.URL.Path || forwarded.RawQuery != "fresh=1" || len(forwarded.Body) != 0 {
		t.Fatalf("forwarded private Planner request = %+v", forwarded)
	}
}
