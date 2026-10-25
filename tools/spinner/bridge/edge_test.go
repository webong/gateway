package gateway_test

// Edge integration tests.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webong/gateway/arranger/automation"
	javascript "github.com/webong/gateway/pkg/javascript/runtime"
	shell "github.com/webong/gateway/pkg/shell/runtime"
	. "github.com/webong/gateway/pkg/spinner/bridge"
)

type plannerFunc func(context.Context, IngressRequest) (RoutePlan, error)

func TestEdgeRejectsSSHStreamRequestsBeforeExecution(t *testing.T) {
	planner := plannerFunc(func(context.Context, IngressRequest) (RoutePlan, error) {
		return RoutePlan{Version: ProtocolVersion, Action: ActionAutomation,
			Automation: &Automation{Language: "shell", Source: "ssh:2c034453-f6ef-4f98-a9a2-6dfb624758d0"}}, nil
	})
	edge := NewEdge(planner, &recordingExecutor{}, 1024)
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	request.Header.Set("Accept", "text/event-stream")
	recorder := httptest.NewRecorder()
	edge.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("SSH stream request should be rejected, got %d", recorder.Code)
	}
}

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
	if len(planned.Attributes["ingress_id"]) != 32 || planned.Attributes["remote_addr"] != request.RemoteAddr {
		t.Fatalf("expected a unique receipt identity and actual TCP peer: %+v", planned.Attributes)
	}
	if len(executor.delivered) != 1 || executor.delivered[0].Body == nil {
		t.Fatalf("expected original body on synchronous delivery: %+v", executor.delivered)
	}
	if len(executor.queued) != 1 || executor.queued[0].RawQuery != "source=test" || executor.queued[0].SubscriberID != "subscriber-1" {
		t.Fatalf("expected materialized relay delivery: %+v", executor.queued)
	}
	if executor.queued[0].EventID != planned.Attributes["ingress_id"] {
		t.Fatalf("queued relay lost its ingress event ID: %+v", executor.queued[0])
	}
}

