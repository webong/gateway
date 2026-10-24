package runtime

import (
	"context"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

func TestGuestRequiresAutomationAndCompanionDeclarations(t *testing.T) {
	if Language != "webhookscript" {
		t.Fatalf("HookScript language must match the Planner language ID: %q", Language)
	}
	descriptor := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "webong/hookscript", Revision: "test-r1", Version: "1.0.0"},
		Contracts: []plugin.Contract{{
			ContractRef: automationContract,
			Operations:  []plugin.Operation{{Name: "execute", Surface: string(Language)}},
		}},
		Reverse: []plugin.Contract{{
			ContractRef: companionContract,
			Operations:  []plugin.Operation{{Name: CompanionAction}},
		}},
	}
	guest, err := Guest(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := guest.Handshake(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if actual.Identity != descriptor.Identity {
		t.Fatalf("unexpected identity: %+v", actual.Identity)
	}

	descriptor.Reverse = nil
	if _, err := Guest(descriptor); err == nil {
		t.Fatal("expected a missing companion declaration to be rejected")
	}
}
