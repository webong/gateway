package router

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/webong/gateway/src/spinner/transport"
)

func TestRunOwnsPlannerLifecycle(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_ROUTER_APP_DIR", t.TempDir())
	t.Setenv("GATEWAY_ROUTER_LISTEN", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	planner := &testPlanner{}
	err := Run(ctx, func(appDir string) (Planner, error) {
		if appDir == "" {
			t.Fatal("missing app directory")
		}
		cancel()
		return planner, nil
	})
	if err != nil || !planner.closed {
		t.Fatalf("Run returned %v; Planner closed = %v", err, planner.closed)
	}
}

func TestRunReturnsPlannerStartupError(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_ROUTER_APP_DIR", t.TempDir())
	want := errors.New("PHP startup failed")
	if err := Run(context.Background(), func(string) (Planner, error) { return nil, want }); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestRunPollsSchedulesThroughEmbeddedPlanner(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_SCHEDULES_ENABLED", "true")
	t.Setenv("GATEWAY_ROUTER_APP_DIR", t.TempDir())
	t.Setenv("GATEWAY_ROUTER_LISTEN", "127.0.0.1:0")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	planner := &schedulePlanner{claimed: make(chan string, 1), cancel: cancel}
	if err := Run(ctx, func(string) (Planner, error) { return planner, nil }); err != nil {
		t.Fatal(err)
	}
	if !planner.closed {
		t.Fatal("embedded Planner was not closed")
	}
	select {
	case token := <-planner.claimed:
		if token != "test-token" {
			t.Fatalf("embedded schedule claim had token %q", token)
		}
	default:
		t.Fatal("bundled Router did not poll the embedded Planner")
	}
}

type testPlanner struct{ closed bool }

func (*testPlanner) ServeHTTP(http.ResponseWriter, *http.Request) {}
func (p *testPlanner) Close()                                     { p.closed = true }

type schedulePlanner struct {
	claimed chan string
	cancel  context.CancelFunc
	closed  bool
}

func (p *schedulePlanner) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/_internal/gateway/schedules/claim" {
		p.claimed <- request.Header.Get("X-Gateway-Internal")
		p.cancel()
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write([]byte(`{"data":[]}`))
}

func (p *schedulePlanner) Close() { p.closed = true }

func TestRouterHandlerRoutesOnlyHooksToSpinner(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/hooks/order", want: "edge"},
		{path: "/hooks2/order", want: "planner"},
		{path: "/registry/endpoints", want: "planner"},
		{path: "/_internal/gateway/plan", want: "404 page not found\n"},
	} {
		t.Run(test.path, func(t *testing.T) {
			planner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("planner")) })
			edge := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("edge")) })
			response := httptest.NewRecorder()
			NewHandler(planner, edge, edge, nil, nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Body.String() != test.want {
				t.Fatalf("got %q, want %q", response.Body.String(), test.want)
			}
		})
	}
}

func TestDisabledManagedRouteIsNilAtRouterHandler(t *testing.T) {
	managed, err := ManagedRouteProxy(&transport.Router{})
	if err != nil || managed != nil {
		t.Fatalf("disabled managed route = %v, err=%v", managed, err)
	}
	planner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("healthy")) })
	response := httptest.NewRecorder()
	NewHandler(planner, planner, planner, nil, managed).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusOK || response.Body.String() != "healthy" {
		t.Fatalf("default Router returned %d %q", response.Code, response.Body.String())
	}
}

func TestProvisioningControlOnlyForwardsProvisioningRoutes(t *testing.T) {
	planner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Internal") != "worker-secret" || r.Header.Get("X-Gateway-Target") != "target-1" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(r.URL.Path))
	})
	control := provisioningControlHandler(planner)
	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/_internal/provisioning/targets/target-1", want: http.StatusOK},
		{path: "/_internal/provisioning2/targets/target-1", want: http.StatusNotFound},
		{path: "/_internal/gateway/plan", want: http.StatusNotFound},
		{path: "/registry/endpoints", want: http.StatusNotFound},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("X-Gateway-Internal", "worker-secret")
		request.Header.Set("X-Gateway-Target", "target-1")
		response := httptest.NewRecorder()
		control.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("%s returned %d, want %d", test.path, response.Code, test.want)
		}
	}
	response := httptest.NewRecorder()
	control.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/_internal/provisioning/targets/target-1", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("Planner authentication was bypassed: %d", response.Code)
	}
}

func TestRunServesPrivateProvisioningControl(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_ROUTER_APP_DIR", t.TempDir())
	t.Setenv("GATEWAY_ROUTER_LISTEN", "127.0.0.1:0")
	t.Setenv("GATEWAY_ROUTER_CONTROL_LISTEN", unusedLoopbackAddress(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- Run(ctx, func(string) (Planner, error) { return &controlTestPlanner{}, nil })
	}()
	controlURL := "http://" + os.Getenv("GATEWAY_ROUTER_CONTROL_LISTEN")
	response := awaitControlResponse(t, controlURL+"/_internal/provisioning/targets/target-1")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("private provisioning route returned %d", response.StatusCode)
	}
	_ = response.Body.Close()
	response = awaitControlResponse(t, controlURL+"/registry/endpoints")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("private control exposed public Planner route: %d", response.StatusCode)
	}
	_ = response.Body.Close()
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bundled Router did not stop")
	}
}

type controlTestPlanner struct{}

func (*controlTestPlanner) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (*controlTestPlanner) Close() {}

func unusedLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func awaitControlResponse(t *testing.T, address string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(3 * time.Second)
	for {
		response, err := client.Get(address)
		if err == nil {
			return response
		}
		if time.Now().After(deadline) {
			t.Fatalf("private control listener unavailable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type testManagedRoute struct{ calls int }

func (m *testManagedRoute) ServeWithPlanner(w http.ResponseWriter, r *http.Request, _ http.Handler) bool {
	m.calls++
	if r.URL.Path != "/connection/websocket" {
		return false
	}
	_, _ = w.Write([]byte("managed"))
	return true
}

func TestRouterHandlerRoutesManagedWorkloadsBeforeLaravelAndHooks(t *testing.T) {
	planner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("planner")) })
	edge := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("edge")) })
	managed := &testManagedRoute{}
	handler := NewHandler(planner, edge, edge, nil, managed)
	for path, want := range map[string]string{
		"/connection/websocket": "managed",
		"/hooks/order":          "edge",
		"/registry/endpoints":   "planner",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Body.String() != want {
			t.Errorf("%s returned %q, want %q", path, response.Body.String(), want)
		}
	}
	before := managed.calls
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/_internal/provisioning/routes/resolve", nil))
	if response.Code != http.StatusNotFound || managed.calls != before {
		t.Fatal("public internal route reached managed lookup")
	}
}
