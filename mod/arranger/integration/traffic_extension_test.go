package integration_test

import (
	"testing"

	"github.com/webong/gateway/arranger/integration"
)

func TestSharedContractAcceptsExtensionOwnedProtocolNames(t *testing.T) {
	decision := integration.GatewayDecision{
		Version:  integration.ProtocolVersion,
		Protocol: integration.Protocol("example-protocol"),
		Action:   integration.GatewaySubmit,
		Deliveries: []integration.GatewayDelivery{{
			Protocol: integration.Protocol("example-protocol"),
			Target:   "example-target",
		}},
	}
	if err := decision.Validate(); err != nil {
		t.Fatalf("extension-owned protocol was rejected by Core: %v", err)
	}
}

func TestSharedContractRejectsUnsafeExtensionProtocolNames(t *testing.T) {
	decision := integration.GatewayDecision{
		Version:  integration.ProtocolVersion,
		Protocol: integration.Protocol("../../unsafe"),
		Action:   integration.GatewayAccept,
	}
	if err := decision.Validate(); err == nil {
		t.Fatal("unsafe extension protocol name was accepted")
	}
}

func TestSharedContractAcceptsExtensionOwnedEventKinds(t *testing.T) {
	event := integration.GatewayEvent{
		ID: "event-1", Protocol: integration.Protocol("example-protocol"),
		Kind: integration.EventKind("example-event"), Route: "example-route",
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("extension-owned event kind was rejected by Core: %v", err)
	}
}
