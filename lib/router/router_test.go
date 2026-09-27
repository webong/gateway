package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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

type testPlanner struct{ closed bool }

func (*testPlanner) ServeHTTP(http.ResponseWriter, *http.Request) {}
func (p *testPlanner) Close()                                     { p.closed = true }

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
			routerHandler(planner, edge, edge).ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Body.String() != test.want {
				t.Fatalf("got %q, want %q", response.Body.String(), test.want)
			}
		})
	}
}
