package automation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	driver "github.com/webong/gateway/driver/automation"
)

type testEngine struct{}

func (testEngine) Language() driver.Language { return "test" }
func (testEngine) Execute(_ context.Context, source string, input driver.Input) (driver.Outcome, error) {
	return driver.Outcome{Variables: map[string]any{"source": source, "event": input.Event["id"]}}, nil
}

func TestGuestUsesDescriptorIdentityAndEngineLanguage(t *testing.T) {
	descriptor := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "example/test", Revision: "r1", Version: "1.0.0"},
		Contracts: []plugin.Contract{{
			ContractRef: automationContract,
			Operations:  []plugin.Operation{{Name: "execute", Surface: "test"}},
		}},
	}
	guest, err := Guest(descriptor, func() (driver.Engine, error) { return testEngine{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(executeRequest{Language: "test", Source: "source", Event: map[string]any{"id": "event-1"}})
	response, err := guest.Invoke(context.Background(), plugin.Request{
		APIVersion: plugin.APIVersion,
		ID:         "request-1",
		Plugin:     descriptor.Identity,
		Contract:   automationContract,
		Operation:  "execute",
		Surface:    "test",
		Deadline:   time.Now().Add(time.Second),
		Payload:    payload,
	})
	if err != nil || response.Error != nil {
		t.Fatalf("invoke automation guest: response=%+v err=%v", response, err)
	}
	var result executeResponse
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome.Variables["source"] != "source" || result.Outcome.Variables["event"] != "event-1" {
		t.Fatalf("unexpected outcome: %+v", result.Outcome)
	}
}

func TestGuestRejectsUndeclaredLanguage(t *testing.T) {
	_, err := Guest(plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "example/test", Revision: "r1"},
		Contracts:  []plugin.Contract{{ContractRef: automationContract, Operations: []plugin.Operation{{Name: "execute", Surface: "other"}}}},
	}, func() (driver.Engine, error) { return testEngine{}, nil })
	if err == nil {
		t.Fatal("undeclared automation language was accepted")
	}
}
