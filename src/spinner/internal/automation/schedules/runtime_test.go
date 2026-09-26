package schedules

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/webong/gateway/src/spinner/internal/automation"
)

type fakeControl struct {
	runs    []Run
	status  string
	queued  int
	message string
}

func (f *fakeControl) Claim(context.Context) ([]Run, error) { return f.runs, nil }
func (f *fakeControl) Complete(_ context.Context, _ Run, status string, queued int, message string) error {
	f.status, f.queued, f.message = status, queued, message
	return nil
}

func TestPollExecutesGoScriptAndReportsQueuedDeliveries(t *testing.T) {
	control := &fakeControl{runs: []Run{{
		ID: "run-1", ScheduleID: "schedule-1", ScheduledFor: "2026-09-25 10:00:00",
		Language: automation.LanguageLua,
		Source:   `gateway.forward({url = "https://example.test/hook", body = gateway.event.payload.value})`,
		Payload:  map[string]any{"value": "hello"},
	}}}
	var delivered automation.Delivery
	(Runtime{Control: control, Enqueue: func(d automation.Delivery) bool {
		delivered = d
		return true
	}}).Poll(context.Background())
	if control.status != "succeeded" || control.queued != 1 || delivered.Method != "POST" || delivered.Body != "hello" {
		t.Fatalf("unexpected run: status=%s queued=%d delivery=%+v error=%s", control.status, control.queued, delivered, control.message)
	}
}

func TestPollReportsQueueFailure(t *testing.T) {
	control := &fakeControl{runs: []Run{{
		ID: "run-2", Language: automation.LanguageJavaScript,
		Source: `gateway.forward({url: 'https://example.test/hook'})`,
	}}}
	(Runtime{Control: control, Enqueue: func(automation.Delivery) bool { return false }}).Poll(context.Background())
	if control.status != "failed" || control.queued != 0 || control.message == "" {
		t.Fatalf("unexpected run result: %+v", control)
	}
}

func TestPollReportsScriptFailure(t *testing.T) {
	control := &fakeControl{runs: []Run{{ID: "run-3", Language: automation.LanguageLua, Source: `error("boom")`}}}
	(Runtime{Control: control, Enqueue: func(automation.Delivery) bool { return true }}).Poll(context.Background())
	if control.status != "failed" || control.queued != 0 || control.message == "" {
		t.Fatalf("unexpected run result: %+v", control)
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
		body := `{"data":[{"id":"run-1","claim_token":"claim-1","language":"lua","source":"gateway.respond('ok')"}]}`
		if strings.HasSuffix(request.URL.Path, "/complete") {
			body = `{"status":"succeeded"}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	control, err := NewHTTPControlPlane("http://planner.test/base", "secret", client)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := control.Claim(context.Background())
	if err != nil || len(runs) != 1 || runs[0].Language != automation.LanguageLua {
		t.Fatalf("unexpected claims: %+v, %v", runs, err)
	}
	if err := control.Complete(context.Background(), runs[0], "succeeded", 0, ""); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/base/_internal/gateway/schedules/claim" || paths[1] != "/base/_internal/gateway/schedule-runs/run-1/complete" {
		t.Fatalf("unexpected private routes: %v", paths)
	}
}
