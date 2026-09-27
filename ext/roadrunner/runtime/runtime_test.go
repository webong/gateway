package runtime

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareUsesInjectedHandler(t *testing.T) {
	called := false
	p := &plugin{middleware: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	}}
	if p.Name() != "gateway" || p.Init() != nil {
		t.Fatal("unexpected plugin registration")
	}
	response := httptest.NewRecorder()
	p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if !called || response.Code != http.StatusNoContent {
		t.Fatal("injected middleware was not used")
	}
}

func TestNewRequiresMiddleware(t *testing.T) {
	if _, err := New("unused.yaml", nil, nil); err == nil {
		t.Fatal("expected a missing middleware error")
	}
}
