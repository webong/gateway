package spinner

import (
	"context"
	"testing"

	mdns "github.com/miekg/dns"
	bridge "github.com/webong/gateway/arranger/integration"
)

func TestDNSProvidedAnswerEmitsPostAnswerObservation(t *testing.T) {
	observations := &captureObservationQueue{accepted: true}
	server, err := NewServer(Config{
		Address: "127.0.0.1:5353", Zone: "dns.example.test", Nameservers: []string{"ns1.example.test"},
		Observations: observations,
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolDNS,
			Action: bridge.GatewayRespond, StatusCode: 250, Payload: []byte(`[{"type":"a","value":"192.0.2.10"}]`)}, nil
	}), &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}

	response := exchangeHandler(server, "payload.hook.dns.example.test.", mdns.TypeA)
	if response.Rcode != mdns.RcodeSuccess || len(response.Answer) != 1 {
		t.Fatalf("unexpected DNS response: %+v", response)
	}
	observed := observations.last
	if observed.Type != "dns.query" || observed.ID == "" || observed.Attributes["mode"] != "provided" ||
		observed.Attributes["zone"] != "dns.example.test." || observed.Attributes["hook"] != "hook" ||
		observed.Attributes["decision_action"] != "respond" || observed.Attributes["response_name"] != "NOERROR" ||
		observed.Attributes["answer_types"] != "A:1" {
		t.Fatalf("unexpected provided observation: %+v", observed)
	}
}

func TestDNSManagedRecordEmitsManagedObservation(t *testing.T) {
	observations := &captureObservationQueue{accepted: true}
	server, err := NewServer(Config{
		Address: "127.0.0.1:5353", Observations: observations,
		Zones: []Zone{{Name: "dns.example.test", Nameservers: []string{"ns1.example.test"},
			Records: []ZoneRecord{{Name: "static.dns.example.test", Type: "A", Value: "192.0.2.25"}}}},
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		t.Fatal("managed DNS record unexpectedly reached Planner")
		return bridge.GatewayDecision{}, nil
	}), &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}

	response := exchangeHandler(server, "static.dns.example.test.", mdns.TypeA)
	if response.Rcode != mdns.RcodeSuccess || len(response.Answer) != 1 {
		t.Fatalf("unexpected managed DNS answer: %+v", response)
	}
	if observations.last.Attributes["mode"] != "managed" || observations.last.Attributes["hook"] != "" {
		t.Fatalf("unexpected managed observation: %+v", observations.last)
	}
}

func TestDNSObservationDropCannotChangeSelectedAnswer(t *testing.T) {
	server, err := NewServer(Config{
		Address: "127.0.0.1:5353", Zone: "dns.example.test", Nameservers: []string{"ns1.example.test"},
		Observations: &captureObservationQueue{accepted: false},
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolDNS,
			Action: bridge.GatewayRespond, Payload: []byte(`[{"type":"txt","value":"selected"}]`)}, nil
	}), &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}

	response := exchangeHandler(server, "payload.hook.dns.example.test.", mdns.TypeTXT)
	if response.Rcode != mdns.RcodeSuccess || len(response.Answer) != 1 || response.Answer[0].(*mdns.TXT).Txt[0] != "selected" {
		t.Fatalf("observation drop changed the DNS answer: %+v", response)
	}
}

type captureObservationQueue struct {
	accepted bool
	last     bridge.Observation
}

func (queue *captureObservationQueue) Enqueue(observation bridge.Observation) bool {
	queue.last = observation
	return queue.accepted
}
