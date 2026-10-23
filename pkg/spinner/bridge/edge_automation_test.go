package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/webong/gateway/driver/automation"
)

type automationPlannerFunc func(context.Context, IngressRequest) (RoutePlan, error)

func (f automationPlannerFunc) Plan(ctx context.Context, request IngressRequest) (RoutePlan, error) {
	return f(ctx, request)
}

type failingAutomationExecutor struct{ err error }

func (e failingAutomationExecutor) Supports(automation.Language) bool { return true }
func (e failingAutomationExecutor) Execute(context.Context, automation.Script, automation.Input) (automation.Outcome, error) {
	return automation.Outcome{}, e.err
}

type capturingAutomationExecutor struct{ script automation.Script }

func (e *capturingAutomationExecutor) Supports(automation.Language) bool { return true }
func (e *capturingAutomationExecutor) Execute(_ context.Context, script automation.Script, _ automation.Input) (automation.Outcome, error) {
	e.script = script
	return automation.Outcome{}, nil
}

func TestIngressAutomationCarriesPlannerExecutorChoice(t *testing.T) {
	planner := automationPlannerFunc(func(context.Context, IngressRequest) (RoutePlan, error) {
		return RoutePlan{Version: ProtocolVersion, Action: ActionAutomation, Automation: &Automation{
			Language: "test", Source: "run", Executor: automation.ExecutorGoroutine,
		}}, nil
	})
	executor := &capturingAutomationExecutor{}
	edge := NewEdge(planner, &recordingExecutor{}, 1024).SetAutomationExecutor(executor)
	response := httptest.NewRecorder()
	edge.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://gateway.test/hook", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("unexpected response: %d", response.Code)
	}
	if executor.script.Executor != automation.ExecutorGoroutine {
		t.Fatalf("executor choice was %q", executor.script.Executor)
	}
}

func TestAutomationPlanRejectsUnknownExecutor(t *testing.T) {
	plan := RoutePlan{Version: ProtocolVersion, Action: ActionAutomation, Automation: &Automation{
		Language: "test", Source: "run", Executor: "process",
	}}
	if err := plan.Validate(); err == nil {
		t.Fatal("accepted unknown automation executor")
	}
}

func TestIngressAutomationReportsActorSaturation(t *testing.T) {
	planner := automationPlannerFunc(func(context.Context, IngressRequest) (RoutePlan, error) {
		return RoutePlan{Version: ProtocolVersion, Action: ActionAutomation, Automation: &Automation{Language: "test", Source: "run"}}, nil
	})
	edge := NewEdge(planner, &recordingExecutor{}, 1024).
		SetAutomationExecutor(failingAutomationExecutor{err: automation.ErrBusy})
	response := httptest.NewRecorder()
	edge.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://gateway.test/hook", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("unexpected saturation response: status=%d retry=%q", response.Code, response.Header().Get("Retry-After"))
	}
}

func TestIngressAutomationReportsExecutionDeadline(t *testing.T) {
	planner := automationPlannerFunc(func(context.Context, IngressRequest) (RoutePlan, error) {
		return RoutePlan{Version: ProtocolVersion, Action: ActionAutomation, Automation: &Automation{Language: "test", Source: "run"}}, nil
	})
	edge := NewEdge(planner, &recordingExecutor{}, 1024).
		SetAutomationExecutor(failingAutomationExecutor{err: context.DeadlineExceeded})
	response := httptest.NewRecorder()
	edge.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://gateway.test/hook", nil))
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("unexpected deadline response: %d", response.Code)
	}
}
