package provision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRuntimeStopReleasesSessionOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	releases := 0
	runtime := &Runtime{
		cancel: cancel, done: done,
		release:     func(context.Context) error { releases++; return nil },
		stopTimeout: time.Second, stopped: make(chan struct{}),
	}
	runtime.Stop()
	runtime.Stop()
	if ctx.Err() != context.Canceled || releases != 1 {
		t.Fatalf("shutdown did not release once: context=%v releases=%d", ctx.Err(), releases)
	}
}

func TestRuntimeStopContextKeepsSessionUntilReconcilerExits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	released := make(chan struct{})
	runtime := &Runtime{
		cancel: cancel, done: done,
		release:     func(context.Context) error { close(released); return nil },
		stopTimeout: time.Second, stopped: make(chan struct{}),
	}
	stopCtx, stopCancel := context.WithCancel(context.Background())
	stopCancel()
	if err := runtime.StopContext(stopCtx); err != context.Canceled {
		t.Fatalf("expected bounded stop to return cancellation, got %v", err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("reconciliation was not canceled")
	}
	select {
	case <-released:
		t.Fatal("session was released while reconciler was still running")
	default:
	}
	close(done)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := runtime.StopContext(waitCtx); err != nil {
		t.Fatalf("waiting for session release: %v", err)
	}
	select {
	case <-released:
	default:
		t.Fatal("session was not released after reconciler exited")
	}
}

func TestClaimWorkerSessionWaitsForExpiredPredecessor(t *testing.T) {
	attempts := 0
	var session string
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts++
		var payload struct {
			SessionID    string `json:"session_id"`
			RequestNonce string `json:"request_nonce"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || len(payload.SessionID) != 32 {
			t.Fatalf("invalid session claim payload: %+v, %v", payload, err)
		}
		if session != "" && session != payload.SessionID {
			t.Fatal("worker changed its session while retrying")
		}
		session = payload.SessionID
		if attempts < 3 {
			writer.WriteHeader(http.StatusLocked)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": []ServerSpec{}, "meta": map[string]any{"leader_id": "node-a", "session_id": session, "request_nonce": payload.RequestNonce, "owner_id": "persistent-owner", "execution_protocol": "workload/v1", "issued_at_ms": 1000000, "permission_expires_at_ms": 1002000}})
	}))
	defer backend.Close()
	control, err := NewHTTPControlPlane(backend.URL, "internal-token", backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	control.ownerID = "persistent-owner"
	config := Config{NodeID: "node-a", CoordinationLease: 2 * time.Second, StopTimeout: time.Second}
	if err := claimWorkerSession(config, control, []string{DriverLocal}); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("expected retry after two fenced claims, got %d attempts", attempts)
	}
}

// A failed release must retain the local fence and remain retryable.
func TestRuntimeRetriesUnconfirmedRelease(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	attempts := 0
	owner := &countingOwner{}
	runtime := &Runtime{cancel: cancel, done: done, stopped: make(chan struct{}), owner: owner,
		release: func(context.Context) error {
			attempts++
			if attempts == 1 {
				return errors.New("planner unavailable")
			}
			return nil
		}}
	if err := runtime.StopContext(t.Context()); err == nil {
		t.Fatal("failed release was hidden")
	}
	if owner.closed != 0 {
		t.Fatal("local ownership released before Planner acknowledged cleanup")
	}
	if err := runtime.StopContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || owner.closed != 1 {
		t.Fatalf("release retry: attempts=%d closes=%d", attempts, owner.closed)
	}
}

type countingOwner struct{ closed int }

func (o *countingOwner) Close() error { o.closed++; return nil }

func TestRuntimePropagatesTerminalFailureAfterSuccessfulCleanup(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	close(done)
	failure := errors.New("invalid recovered assignment")
	owner := &countingOwner{}
	runtime := &Runtime{cancel: cancel, done: done, stopped: make(chan struct{}), owner: owner, runErr: failure}
	for i := 0; i < 2; i++ {
		if err := runtime.StopContext(t.Context()); !errors.Is(err, failure) {
			t.Fatalf("terminal failure hidden: %v", err)
		}
	}
	if owner.closed != 1 {
		t.Fatalf("cleanup not completed once: %d", owner.closed)
	}
}
