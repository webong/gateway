package webrelay

// PHP planner tests.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestRoadRunnerPluginPassesNonRelayPathsBackToPHP(t *testing.T) {
	executor := &recordingExecutor{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == PHPPlannerPath {
			if r.Header.Get("X-RoadRunner-Relay-Internal") != "test-token" {
				t.Fatalf("expected configured internal planner token")
			}
			var ingress IngressRequest
			if err := json.NewDecoder(r.Body).Decode(&ingress); err != nil {
				t.Fatalf("decode planner request: %v", err)
			}
			plan := RoutePlan{Version: ProtocolVersion, Action: ActionPassThrough}
			if ingress.Path == "/owned/by/subscriber" {
				plan = RoutePlan{
					Version: ProtocolVersion,
					Action:  ActionRelay,
					Relays:  []Delivery{{URL: "https://subscriber.example.test/receive"}},
				}
			}
			_ = json.NewEncoder(w).Encode(plan)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Laravel application"))
	})
	middleware := NewRoadRunnerPlugin(executor, 1024, "test-token").Middleware(next)

	owned := httptest.NewRecorder()
	middleware.ServeHTTP(owned, httptest.NewRequest(http.MethodPost, "/owned/by/subscriber", nil))
	if owned.Code != http.StatusAccepted || len(executor.queued) != 1 {
		t.Fatalf("expected arbitrary path to be handled by Go transport, got %d and %d deliveries", owned.Code, len(executor.queued))
	}

	application := httptest.NewRecorder()
	middleware.ServeHTTP(application, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if application.Code != http.StatusOK || application.Body.String() != "Laravel application" {
		t.Fatalf("expected PHP pass-through, got %d %q", application.Code, application.Body.String())
	}
}
