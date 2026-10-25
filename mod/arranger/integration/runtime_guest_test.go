package integration

import (
	"context"
	"encoding/json"
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
