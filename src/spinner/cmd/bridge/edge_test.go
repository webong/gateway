package gateway

// Edge integration tests.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/gateway/driver/automation"
	javascript "github.com/webong/gateway/ext/javascript/runtime"
	shell "github.com/webong/gateway/ext/shell/runtime"
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

type recordingScheduleRegistrar struct {
	origin    string
	schedules []automation.Schedule
}

func (r *recordingScheduleRegistrar) RegisterSchedules(_ context.Context, origin string, schedules []automation.Schedule) error {
	r.origin = origin
	r.schedules = append([]automation.Schedule(nil), schedules...)
	return nil
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

func TestEdgeQueuesRelaysBeforeReturningImmediatePlannerResponse(t *testing.T) {
	executor := &recordingExecutor{}
	planner := plannerFunc(func(_ context.Context, _ IngressRequest) (RoutePlan, error) {
		return RoutePlan{
			Version: ProtocolVersion,
			Action:  ActionRelay,
			Relays:  []Delivery{{URL: "https://subscriber.example.test/hook"}},
			ImmediateResponse: &Response{
				StatusCode: http.StatusAccepted,
				Headers:    map[string][]string{"X-Engine": {"Primi"}},
				Body:       []byte("scripted"),
			},
		}, nil
	})
	edge := NewEdge(planner, executor, 1024)

	record := httptest.NewRecorder()
	edge.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/scripted", strings.NewReader("payload")))

	if record.Code != http.StatusAccepted || record.Body.String() != "scripted" || record.Header().Get("X-Engine") != "Primi" {
		t.Fatalf("expected immediate script response, got %d %q %#v", record.Code, record.Body.String(), record.Header())
	}
	if len(executor.queued) != 1 || executor.queued[0].URL != "https://subscriber.example.test/hook" || string(executor.queued[0].Body) != "payload" {
		t.Fatalf("expected queued script delivery, got %#v", executor.queued)
	}
}

func TestRoutePlanAllowsExtensionLanguageIdentifiers(t *testing.T) {
	plan := RoutePlan{
		Version: ProtocolVersion,
		Action:  ActionAutomation,
		Automation: &Automation{
			Language: automation.Language("webhookscript"),
			Source:   `respond('done', 200);`,
		},
	}

	if err := plan.Validate(); err != nil {
		t.Fatalf("expected extension language to pass the transport contract: %v", err)
	}
}

func TestEdgeFailsClosedWhenLanguageExtensionIsNotInstalled(t *testing.T) {
	edge := NewEdge(plannerFunc(func(_ context.Context, _ IngressRequest) (RoutePlan, error) {
		return RoutePlan{
			Version: ProtocolVersion,
			Action:  ActionAutomation,
			Automation: &Automation{
				Language: javascript.Language,
				Source:   `gateway.respond("scripted", 202);`,
			},
		}, nil
	}), &recordingExecutor{}, 1024).SetAutomationExecutor(automation.NewRuntime())

	record := httptest.NewRecorder()
	edge.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/scripted", nil))

	if record.Code != http.StatusBadGateway {
		t.Fatalf("expected missing extension to fail closed, got %d", record.Code)
	}
}

func TestEdgeExecutesGoAutomationAndQueuesItsForward(t *testing.T) {
	executor := &recordingExecutor{}
	edge := NewEdge(plannerFunc(func(_ context.Context, ingress IngressRequest) (RoutePlan, error) {
		if len(ingress.Runtime.AutomationLanguages) != 1 || ingress.Runtime.AutomationLanguages[0] != javascript.Language {
			t.Fatalf("planner did not receive the active engine catalog: %v", ingress.Runtime.AutomationLanguages)
		}
		return RoutePlan{
			Version: ProtocolVersion,
			Action:  ActionAutomation,
			Automation: &Automation{
				Language: javascript.Language,
				Source:   `gateway.forward({url: "https://subscriber.example.test/events", method: "POST", body: gateway.event.body}); gateway.respond("scripted", 202);`,
			},
		}, nil
	}), executor, 1024).SetAutomationExecutor(automation.NewRuntime(javascript.New()))

	record := httptest.NewRecorder()
	edge.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/scripted", strings.NewReader("payload")))

	if record.Code != http.StatusAccepted || record.Body.String() != "scripted" {
		t.Fatalf("expected automation response, got %d %q", record.Code, record.Body.String())
	}
	if len(executor.queued) != 1 || executor.queued[0].URL != "https://subscriber.example.test/events" || string(executor.queued[0].Body) != "payload" {
		t.Fatalf("expected automation delivery, got %#v", executor.queued)
	}
}

func TestEdgeRegistersAutomationSchedulesWithEndpointOrigin(t *testing.T) {
	registrar := &recordingScheduleRegistrar{}
	edge := NewEdge(plannerFunc(func(_ context.Context, _ IngressRequest) (RoutePlan, error) {
		return RoutePlan{
			Version: ProtocolVersion, Action: ActionAutomation,
			Metadata:   map[string]string{"endpoint_id": "endpoint-1"},
			Automation: &Automation{Language: javascript.Language, Source: `gateway.schedule({key: "daily", name: "Daily", cron: "0 9 * * *", language: "javascript", source: "gateway.respond('ok', 200)"});`},
		}, nil
	}), &recordingExecutor{}, 1024).
		SetAutomationExecutor(automation.NewRuntime(javascript.New())).
		SetScheduleRegistrar(registrar)

	record := httptest.NewRecorder()
	edge.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/scripted", nil))

	if record.Code != http.StatusAccepted || registrar.origin != "endpoint:endpoint-1" || len(registrar.schedules) != 1 || registrar.schedules[0].Key != "daily" {
		t.Fatalf("unexpected schedule registration: status=%d origin=%q schedules=%#v", record.Code, registrar.origin, registrar.schedules)
	}
}

func TestEdgeStreamsPlannerSelectedShellHook(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "progress")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'running'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine, err := shell.New(shell.Config{HooksDir: root, OutputMode: shell.ModeBuffered})
	if err != nil {
		t.Fatal(err)
	}
	edge := NewEdge(plannerFunc(func(_ context.Context, request IngressRequest) (RoutePlan, error) {
		if len(request.Runtime.AutomationLanguages) != 1 || request.Runtime.AutomationLanguages[0] != shell.Language {
			t.Fatalf("shell capability missing from plan request: %+v", request.Runtime)
		}
		return RoutePlan{Version: ProtocolVersion, Action: ActionAutomation,
			Automation: &Automation{Language: shell.Language, Source: "progress"}}, nil
	}), &recordingExecutor{}, 1024).SetAutomationExecutor(automation.NewRuntime(engine))
	record := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/custom/hook", nil)
	request.Header.Set("Accept", "text/event-stream")
	edge.ServeHTTP(record, request)
	if record.Code != http.StatusOK || record.Header().Get("X-Gateway-Shell-Run-Id") == "" || !strings.Contains(record.Body.String(), "data: running") {
		t.Fatalf("unexpected streaming response: %d %q %#v", record.Code, record.Body.String(), record.Header())
	}
}

func TestEdgeReturnsShellRunIDForAcceptedHook(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "accept"), []byte("#!/bin/sh\nprintf '{}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine, err := shell.New(shell.Config{HooksDir: root})
	if err != nil {
		t.Fatal(err)
	}
	edge := NewEdge(plannerFunc(func(_ context.Context, _ IngressRequest) (RoutePlan, error) {
		return RoutePlan{Version: ProtocolVersion, Action: ActionAutomation,
			Automation: &Automation{Language: shell.Language, Source: "accept"}}, nil
	}), &recordingExecutor{}, 1024).SetAutomationExecutor(automation.NewRuntime(engine))
	record := httptest.NewRecorder()
	edge.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/custom/hook", nil))
	if record.Code != http.StatusAccepted || record.Header().Get("X-Gateway-Shell-Run-Id") == "" {
		t.Fatalf("expected accepted hook with run ID, got %d %#v", record.Code, record.Header())
	}
}
