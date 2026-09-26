package gateway

// PHP planner tests.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestPHPPlannerPreservesAnArbitraryOwnedPath(t *testing.T) {
	planner := NewPHPPlanner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PHPPlannerPath {
			t.Fatalf("expected internal planner path, got %s", r.URL.Path)
		}
		var ingress IngressRequest
		if err := json.NewDecoder(r.Body).Decode(&ingress); err != nil {
			t.Fatalf("decode ingress: %v", err)
		}
		if ingress.Path != "/customer-defined/provider/opaque-value" {
			t.Fatalf("expected arbitrary registered path, got %s", ingress.Path)
		}
		_ = json.NewEncoder(w).Encode(RoutePlan{
			Version: ProtocolVersion,
			Action:  ActionRelay,
			Relays: []Delivery{{
				SubscriberID: "subscriber-1",
				URL:          "https://subscriber.example.test/receive",
			}},
		})
	}), 1024)

	plan, err := planner.Plan(context.Background(), IngressRequest{
		DeliveryID: "delivery-1",
		Method:     http.MethodPost,
		Host:       "hooks.example.test",
		Path:       "/customer-defined/provider/opaque-value",
		Body:       []byte(`{"event":"created"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != ActionRelay || len(plan.Relays) != 1 || plan.Relays[0].SubscriberID != "subscriber-1" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}