func TestEdgeKeepsStableDeliveryIDButDistinctIngressReceipts(t *testing.T) {
	var requests []IngressRequest
	edge := NewEdge(plannerFunc(func(_ context.Context, request IngressRequest) (RoutePlan, error) {
		requests = append(requests, request)
		return RoutePlan{Version: ProtocolVersion, Action: ActionRespond, ImmediateResponse: &Response{StatusCode: http.StatusNoContent}}, nil
	}), &recordingExecutor{}, 1024)
	for range 2 {
		edge.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/same", strings.NewReader("same body")))
	}
	if len(requests) != 2 || requests[0].DeliveryID != requests[1].DeliveryID || requests[0].Attributes["ingress_id"] == requests[1].Attributes["ingress_id"] {
		t.Fatalf("identical deliveries must retain two ingress receipts: %+v", requests)
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

func TestEdgePassesEmailCredentialsOnlyToApplicationRoute(t *testing.T) {
	planned := false
	edge := NewEdge(plannerFunc(func(_ context.Context, _ IngressRequest) (RoutePlan, error) {
		planned = true
		return RoutePlan{}, nil
	}), &recordingExecutor{}, 4).SetPassThrough(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic private" {
			t.Error("submission credentials were lost")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	request := httptest.NewRequest(http.MethodPost, "/email/submissions", strings.NewReader(strings.Repeat("x", 20)))
	request.Header.Set("Authorization", "Basic private")
	response := httptest.NewRecorder()
	edge.ServeHTTP(response, request)
	if planned || response.Code != http.StatusAccepted {
		t.Fatalf("email credentials reached route planner or submission was denied: planned=%v status=%d", planned, response.Code)
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

func TestRoutePlanAllowsOnlyGeneratedShellNotifications(t *testing.T) {
	plan := RoutePlan{Version: ProtocolVersion, Action: ActionAutomation,
		Automation:        &Automation{Language: shell.Language, Source: "deploy"},
		ShellNotification: &Delivery{URL: "https://operator.example.test/shell-runs"}}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid shell notification rejected: %v", err)
	}
	plan.Automation.Language = javascript.Language
	if err := plan.Validate(); err == nil {
		t.Fatal("non-shell automation accepted a shell notification")
	}
	plan.Automation.Language = shell.Language
	plan.ShellNotification.Body = []byte("arbitrary payload")
	if err := plan.Validate(); err == nil {
		t.Fatal("shell notification accepted arbitrary payload")
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

func TestEdgeQueuesPlannerSelectedShellCompletionNoticeWithoutHookOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "accept"), []byte("#!/bin/sh\nprintf '{}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fail"), []byte("#!/bin/sh\necho 'secret failure output' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine, err := shell.New(shell.Config{HooksDir: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		hook, status string
		response     int
	}{
		{hook: "accept", status: "succeeded", response: http.StatusAccepted},
		{hook: "fail", status: "failed", response: http.StatusBadGateway},
	} {
		t.Run(test.hook, func(t *testing.T) {
			executor := &recordingExecutor{}
			edge := NewEdge(plannerFunc(func(_ context.Context, _ IngressRequest) (RoutePlan, error) {
				return RoutePlan{Version: ProtocolVersion, Action: ActionAutomation,
					Automation:        &Automation{Language: shell.Language, Source: test.hook},
					ShellNotification: &Delivery{URL: "https://operator.example.test/shell-runs"}}, nil
			}), executor, 1024).SetAutomationExecutor(automation.NewRuntime(engine))
			record := httptest.NewRecorder()
			edge.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/custom/hook", strings.NewReader("secret request body")))
			if record.Code != test.response || len(executor.queued) != 1 {
				t.Fatalf("unexpected hook response or notification: %d %#v", record.Code, executor.queued)
			}
			notice := executor.queued[0]
			if notice.URL != "https://operator.example.test/shell-runs" || notice.Method != http.MethodPost || notice.Headers["Content-Type"][0] != "application/json" || strings.Contains(string(notice.Body), "secret") {
				t.Fatalf("unsafe shell completion notice: %#v", notice)
			}
			var body map[string]string
			if err := json.Unmarshal(notice.Body, &body); err != nil || body["status"] != test.status || body["run_id"] == "" || body["hook"] != test.hook {
				t.Fatalf("invalid shell completion notice: %s, %v", notice.Body, err)
			}
			if record.Header().Get("X-Gateway-Shell-Run-Id") != body["run_id"] {
				t.Fatalf("response and notification run IDs differ: %q vs %q", record.Header().Get("X-Gateway-Shell-Run-Id"), body["run_id"])
			}
		})
	}
}

func TestEdgeMarksBufferedNonzeroShellExitAsFailedCompletion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "reject"), []byte("#!/bin/sh\nprintf 'rejected'\nexit 118\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine, err := shell.New(shell.Config{HooksDir: root, OutputMode: shell.ModeBuffered})
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	edge := NewEdge(plannerFunc(func(_ context.Context, _ IngressRequest) (RoutePlan, error) {
		return RoutePlan{Version: ProtocolVersion, Action: ActionAutomation,
			Automation:        &Automation{Language: shell.Language, Source: "reject"},
			ShellNotification: &Delivery{URL: "https://operator.example.test/shell-runs"}}, nil
	}), executor, 1024).SetAutomationExecutor(automation.NewRuntime(engine))
	record := httptest.NewRecorder()
	edge.ServeHTTP(record, httptest.NewRequest(http.MethodPost, "/custom/hook", nil))
	if record.Code != 418 || len(executor.queued) != 1 || !strings.Contains(string(executor.queued[0].Body), `"status":"failed"`) {
		t.Fatalf("buffered failure notification missing: status=%d queued=%#v", record.Code, executor.queued)
	}
}
