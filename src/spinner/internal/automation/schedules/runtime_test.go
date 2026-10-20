package schedules

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/webong/gateway/driver/automation"
	javascript "github.com/webong/gateway/ext/javascript/runtime"
	lua "github.com/webong/gateway/ext/lua/runtime"
)

type fakeControl struct {
	runs    []Run
	status  string
	queued  int
	message string
}

type fakeRegistrar struct {
	origin    string
	schedules []automation.Schedule
}

func (f *fakeRegistrar) RegisterSchedules(_ context.Context, origin string, schedules []automation.Schedule) error {
	f.origin = origin
	f.schedules = append([]automation.Schedule(nil), schedules...)
	return nil
}

func (f *fakeControl) Claim(context.Context) ([]Run, error) { return f.runs, nil }
func (f *fakeControl) Complete(_ context.Context, _ Run, status string, queued int, message string) error {
	f.status, f.queued, f.message = status, queued, message
	return nil
}

func TestPollExecutesGoScriptAndReportsQueuedDeliveries(t *testing.T) {
	control := &fakeControl{runs: []Run{{
		ID: "run-1", ScheduleID: "schedule-1", ScheduledFor: "2026-09-25 10:00:00",
		Language: lua.Language,
		Source:   `gateway.forward({url = "https://example.test/hook", body = gateway.event.payload.value})`,
		Payload:  map[string]any{"value": "hello"},
	}}}
	var delivered automation.Delivery
	var originRunID string
	(Runtime{Control: control, Executor: automation.NewRuntime(lua.New()), Enqueue: func(runID string, d automation.Delivery) bool {
		originRunID = runID
		delivered = d
		return true
	}}).Poll(context.Background())
	if control.status != "succeeded" || control.queued != 1 || originRunID != "run-1" || delivered.Method != "POST" || delivered.Body != "hello" {
		t.Fatalf("unexpected run: status=%s queued=%d delivery=%+v error=%s", control.status, control.queued, delivered, control.message)
	}
}

func TestPollReportsQueueFailure(t *testing.T) {
	control := &fakeControl{runs: []Run{{
		ID: "run-2", Language: javascript.Language,
		Source: `gateway.forward({url: 'https://example.test/hook'})`,
	}}}
	(Runtime{Control: control, Executor: automation.NewRuntime(javascript.New()), Enqueue: func(string, automation.Delivery) bool { return false }}).Poll(context.Background())
	if control.status != "failed" || control.queued != 0 || control.message == "" {
		t.Fatalf("unexpected run result: %+v", control)
	}
}

func TestPollReportsScriptFailure(t *testing.T) {
	control := &fakeControl{runs: []Run{{ID: "run-3", Language: lua.Language, Source: `error("boom")`}}}
	(Runtime{Control: control, Executor: automation.NewRuntime(lua.New()), Enqueue: func(string, automation.Delivery) bool { return true }}).Poll(context.Background())
	if control.status != "failed" || control.queued != 0 || control.message == "" {
		t.Fatalf("unexpected run result: %+v", control)
	}
}

func TestPollDispatchesNativeIMAPThroughExistingScheduleClaim(t *testing.T) {
	control := &fakeControl{runs: []Run{{ID: "run-imap", Language: "native.imap", Source: "poll-resource-id"}}}
	called := ""
	(Runtime{Control: control, NativeIMAP: func(_ context.Context, routeID string) error {
		called = routeID
		return nil
	}, Enqueue: func(string, automation.Delivery) bool { return true }}).Poll(context.Background())
	if called != "poll-resource-id" || control.status != "succeeded" || control.queued != 0 {
		t.Fatalf("native job was not dispatched through schedules: called=%q control=%+v", called, control)
	}
}

