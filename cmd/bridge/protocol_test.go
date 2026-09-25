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

func TestHTTPGatewayEventRejectsSessionKinds(t *testing.T) {
	event := GatewayEvent{
		ID:       "event-1",
		Protocol: ProtocolHTTP,
		Kind:     EventMessage,
		Route:    "/events",
	}

	if err := event.Validate(); err == nil {
		t.Fatal("expected HTTP message event to fail validation")
	}
}

func TestDNSGatewayEventRequiresQueryKind(t *testing.T) {
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
	if err := event.Validate(); err == nil {
		t.Fatal("expected DNS message event to fail validation")
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
