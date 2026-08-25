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
