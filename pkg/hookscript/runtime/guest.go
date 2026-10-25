package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/automation"
)

var automationContract = plugin.ContractRef{Name: "gateway.automation", Version: "v1"}

type executeRequest struct {
	Language  string         `json:"language"`
	Source    string         `json:"source"`
	Event     map[string]any `json:"event"`
	Variables map[string]any `json:"variables"`
}

type executeResponse struct {
	Outcome automation.Outcome `json:"outcome"`
}

// Guest exposes HookScript through the portable automation process contract.
// The reviewed package descriptor remains the authority for identity, forward
// operations, and the one companion reverse action the guest may call.
func Guest(descriptor plugin.Descriptor) (*plugin.Guest, error) {
	if !declaresOperation(descriptor.Contracts, automationContract, "execute", string(Language)) {
		return nil, errors.New("hookscript descriptor does not declare its automation operation")
	}
	if !declaresOperation(descriptor.Reverse, companionContract, CompanionAction, "") {
		return nil, errors.New("hookscript descriptor does not declare its companion action")
	}
	engine := New()
	return plugin.NewGuest(descriptor, plugin.GuestOptions{Handler: func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
		var input executeRequest
		if err := json.Unmarshal(request.Payload, &input); err != nil {
			return nil, fmt.Errorf("decode hookscript automation request: %w", err)
		}
		if input.Language != "" && input.Language != string(Language) {
			return nil, errors.New("hookscript automation language does not match the guest")
		}
		outcome, err := engine.Execute(ctx, input.Source, automation.Input{Event: input.Event, Variables: input.Variables})
		if err != nil {
			return nil, err
		}
		return json.Marshal(executeResponse{Outcome: outcome})
	}})
}

func declaresOperation(contracts []plugin.Contract, ref plugin.ContractRef, name, surface string) bool {
	for _, declared := range contracts {
		if declared.ContractRef != ref {
			continue
		}
		for _, operation := range declared.Operations {
			if operation.Name == name && (surface == "" || operation.Surface == surface) {
				return true
			}
		}
	}
	return false
}
