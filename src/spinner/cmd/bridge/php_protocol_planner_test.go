package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestPHPProtocolPlannerUsesInProcessEventHandler(t *testing.T) {
	planner := NewPHPProtocolPlanner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PHPProtocolEventPath {
			t.Fatalf("expected protocol event path %q, got %q", PHPProtocolEventPath, r.URL.Path)
		}
		if r.Header.Get(InternalTokenHeader) != "test-token" {
			t.Fatalf("expected internal protocol token")
		}
		var event GatewayEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Fatalf("decode gateway event: %v", err)
		}
		if event.Protocol != ProtocolSMTP || event.Route != "recipient@example.test" {
			t.Fatalf("unexpected gateway event: %+v", event)
		}
		_ = json.NewEncoder(w).Encode(GatewayDecision{
			Version:  ProtocolVersion,
			Protocol: ProtocolSMTP,
			Action:   GatewayAccept,
		})
	}), 1024)
	planner.InternalToken = "test-token"

	decision, err := planner.PlanEvent(context.Background(), GatewayEvent{
		ID:       "smtp-event-1",
		Protocol: ProtocolSMTP,
		Kind:     EventTransaction,
		Route:    "recipient@example.test",
		Payload:  []byte("message"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != GatewayAccept {
		t.Fatalf("expected accept decision, got %+v", decision)
	}
}

func TestPHPProtocolPlannerRejectsOversizedDecision(t *testing.T) {
	planner := NewPHPProtocolPlanner(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":"v1","protocol":"smtp","action":"accept","message":"too large"}`))
	}), 8)

	_, err := planner.PlanEvent(context.Background(), GatewayEvent{
		ID:       "smtp-event-2",
		Protocol: ProtocolSMTP,
		Kind:     EventTransaction,
		Route:    "recipient@example.test",
	})
	if err == nil {
		t.Fatal("expected oversized decision to fail")
	}
}

func TestPHPProtocolPlannerDecodesDNSSynchronousReply(t *testing.T) {
	planner := NewPHPProtocolPlanner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var event GatewayEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Fatalf("decode DNS event: %v", err)
		}
		if event.Protocol != ProtocolDNS || event.Kind != EventQuery || event.Attributes["qtype"] != "TXT" {
			t.Fatalf("unexpected DNS event: %+v", event)
		}
		_, _ = w.Write([]byte(`{
			"version":"v1",
			"protocol":"dns",
			"action":"deliver",
			"reply":{"protocol":"http","target":"https://reply.example.test/dns"}
		}`))
	}), 4096)

	decision, err := planner.PlanEvent(context.Background(), GatewayEvent{
		ID: "dns-event-1", Protocol: ProtocolDNS, Kind: EventQuery, Route: "/hook-1",
		Attributes: map[string]string{"qtype": "TXT"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Reply == nil || decision.Reply.Target != "https://reply.example.test/dns" {
		t.Fatalf("unexpected DNS reply decision: %+v", decision)
	}
}
