package router

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"
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

func TestRoadRunnerPollsSchedulesThroughEmbeddedPlanner(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_SCHEDULES_ENABLED", "true")
	t.Setenv("ROADRUNNER_CONFIG", "test.yaml")
	t.Setenv("GATEWAY_SMTP_ADDR", "")
	t.Setenv("GATEWAY_DNS_ADDR", "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	claims := make(chan string, 1)
	server := &passiveWorkerServer{done: make(chan struct{})}
	err := RunRoadRunner(ctx, func(_ string, middleware func(http.Handler) http.Handler) (WorkerServer, error) {
		middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/_internal/gateway/schedules/claim" {
				claims <- request.Header.Get("X-Gateway-Internal")
				cancel()
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"data":[]}`))
		}))
		return server, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case token := <-claims:
		if token != "test-token" {
			t.Fatalf("embedded schedule claim had token %q", token)
		}
	default:
		t.Fatal("RoadRunner did not poll the embedded Planner")
	}
}

func TestRoadRunnerRoutesManagedWorkloadThroughEmbeddedPlanner(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED", "true")
	t.Setenv("GATEWAY_PROVISIONING_ROUTE_CIDRS", "127.0.0.0/8")
	t.Setenv("ROADRUNNER_CONFIG", "test.yaml")
	t.Setenv("GATEWAY_SMTP_ADDR", "")
	t.Setenv("GATEWAY_DNS_ADDR", "")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("managed"))
	}))
	defer backend.Close()
	parsed, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, rawPort, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &testWorkerServer{done: make(chan struct{}), cancel: cancel}
	err = RunRoadRunner(ctx, func(_ string, middleware func(http.Handler) http.Handler) (WorkerServer, error) {
		planner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/_internal/provisioning/routes/resolve" {
				if r.Header.Get("X-Gateway-Internal") != "test-token" || r.URL.Query().Get("hostname") != "events.example.test" {
					t.Errorf("unexpected RoadRunner lookup: %s", r.URL.String())
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"host": host, "port": port}})
				return
			}
			_, _ = w.Write([]byte("Laravel"))
		})
		handler := middleware(planner)
		request := httptest.NewRequest(http.MethodGet, "http://events.example.test/connection", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != "managed" {
			t.Errorf("managed RoadRunner route returned %d %q", response.Code, response.Body.String())
		}
		return server, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type testWorkerServer struct {
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
}

func (s *testWorkerServer) Serve() error { s.cancel(); <-s.done; return nil }
func (s *testWorkerServer) Stop()        { s.once.Do(func() { close(s.done) }) }

type passiveWorkerServer struct {
	done chan struct{}
	once sync.Once
}

func (s *passiveWorkerServer) Serve() error { <-s.done; return nil }
func (s *passiveWorkerServer) Stop()        { s.once.Do(func() { close(s.done) }) }
