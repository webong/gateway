package gateway

import (
	"net/http"
	"testing"
)

func TestHTTPIngressMapsToProtocolNeutralGatewayEvent(t *testing.T) {
	ingress := IngressRequest{
		DeliveryID: "delivery-1",
		Protocol:   ProtocolHTTP,
		Event:      EventRequest,
		Method:     http.MethodPost,
		Host:       "hooks.example.test",
		Path:       "/provider/events",
		RawQuery:   "source=test",
		Headers:    map[string][]string{"X-Event": {"created"}},
		Body:       []byte(`{"event":"created"}`),
	}

	event := GatewayEventFromHTTP(ingress)
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	if event.Protocol != ProtocolHTTP || event.Kind != EventRequest || event.Route != ingress.Path || string(event.Payload) != string(ingress.Body) {
		t.Fatalf("unexpected gateway event: %+v", event)
	}
}
func TestGatewayDecisionRequiresTypedDeliveryDestinations(t *testing.T) {
	decision := GatewayDecision{
		Version:  ProtocolVersion,
		Protocol: ProtocolWebSocket,
		Action:   GatewayDeliver,
		Deliveries: []GatewayDelivery{{
			Protocol: ProtocolWebSocket,
			Target:   "channel:customer-42",
		}},
	}

	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}

	decision.Deliveries = nil
	if err := decision.Validate(); err == nil {
		t.Fatal("expected deliver decision without destinations to fail")
	}
}

func TestGatewayEventLetsTheOwningExtensionChooseEventKinds(t *testing.T) {
	event := GatewayEvent{
		ID:       "event-1",
		Protocol: ProtocolHTTP,
		Kind:     EventMessage,
		Route:    "/events",
	}

	if err := event.Validate(); err != nil {
		t.Fatalf("Core rejected an Extension-owned event kind: %v", err)
	}
}

func TestGatewayEventValidatesSafeKindsWithoutKnowingTheProtocol(t *testing.T) {
	event := GatewayEvent{
		ID:       "dns-event-1",
		Protocol: ProtocolDNS,
		Kind:     EventQuery,
		Route:    "/hook-1",
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	event.Kind = EventMessage
	if err := event.Validate(); err != nil {
		t.Fatalf("Core rejected a safe event kind: %v", err)
	}
}

func TestSMTPGatewayEventAllowsRecipientKind(t *testing.T) {
	event := GatewayEvent{ID: "recipient-1", Protocol: ProtocolSMTP, Kind: EventRecipient, Route: "user@example.test"}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayDecisionAllowsSynchronousReply(t *testing.T) {
	decision := GatewayDecision{
		Version:  ProtocolVersion,
		Protocol: ProtocolDNS,
		Action:   GatewayDeliver,
		Reply:    &GatewayDelivery{Protocol: ProtocolHTTP, Target: "https://reply.example.test/dns"},
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayDecisionRequiresOneGenericSubmitEnvelope(t *testing.T) {
	decision := GatewayDecision{
		Version: ProtocolVersion, Protocol: ProtocolSMTP, Action: GatewaySubmit,
		Deliveries: []GatewayDelivery{{Protocol: ProtocolSMTP, Target: "first@example.net"}},
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
	decision.Deliveries = append(decision.Deliveries, GatewayDelivery{Protocol: ProtocolSMTP, Target: "second@example.net"})
	if err := decision.Validate(); err == nil {
		t.Fatal("submit accepted multiple separate relay transactions")
	}
	decision.Deliveries = decision.Deliveries[:1]
	decision.Deliveries[0].Protocol = ProtocolHTTP
	if err := decision.Validate(); err != nil {
		t.Fatalf("Core rejected an Extension-owned submit protocol: %v", err)
	}
}

func TestGatewayDecisionQuarantineHasNoDelivery(t *testing.T) {
	decision := GatewayDecision{Version: ProtocolVersion, Protocol: ProtocolSMTP, Action: GatewayQuarantine}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
	decision.Protocol = ProtocolHTTP
	if err := decision.Validate(); err != nil {
		t.Fatalf("Core rejected an Extension-owned quarantine protocol: %v", err)
	}
	decision.Protocol = ProtocolSMTP
	decision.Deliveries = []GatewayDelivery{{Protocol: ProtocolSMTP, Target: "recipient@example.test"}}
	if err := decision.Validate(); err == nil {
		t.Fatal("quarantine accepted an outbound delivery")
	}
}

func TestGatewayDecisionRequiresOneGenericStreamSource(t *testing.T) {
	decision := GatewayDecision{
		Version: ProtocolVersion, Protocol: ProtocolWebstream, Action: GatewayStream,
		Stream: &GatewayDelivery{Protocol: ProtocolHTTP, Target: "https://events.example.test/stream"},
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
	decision.Stream.Protocol = ProtocolSMTP
	if err := decision.Validate(); err != nil {
		t.Fatalf("Core rejected an Extension-owned stream source: %v", err)
	}
}

func TestGatewayDeliveryRejectsUnsafeAdapterNames(t *testing.T) {
	delivery := GatewayDelivery{
		Protocol: ProtocolSMTP,
		Adapter:  "../../plugin",
		Target:   "account-42",
	}
	if err := delivery.Validate(); err == nil {
		t.Fatal("expected unsafe adapter name to fail validation")
	}
}
