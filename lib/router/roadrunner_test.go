package router

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

func TestRoadRunnerStopsWorkerServerOnCancellation(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("ROADRUNNER_CONFIG", "test.yaml")
	t.Setenv("GATEWAY_SMTP_ADDR", "")
	t.Setenv("GATEWAY_DNS_ADDR", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &testWorkerServer{done: make(chan struct{}), cancel: cancel}
	err := RunRoadRunner(ctx, func(path string, middleware func(http.Handler) http.Handler) (WorkerServer, error) {
		if path != "test.yaml" || middleware == nil {
			t.Fatal("missing worker configuration")
		}
		return server, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-server.done:
	default:
		t.Fatal("worker server was not stopped")
	}
}

type testWorkerServer struct {
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
}

func (s *testWorkerServer) Serve() error { s.cancel(); <-s.done; return nil }
func (s *testWorkerServer) Stop()        { s.once.Do(func() { close(s.done) }) }
