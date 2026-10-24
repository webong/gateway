package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/driver/automation"
	"github.com/webong/gateway/driver/companion"
)

type reverseCallerFunc func(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)

func (f reverseCallerFunc) Call(ctx context.Context, contract plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	return f(ctx, contract, operation, payload)
}

func TestEngineExecutesThroughDeclaredPHPCompanion(t *testing.T) {
	var captured companion.Invocation
	caller := reverseCallerFunc(func(_ context.Context, contract plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
		if contract != companionContract || operation != CompanionAction {
			t.Fatalf("unexpected reverse call: %+v %q", contract, operation)
		}
		if err := json.Unmarshal(payload, &captured); err != nil {
			t.Fatal(err)
		}
		result := companion.Result{
			ProtocolVersion: companion.ProtocolVersion,
			ExecutionID:     captured.ExecutionID,
			Status:          companion.StatusSucceeded,
			Output: map[string]any{"outcome": map[string]any{
				"variables":  map[string]any{"ticket": "created"},
				"deliveries": []any{map[string]any{"url": "https://example.test/ticket", "method": "POST", "body": "payload"}},
				"response":   map[string]any{"status": 202, "body": "accepted"},
			}},
		}
		return json.Marshal(result)
	})
	ctx := plugin.WithReverse(context.Background(), caller)
	outcome, err := New().Execute(ctx, "respond('accepted', 202);", automation.Input{
		Event:     map[string]any{"delivery_id": "delivery-1"},
		Variables: map[string]any{"request": map[string]any{"content": "payload"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Action != CompanionAction || captured.CompanionVersion != CompanionVersion || captured.Deadline == nil || !captured.Deadline.After(time.Now()) {
		t.Fatalf("unexpected companion invocation: %+v", captured)
	}
	if outcome.Response == nil || outcome.Response.Status != 202 || outcome.Response.Body != "accepted" || len(outcome.Deliveries) != 1 {
		t.Fatalf("unexpected automation outcome: %+v", outcome)
	}
	if outcome.Variables["ticket"] != "created" {
		t.Fatalf("unexpected variables: %+v", outcome.Variables)
	}
}

func TestEngineFailsClosedWithoutCompanionHost(t *testing.T) {
	_, err := New().Execute(context.Background(), "respond('ok');", automation.Input{})
	if err == nil {
		t.Fatal("hookscript executed without a companion host")
	}
}

func TestEngineDeclaresOnlyHookScriptCompanion(t *testing.T) {
	contracts := New().ReverseContracts()
	if len(contracts) != 1 || contracts[0].ContractRef != companionContract || len(contracts[0].Operations) != 1 || contracts[0].Operations[0].Name != CompanionAction {
		t.Fatalf("unexpected reverse contracts: %+v", contracts)
	}
}