func TestPollRegistersSchedulesOwnedByTheParentSchedule(t *testing.T) {
	control := &fakeControl{runs: []Run{{
		ID: "run-4", ScheduleID: "schedule-4", Language: javascript.Language,
		Source: `gateway.schedule({key: "child", name: "Child", cron: "0 9 * * *", language: "javascript", source: "gateway.respond('ok', 200)"})`,
	}}}
	registrar := &fakeRegistrar{}
	(Runtime{
		Control: control, Executor: automation.NewRuntime(javascript.New()), Registrar: registrar,
		Enqueue: func(string, automation.Delivery) bool { return true },
	}).Poll(context.Background())
	if control.status != "succeeded" || registrar.origin != "schedule:schedule-4" || len(registrar.schedules) != 1 {
		t.Fatalf("unexpected child schedule registration: control=%+v registrar=%+v", control, registrar)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestHTTPControlPlaneUsesPrivateContract(t *testing.T) {
	paths := []string{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("X-Gateway-Internal") != "secret" || request.Method != http.MethodPost {
			t.Fatal("missing private schedule authentication")
		}
		paths = append(paths, request.URL.Path)
		if strings.HasSuffix(request.URL.Path, "/claim") {
			var payload struct {
				Languages []automation.Language `json:"automation_languages"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Languages) != 1 || payload.Languages[0] != lua.Language {
				t.Fatalf("claim did not advertise the active languages: %v", payload.Languages)
			}
		}
		if strings.HasSuffix(request.URL.Path, "/register") {
			var payload struct {
				Origin    string                `json:"origin"`
				Schedules []automation.Schedule `json:"schedules"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.Origin != "endpoint:one" || len(payload.Schedules) != 1 || payload.Schedules[0].Key != "daily" {
				t.Fatalf("unexpected register payload: %#v", payload)
			}
		}
		body := `{"data":[{"id":"run-1","claim_token":"claim-1","language":"lua","source":"gateway.respond('ok')"}]}`
		if strings.HasSuffix(request.URL.Path, "/complete") {
			body = `{"status":"succeeded"}`
		}
		if strings.HasSuffix(request.URL.Path, "/register") {
			body = `{"registered":1}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	control, err := NewHTTPControlPlane("http://planner.test/base", "secret", client, lua.Language)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := control.Claim(context.Background())
	if err != nil || len(runs) != 1 || runs[0].Language != lua.Language {
		t.Fatalf("unexpected claims: %+v, %v", runs, err)
	}
	if err := control.Complete(context.Background(), runs[0], "succeeded", 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := control.RegisterSchedules(context.Background(), "endpoint:one", []automation.Schedule{{
		Key: "daily", Name: "Daily", Cron: "0 9 * * *", Language: javascript.Language, Source: "run()",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := control.DispatchNotifications(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 4 || paths[0] != "/base/_internal/gateway/schedules/claim" || paths[1] != "/base/_internal/gateway/schedule-runs/run-1/complete" || paths[2] != "/base/_internal/gateway/schedules/register" || paths[3] != "/base/_internal/gateway/schedules/notify" {
		t.Fatalf("unexpected private routes: %v", paths)
	}
}

func TestHandlerControlPlaneUsesEmbeddedPlannerWithoutNetwork(t *testing.T) {
	paths := []string{}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "gateway.internal" || request.Header.Get("X-Gateway-Internal") != "secret" {
			t.Errorf("unexpected embedded Planner request: host=%q token=%q", request.Host, request.Header.Get("X-Gateway-Internal"))
		}
		paths = append(paths, request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/claim") {
			_, _ = writer.Write([]byte(`{"data":[]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	control, err := NewHandlerControlPlane(handler, "secret", lua.Language)
	if err != nil {
		t.Fatal(err)
	}
	if runs, err := control.Claim(context.Background()); err != nil || len(runs) != 0 {
		t.Fatalf("unexpected embedded claim: runs=%v err=%v", runs, err)
	}
	if err := control.RegisterSchedules(context.Background(), "endpoint:one", []automation.Schedule{}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/_internal/gateway/schedules/claim" || paths[1] != "/_internal/gateway/schedules/register" {
		t.Fatalf("unexpected embedded paths: %v", paths)
	}
}

func TestEmptyLanguageListIsAnArrayForPlannerValidation(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Accept") != "application/json" {
			t.Error("private request must expect JSON")
		}
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if string(payload["automation_languages"]) != "[]" || string(payload["native_jobs"]) != `["imap"]` {
			t.Errorf("invalid Planner claim payload: %v", payload)
		}
		_, _ = writer.Write([]byte(`{"data":[]}`))
	})
	control, err := NewHandlerControlPlane(handler, "secret")
	if err != nil {
		t.Fatal(err)
	}
	control.EnableNativeIMAP()
	if _, err := control.Claim(context.Background()); err != nil {
		t.Fatal(err)
	}
}
