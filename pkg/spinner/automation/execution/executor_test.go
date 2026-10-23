package execution

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webong/gateway/driver/automation"
)

type testExecutor struct {
	started chan string
	release chan struct{}
	runs    atomic.Int32
}

func (e *testExecutor) Supports(language automation.Language) bool { return language == "test" }
func (e *testExecutor) Languages() []automation.Language           { return []automation.Language{"test"} }
func (e *testExecutor) Execute(ctx context.Context, script automation.Script, _ automation.Input) (automation.Outcome, error) {
	e.runs.Add(1)
	if e.started != nil {
		e.started <- script.Source
	}
	if e.release != nil {
		select {
		case <-e.release:
		case <-ctx.Done():
			return automation.Outcome{}, ctx.Err()
		}
	}
	return automation.Outcome{Response: &automation.Response{Status: 200, Body: script.Source}}, nil
}

func TestNewSelectsEachExecutionModeBehindOneContract(t *testing.T) {
	for _, mode := range []Mode{ModeDirect, ModeGoroutine, ModeErgo} {
		t.Run(string(mode), func(t *testing.T) {
			executor, err := New(&testExecutor{}, Options{Mode: mode, Workers: 1, QueueSize: 1, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer executor.Close()
			var contract automation.Executor = executor
			outcome, err := contract.Execute(context.Background(), automation.Script{Language: "test", Source: string(mode)}, automation.Input{})
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Response == nil || outcome.Response.Body != string(mode) {
				t.Fatalf("unexpected outcome: %+v", outcome)
			}
			if executor.Mode() != mode || !executor.Supports("test") {
				t.Fatalf("unexpected selected executor: mode=%q supports=%v", executor.Mode(), executor.Supports("test"))
			}
			if got := automation.Languages(executor); len(got) != 1 || got[0] != "test" {
				t.Fatalf("unexpected languages: %v", got)
			}
		})
	}
}

func TestGoroutineExecutorBoundsQueue(t *testing.T) {
	delegate := &testExecutor{started: make(chan string, 2), release: make(chan struct{})}
	executor, err := New(delegate, Options{Mode: ModeGoroutine, Workers: 1, QueueSize: 0, Timeout: 60 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()

	first := make(chan error, 1)
	go func() {
		_, runErr := executor.Execute(context.Background(), automation.Script{Language: "test", Source: "first"}, automation.Input{})
		first <- runErr
	}()
	<-delegate.started

	started := time.Now()
	_, err = executor.Execute(context.Background(), automation.Script{Language: "test", Source: "second"}, automation.Input{})
	if !errors.Is(err, automation.ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	if time.Since(started) < 40*time.Millisecond {
		t.Fatal("saturated execution did not wait for capacity")
	}
	if delegate.runs.Load() != 1 {
		t.Fatalf("queued work executed; run count is %d", delegate.runs.Load())
	}
	close(delegate.release)
	<-first
}

func TestGoroutineExecutorHonorsCallerCancellationWhileWaiting(t *testing.T) {
	delegate := &testExecutor{started: make(chan string, 1), release: make(chan struct{})}
	executor, err := New(delegate, Options{Mode: ModeGoroutine, Workers: 1, QueueSize: 0, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()

	first := make(chan struct{})
	go func() {
		_, _ = executor.Execute(context.Background(), automation.Script{Language: "test", Source: "first"}, automation.Input{})
		close(first)
	}()
	<-delegate.started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = executor.Execute(ctx, automation.Script{Language: "test", Source: "cancelled"}, automation.Input{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if delegate.runs.Load() != 1 {
		t.Fatalf("cancelled work executed; run count is %d", delegate.runs.Load())
	}
	close(delegate.release)
	<-first
}

func TestNewRejectsUnknownMode(t *testing.T) {
	_, err := New(&testExecutor{}, Options{Mode: "process", Workers: 1, Timeout: time.Second})
	if err == nil {
		t.Fatal("expected unknown execution mode to fail")
	}
}
