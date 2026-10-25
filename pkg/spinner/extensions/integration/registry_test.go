package integrationextensions

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistryHTTPSelectionFailsClosed(t *testing.T) {
	registry := NewRegistry()
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/hooks/example", nil)
	response := httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing provider returned %d", response.Code)
	}

	registry.mounted["example/web"] = mounted{http: []httpMount{{prefix: "/", proxy: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	})}}}
	response = httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("selected provider returned %d", response.Code)
	}

	registry.mounted["example/other"] = mounted{http: []httpMount{{prefix: "/", proxy: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}}}
	response = httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("ambiguous providers returned %d", response.Code)
	}
}

func TestRegistrySelectsTheLongestHTTPMount(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterHTTP("example/web", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	})); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterHTTPMount("example/email", "/email", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusCreated)
	})); err != nil {
		t.Fatal(err)
	}

	for requestPath, expected := range map[string]int{
		"/hooks/example": http.StatusAccepted,
		"/email":         http.StatusCreated,
		"/email/worker":  http.StatusCreated,
		"/emails":        http.StatusAccepted,
	} {
		response := httptest.NewRecorder()
		registry.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway.test"+requestPath, nil))
		if response.Code != expected {
			t.Fatalf("%s selected status %d, want %d", requestPath, response.Code, expected)
		}
	}
}
