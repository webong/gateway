package companionextensions

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/inprocess"
	"github.com/webong/gateway/arranger/companion"
)

type recordingRunner struct {
	invocation companion.Invocation
}

func (r *recordingRunner) Capabilities(context.Context) ([]companion.Capability, error) {
	return []companion.Capability{{Name: "email.create-ticket", Version: "1.0.0"}}, nil
}

func (r *recordingRunner) Invoke(_ context.Context, invocation companion.Invocation) (companion.Result, error) {
	r.invocation = invocation
	return companion.Result{ProtocolVersion: companion.ProtocolVersion, ExecutionID: invocation.ExecutionID,
		Status: companion.StatusSucceeded, Output: map[string]any{"ticket_id": "ticket-1"}}, nil
}

func TestPluginReverseCallInvokesOnlyDeclaredCompanionAction(t *testing.T) {
	mainContract := plugin.ContractRef{Name: "example.extension", Version: "v1"}
	descriptor := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "example/ticketing", Revision: "test"},
		Contracts:  []plugin.Contract{{ContractRef: mainContract, Operations: []plugin.Operation{{Name: "run"}}}},
		Reverse:    []plugin.Contract{{ContractRef: Contract, Operations: []plugin.Operation{{Name: "email.create-ticket"}}}},
	}
	guest, err := plugin.NewGuest(descriptor, plugin.GuestOptions{Handler: func(ctx context.Context, _ plugin.Request) (json.RawMessage, error) {
		deadline := time.Now().Add(time.Minute)
		payload, _ := json.Marshal(companion.Invocation{
			ProtocolVersion: companion.ProtocolVersion, Action: "email.create-ticket", CompanionVersion: "1.0.0",
			ExecutionID: "execution-1", IdempotencyKey: "message-1", Deadline: &deadline,
			Input: map[string]any{"subject": "Help"},
		})
		return plugin.ReverseFrom(ctx).Call(ctx, Contract, "email.create-ticket", payload)
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := inprocess.New(guest)
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	host := NewHost()
	host.SetRunner(runner)
	session, err := plugin.Open(context.Background(), descriptor, plugin.Options{
		Verify:    func(context.Context, plugin.Descriptor) error { return nil },
		Connect:   func(context.Context, plugin.Descriptor) (plugin.Backend, error) { return backend, nil },
		Authorize: func(context.Context, plugin.Request) error { return nil },
		Reverse:   host.ReverseOptions(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = session.Close(ctx)
	})

	raw, err := session.Call(context.Background(), mainContract, "run", nil)
	if err != nil {
		t.Fatal(err)
	}
	var result companion.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Output["ticket_id"] != "ticket-1" || runner.invocation.Action != "email.create-ticket" {
		t.Fatalf("unexpected companion round trip: %+v / %+v", result, runner.invocation)
	}
}

func TestPluginReversePolicyRejectsActionSubstitution(t *testing.T) {
	host := NewHost()
	deadline := time.Now().Add(time.Minute)
	payload, _ := json.Marshal(companion.Invocation{
		ProtocolVersion: companion.ProtocolVersion, Action: "dns.change-zone", CompanionVersion: "1.0.0",
		ExecutionID: "execution-1", IdempotencyKey: "message-1", Deadline: &deadline, Input: map[string]any{},
	})
	err := host.authorize(context.Background(), plugin.Request{
		Contract: Contract, Operation: "email.create-ticket", Payload: payload,
	})
	if err == nil {
		t.Fatal("plugin substituted an undeclared companion action")
	}
}
