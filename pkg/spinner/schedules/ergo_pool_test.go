package schedules

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webong/gateway/arranger/automation"
)

// actorTestControl records schedule completion from the actor pool.
type actorTestControl struct {
	runs      []Run
	completed atomic.Int32
}

func (c *actorTestControl) Claim(context.Context) ([]Run, error) { return c.runs, nil }
func (c *actorTestControl) Complete(_ context.Context, _ Run, status string, _ int, _ string) error {
	if status != "succeeded" {
		return fmt.Errorf("unexpected status %q", status)
	}
	c.completed.Add(1)
	return nil
}

type actorTestExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (e *actorTestExecutor) Supports(automation.Language) bool { return true }
func (e *actorTestExecutor) Execute(ctx context.Context, _ automation.Script, _ automation.Input) (automation.Outcome, error) {
	e.started <- struct{}{}
	select {
	case <-e.release:
		return automation.Outcome{}, nil
	case <-ctx.Done():
		return automation.Outcome{}, ctx.Err()
	}
}

func TestErgoSchedulePoolRunsClaimedScriptsConcurrently(t *testing.T) {
	control := &actorTestControl{}
	for i := range scheduleActorCount {
		control.runs = append(control.runs, Run{ID: fmt.Sprintf("run-%d", i), Language: "javascript"})
	}
	executor := &actorTestExecutor{started: make(chan struct{}, scheduleActorCount), release: make(chan struct{})}
	runtime := Runtime{Control: control, Executor: executor, Enqueue: func(string, automation.Delivery) bool { return true }}
	pool, err := newActorPool(runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { runtime.poll(ctx, pool.Dispatch); close(done) }()
	for range scheduleActorCount {
		select {
		case <-executor.started:
		case <-ctx.Done():
			t.Fatal("not all claimed scripts began running")
		}
	}
	close(executor.release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("schedule actors did not finish")
	}
	if got := control.completed.Load(); got != scheduleActorCount {
		t.Fatalf("completed %d runs, want %d", got, scheduleActorCount)
	}
}
