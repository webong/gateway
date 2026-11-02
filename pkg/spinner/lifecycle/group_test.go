package lifecycle

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunStopsAndWaitsForSiblingsAfterFailure(t *testing.T) {
	failure := errors.New("listener failed")
	stopped := make(chan struct{})
	cancelled := make(chan struct{})
	var shutdowns atomic.Int32
	err := Run(context.Background(), time.Second,
		Service{Name: "failed", Run: func(context.Context) error { return failure }, Shutdown: func(context.Context) error { shutdowns.Add(1); return nil }},
		Service{Name: "sibling", Run: func(ctx context.Context) error { <-ctx.Done(); close(cancelled); <-stopped; return nil }, Shutdown: func(context.Context) error { shutdowns.Add(1); close(stopped); return nil }},
	)
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "failed") || shutdowns.Load() != 2 {
		t.Fatalf("unexpected shutdown: error=%v callbacks=%d", err, shutdowns.Load())
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("sibling context was not cancelled")
	}
}

func TestRunStopsOnParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	stopped := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- Run(ctx, time.Second, Service{Name: "listener", Run: func(context.Context) error { close(started); <-stopped; return nil }, Shutdown: func(context.Context) error { close(stopped); return nil }})
	}()
	<-started
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestRunBoundsStuckShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Run(ctx, 20*time.Millisecond, Service{Name: "stuck", Run: func(context.Context) error { <-ctx.Done(); return nil }, Shutdown: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded shutdown, got %v", err)
	}
}
