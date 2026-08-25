package netgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPPlannerUsesTheSharedBridgeContract(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != PHPPlannerPath {
			t.Fatalf("expected planner path %q, got %q", PHPPlannerPath, request.URL.Path)
		}
		if request.Header.Get(InternalTokenHeader) != "test-token" {
			t.Fatalf("expected internal token header")
		}

		var ingress IngressRequest
		if err := json.NewDecoder(request.Body).Decode(&ingress); err != nil {
			t.Fatalf("decode ingress: %v", err)
		}
		if ingress.Path != "/customer-defined/provider/opaque-value" {
			t.Fatalf("expected arbitrary registered path, got %s", ingress.Path)
		}

		_ = json.NewEncoder(writer).Encode(RoutePlan{
			Version: ProtocolVersion,
			Action:  ActionRelay,
			Relays: []Delivery{{
				SubscriberID: "subscriber-1",
				URL:          "https://subscriber.example.test/receive",
			}},
		})
	}))
	defer backend.Close()

	planner, err := NewHTTPPlanner(backend.URL, backend.Client(), 1024, "test-token")
	if err != nil {
		t.Fatal(err)
	}

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

func TestHTTPRuntimeProxiesPassThroughAndProtectsPlannerPath(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == PHPPlannerPath {
			_ = json.NewEncoder(writer).Encode(RoutePlan{Version: ProtocolVersion, Action: ActionPassThrough})
			return
		}

		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read pass-through body: %v", err)
		}
		if request.URL.Path != "/owned/by/subscriber" || string(body) != "payload" {
			t.Fatalf("unexpected pass-through request: path=%q body=%q", request.URL.Path, body)
		}
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("Laravel application"))
	}))
	defer backend.Close()

	runtime, err := NewHTTPRuntime(backend.URL, "test-token", 1024, backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	edge := NewEdge(runtime.Planner(), &recordingExecutor{}, 1024).SetPassThrough(runtime.PassThrough())
	handler := runtime.Handler(edge)

	passThrough := httptest.NewRecorder()
	handler.ServeHTTP(passThrough, httptest.NewRequest(http.MethodPost, "/owned/by/subscriber", strings.NewReader("payload")))
	if passThrough.Code != http.StatusCreated || passThrough.Body.String() != "Laravel application" {
		t.Fatalf("expected Laravel pass-through, got %d %q", passThrough.Code, passThrough.Body.String())
	}

	internal := httptest.NewRecorder()
	handler.ServeHTTP(internal, httptest.NewRequest(http.MethodPost, PHPPlannerPath, nil))
	if internal.Code != http.StatusNotFound {
		t.Fatalf("expected planner path to be private, got %d", internal.Code)
	}
}
