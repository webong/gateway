package router_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/webong/gateway/arranger/router"
)

func TestInternalControlIsDenyByDefaultAndPreservesPlannerAuthorization(t *testing.T) {
	planner := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Gateway-Internal") != "worker-secret" {
			http.NotFound(writer, request)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	allowed := router.AnyInternalRequest(
		func(request *http.Request) bool { return request.URL.Path == "/_internal/example/read" },
		func(request *http.Request) bool { return request.URL.Path == "/_internal/example/write" },
	)

	for _, test := range []struct {
		name   string
		path   string
		token  string
		policy router.InternalRequestPolicy
		want   int
	}{
		{name: "nil policy", path: "/_internal/example/read", token: "worker-secret", want: http.StatusNotFound},
		{name: "public route", path: "/registry/endpoints", token: "worker-secret", policy: allowed, want: http.StatusNotFound},
		{name: "unregistered internal route", path: "/_internal/example/delete", token: "worker-secret", policy: allowed, want: http.StatusNotFound},
		{name: "Planner rejects missing token", path: "/_internal/example/read", policy: allowed, want: http.StatusNotFound},
		{name: "approved and authenticated", path: "/_internal/example/write", token: "worker-secret", policy: allowed, want: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, nil)
			request.Header.Set("X-Gateway-Internal", test.token)
			response := httptest.NewRecorder()
			router.NewInternalControlHandler(planner, test.policy).ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}

type managedRoute struct{}

func (managedRoute) ServeWithPlanner(writer http.ResponseWriter, request *http.Request, _ http.Handler) bool {
	if request.URL.Path != "/managed" {
		return false
	}
	_, _ = writer.Write([]byte("managed"))
	return true
}

func TestPublicHandlerKeepsCapabilitiesInjected(t *testing.T) {
	planner := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("planner")) })
	hooks := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("hooks")) })
	ingress := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("ingress")) })
	handler := router.NewHandler(planner, hooks, managedRoute{}, router.PublicRoute{
		Prefix: "/transport/ingress", Handler: ingress,
	})

	for path, want := range map[string]string{
		"/health":              "planner",
		"/hooks/order":         "hooks",
		"/managed":             "managed",
		"/transport/ingress/1": "ingress",
		"/_internal/example":   "404 page not found\n",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Body.String() != want {
			t.Errorf("%s returned %q, want %q", path, response.Body.String(), want)
		}
	}
}
