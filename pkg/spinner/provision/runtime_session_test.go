package provision

import (
	"context"
	"encoding/json"
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
	runtime := &Runtime{cancel: cancel, done: done, release: func() { releases++ }}
	runtime.Stop()
	runtime.Stop()
	if ctx.Err() != context.Canceled || releases != 1 {
		t.Fatalf("shutdown did not release once: context=%v releases=%d", ctx.Err(), releases)
	}
}

func TestClaimWorkerSessionWaitsForExpiredPredecessor(t *testing.T) {
	attempts := 0
	var session string
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts++
		var payload struct {
			SessionID string `json:"session_id"`
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
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": []ServerSpec{}, "meta": map[string]string{"leader_id": "node-a", "session_id": session}})
	}))
	defer backend.Close()
	control, err := NewHTTPControlPlane(backend.URL, "internal-token", backend.Client())
	if err != nil {
		t.Fatal(err)
	}
	config := Config{NodeID: "node-a", CoordinationLease: 2 * time.Second, StopTimeout: time.Second}
	if err := claimWorkerSession(config, control, []string{DriverLocal}); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("expected retry after two fenced claims, got %d attempts", attempts)
	}
}
