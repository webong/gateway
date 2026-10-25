package execution

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webong/gateway/arranger/automation"
)

type blockingExecutor struct {
	started            chan string
	release            chan struct{}
	runs               atomic.Int32
	ignoreCancellation bool
}

func (e *blockingExecutor) Supports(automation.Language) bool { return true }
func (e *blockingExecutor) Languages() []automation.Language  { return []automation.Language{"test"} }
func (e *blockingExecutor) Execute(ctx context.Context, script automation.Script, _ automation.Input) (automation.Outcome, error) {
	e.runs.Add(1)
	e.started <- script.Source
	select {
	case <-e.release:
		return automation.Outcome{Response: &automation.Response{Status: 200, Body: script.Source}}, nil
	case <-ctx.Done():
		if e.ignoreCancellation {
			<-e.release
			return automation.Outcome{}, nil
		}
		return automation.Outcome{}, ctx.Err()
	}
}

func TestExecutorWaitsForWorkerAndPreservesResults(t *testing.T) {
	delegate := &blockingExecutor{started: make(chan string, 2), release: make(chan struct{}, 2)}
	executor, err := newErgoExecutor(delegate, Options{Workers: 1, QueueSize: 1, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()

	first := make(chan error, 1)
	go func() {
		_, runErr := executor.Execute(context.Background(), automation.Script{Language: "test", Source: "first"}, automation.Input{})
		first <- runErr
	}()
	if got := <-delegate.started; got != "first" {
		t.Fatalf("first execution was %q", got)
	}

	secondResult := make(chan automation.Outcome, 1)
	secondErr := make(chan error, 1)
	go func() {
		outcome, runErr := executor.Execute(context.Background(), automation.Script{Language: "test", Source: "second"}, automation.Input{})
		secondResult <- outcome
		secondErr <- runErr
	}()
	select {
	case got := <-delegate.started:
		t.Fatalf("queued execution started early: %s", got)
	case <-time.After(30 * time.Millisecond):
	}
	delegate.release <- struct{}{}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if got := <-delegate.started; got != "second" {
		t.Fatalf("second execution was %q", got)
	}
	delegate.release <- struct{}{}
	if err := <-secondErr; err != nil {
		t.Fatal(err)
	}
	if got := (<-secondResult).Response.Body; got != "second" {
		t.Fatalf("response body was %q", got)
	}
}

func TestExecutorReturnsBusyOnlyAfterBoundedWait(t *testing.T) {
	delegate := &blockingExecutor{started: make(chan string, 2), release: make(chan struct{}), ignoreCancellation: true}
	executor, err := newErgoExecutor(delegate, Options{Workers: 1, QueueSize: 0, Timeout: 60 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()
	go func() {
		_, _ = executor.Execute(context.Background(), automation.Script{Language: "test", Source: "first"}, automation.Input{})
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
	close(delegate.release)
}

func TestExecutorHonorsCallerCancellationWhileQueued(t *testing.T) {
	delegate := &blockingExecutor{started: make(chan string, 2), release: make(chan struct{})}
	executor, err := newErgoExecutor(delegate, Options{Workers: 1, QueueSize: 0, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()
	go func() {
		_, _ = executor.Execute(context.Background(), automation.Script{Language: "test", Source: "first"}, automation.Input{})
	}()
	<-delegate.started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = executor.Execute(ctx, automation.Script{Language: "test", Source: "cancelled"}, automation.Input{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if delegate.runs.Load() != 1 {
		t.Fatalf("cancelled work executed; run count is %d", delegate.runs.Load())
	}
	close(delegate.release)
}
