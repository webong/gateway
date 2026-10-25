package automationextensions

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/automation"
	"github.com/webong/gateway/arranger/companion"
	companionextensions "github.com/webong/gateway/pkg/spinner/extensions/companion"
)

const staticTestAction = "test.hookscript.execute"

type staticCompanionEngine struct{}

func (staticCompanionEngine) Language() automation.Language { return "companion-test" }

func (staticCompanionEngine) ReverseContracts() []plugin.Contract {
	return []plugin.Contract{{
		ContractRef: companionextensions.Contract,
		Operations:  []plugin.Operation{{Name: staticTestAction}},
	}}
}

func (staticCompanionEngine) Execute(ctx context.Context, _ string, _ automation.Input) (automation.Outcome, error) {
	deadline := time.Now().Add(time.Minute)
	payload, _ := json.Marshal(companion.Invocation{
		ProtocolVersion: companion.ProtocolVersion,
		Action:          staticTestAction, CompanionVersion: "1.0.0",
		ExecutionID: "static-execution", IdempotencyKey: "static-idempotency",
		Deadline: &deadline, Input: map[string]any{"source": "respond('ok')"},
	})
	raw, err := plugin.ReverseFrom(ctx).Call(ctx, companionextensions.Contract, staticTestAction, payload)
	if err != nil {
		return automation.Outcome{}, err
	}
	var result companion.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return automation.Outcome{}, err
	}
	return automation.Outcome{Variables: result.Output}, nil
}

type staticTestRunner struct{}

func (staticTestRunner) Capabilities(context.Context) ([]companion.Capability, error) {
	return []companion.Capability{{Name: staticTestAction, Version: "1.0.0"}}, nil
}

func (staticTestRunner) Invoke(_ context.Context, invocation companion.Invocation) (companion.Result, error) {
	return companion.Result{
		ProtocolVersion: companion.ProtocolVersion,
		ExecutionID:     invocation.ExecutionID,
		Status:          companion.StatusSucceeded,
		Output:          map[string]any{"executed": true},
	}, nil
}

func TestStaticGuestReceivesDeclaredCompanionReverseCalls(t *testing.T) {
	companionextensions.Configure(staticTestRunner{})
	t.Cleanup(func() { companionextensions.Configure(nil) })
	if err := Register(staticCompanionEngine{}); err != nil {
		t.Fatal(err)
	}

	outcome, err := Runtime().Execute(context.Background(), automation.Script{
		Language: "companion-test", Source: "respond('ok')",
	}, automation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Variables["executed"] != true {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
}
