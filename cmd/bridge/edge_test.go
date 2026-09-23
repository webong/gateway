package gateway

// Edge integration tests.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webong/gateway/internal/automation"
)

type plannerFunc func(context.Context, IngressRequest) (RoutePlan, error)

func (f plannerFunc) Plan(ctx context.Context, request IngressRequest) (RoutePlan, error) {
	return f(ctx, request)
}

type recordingExecutor struct {
	delivered []Delivery
	queued    []Delivery
	response  Response
}

func (e *recordingExecutor) Deliver(_ context.Context, delivery Delivery) (Response, error) {
	e.delivered = append(e.delivered, delivery)
	return e.response, nil
}

func (e *recordingExecutor) Enqueue(delivery Delivery) bool {
	e.queued = append(e.queued, delivery)
	return true
}

func TestEdgePlansInPHPAndExecutesInGo(t *testing.T) {
	executor := &recordingExecutor{response: Response{
		StatusCode: http.StatusAccepted,
		Headers:    map[string][]string{"X-Relay": {"go"}},
		Body:       []byte("reply"),
	}}
	var planned IngressRequest
	planner := plannerFunc(func(_ context.Context, request IngressRequest) (RoutePlan, error) {
		planned = request
		return RoutePlan{
			Version: ProtocolVersion,
			Action:  ActionRelay,
			Reply:   &Delivery{URL: "https://crm.example.test/reply"},
			Relays:  []Delivery{{SubscriberID: "subscriber-1", URL: "https://observer.example.test/relay"}},
		}, nil
	})
	edge := NewEdge(planner, executor, 1024)

	record := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/customer-defined/provider/opaque-value?source=test", strings.NewReader(`{"event":"created"}`))
	request.Host = "hooks.example.test"
	request.Header.Set("Content-Type", "application/json")
	edge.ServeHTTP(record, request)

	if record.Code != http.StatusAccepted || record.Body.String() != "reply" {
		t.Fatalf("expected proxied reply, got %d %q", record.Code, record.Body.String())
	}
	if planned.Protocol != ProtocolHTTP || planned.Event != EventRequest || planned.Scheme != "http" || planned.Host != "hooks.example.test" || planned.Path != "/customer-defined/provider/opaque-value" || planned.RawQuery != "source=test" || planned.DeliveryID == "" {
		t.Fatalf("planner received incomplete ingress request: %+v", planned)
	}
	if planned.Headers["X-Webhook-Forwarder-Delivery-Id"][0] != planned.DeliveryID {
		t.Fatal("expected delivery ID to be available to PHP and downstream subscribers")
	}
	if len(executor.delivered) != 1 || executor.delivered[0].Body == nil {
		t.Fatalf("expected original body on synchronous delivery: %+v", executor.delivered)
	}
	if len(executor.queued) != 1 || executor.queued[0].RawQuery != "source=test" || executor.queued[0].SubscriberID != "subscriber-1" {
		t.Fatalf("expected materialized relay delivery: %+v", executor.queued)
	}
}

func TestEdgeRestoresBodyForPHPPassThrough(t *testing.T) {
	executor := &recordingExecutor{}
	edge := NewEdge(plannerFunc(func(_ context.Context, _ IngressRequest) (RoutePlan, error) {
		return RoutePlan{Version: ProtocolVersion, Action: ActionPassThrough}, nil
	}), executor, 1024).SetPassThrough(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 7)
		_, _ = r.Body.Read(body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))

	record := httptest.NewRecorder()
	edge.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/owned/by/subscriber", strings.NewReader("payload")))

	if record.Code != http.StatusOK || record.Body.String() != "payload" {
		t.Fatalf("expected PHP pass-through body to be preserved, got %d %q", record.Code, record.Body.String())
	}
}

func TestEdgeReturnsImmediatePHPResponseWithoutGoDelivery(t *testing.T) {
	executor := &recordingExecutor{}
	planner := plannerFunc(func(_ context.Context, _ IngressRequest) (RoutePlan, error) {
		return RoutePlan{
			Version: ProtocolVersion,
			Action:  ActionRespond,
			ImmediateResponse: &Response{
				StatusCode: http.StatusOK,
				Body:       []byte("challenge"),
			},
		}, nil
	})
	edge := NewEdge(planner, executor, 1024)

	record := httptest.NewRecorder()
	edge.ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/owned/provider", nil))

	if record.Code != http.StatusOK || record.Body.String() != "challenge" {
		t.Fatalf("expected immediate response, got %d %q", record.Code, record.Body.String())
	}
	if len(executor.delivered) != 0 || len(executor.queued) != 0 {
		t.Fatal("expected immediate response to avoid network delivery")
	}
}

func TestEdgeExecutesGoAutomationAndQueuesItsForward(t *testing.T) {
	executor := &recordingExecutor{}
	edge := NewEdge(plannerFunc(func(_ context.Context, _ IngressRequest) (RoutePlan, error) {
		return RoutePlan{
			Version: ProtocolVersion,
			Action:  ActionAutomation,
			Automation: &Automation{
				Language: automation.LanguageJavaScript,
				Source:   `gateway.forward({url: "https://subscriber.example.test/events", method: "POST", body: gateway.event.body}); gateway.respond("scripted", 202);`,
			},
		}, nil
	}), executor, 1024)

	record := httptest.NewRecorder()
	edge.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/scripted", strings.NewReader("payload")))

	if record.Code != http.StatusAccepted || record.Body.String() != "scripted" {
		t.Fatalf("expected automation response, got %d %q", record.Code, record.Body.String())
	}
	if len(executor.queued) != 1 || executor.queued[0].URL != "https://subscriber.example.test/events" || string(executor.queued[0].Body) != "payload" {
		t.Fatalf("expected automation delivery, got %#v", executor.queued)
	}
}
