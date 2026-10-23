package provision

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

func TestRemoteRouterResolvesPrivatePlannerAndProxiesManagedTraffic(t *testing.T) {
	backend := namedBackend(t, "managed")
	defer backend.Close()
	host, port := backendAddress(t, backend)
	requests := 0
	planner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-Gateway-Internal") != "private-token" || r.Header.Get("X-Gateway-Target") != "" {
			t.Error("managed lookup lacked shared internal authentication")
		}
		if r.URL.Path != "/_internal/provisioning/routes/resolve" || r.URL.Query().Get("hostname") != "events.example.test" {
			t.Errorf("unexpected managed lookup: %s", r.URL.String())
		}
		switch r.URL.Query().Get("path") {
		case "/socket":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"host": host, "port": port}})
		case "/unmanaged":
			w.WriteHeader(http.StatusNoContent)
		case "/unhealthy":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/invalid":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"host": "metadata.internal", "port": 80}})
		case "/denied":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"host": "169.254.169.254", "port": 80}})
		default:
			t.Errorf("unexpected lookup path: %q", r.URL.Query().Get("path"))
		}
	}))
	defer planner.Close()
	router, err := NewRemoteRouter(planner.URL, "private-token", planner.Client(), []string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path    string
		matched bool
		status  int
		body    string
	}{
		{"/socket", true, http.StatusOK, "managed"},
		{"/unmanaged", false, http.StatusOK, ""},
		{"/unhealthy", true, http.StatusServiceUnavailable, ""},
		{"/invalid", true, http.StatusBadGateway, ""},
		{"/denied", true, http.StatusBadGateway, ""},
	} {
		request := httptest.NewRequest(http.MethodGet, "http://events.example.test"+test.path, nil)
		request.Host = "events.example.test:443"
		response := httptest.NewRecorder()
		matched := router.ServeIfMatched(response, request)
		if matched != test.matched || response.Code != test.status {
			t.Errorf("%s matched=%v status=%d, want %v/%d", test.path, matched, response.Code, test.matched, test.status)
		}
		if test.body != "" && response.Body.String() != test.body {
			t.Errorf("%s body=%q, want %q", test.path, response.Body.String(), test.body)
		}
	}
	if requests != 5 {
		t.Fatalf("lookups=%d, want one per request", requests)
	}
}

func TestRemoteRouterRequiresPrivateOriginAndToken(t *testing.T) {
	for _, candidate := range []string{"", "file:///tmp/planner", "https://example.test/?redirect=1"} {
		if _, err := NewRemoteRouter(candidate, "secret", nil, []string{"127.0.0.0/8"}); err == nil {
			t.Errorf("accepted Planner URL %q", candidate)
		}
	}
	if _, err := NewRemoteRouter("http://planner.internal", "", nil, []string{"127.0.0.0/8"}); err == nil {
		t.Fatal("accepted missing internal token")
	}
	if _, err := NewRemoteRouter("http://planner.internal", "secret", nil, []string{"not-a-cidr"}); err == nil {
		t.Fatal("accepted invalid destination network")
	}
	if _, err := NewRemoteRouter("http://planner.internal", "secret", nil, nil); err == nil {
		t.Fatal("accepted missing destination networks")
	}
}

func TestRemoteRouterUsesEmbeddedPlannerHandler(t *testing.T) {
	backend := namedBackend(t, "embedded")
	defer backend.Close()
	host, port := backendAddress(t, backend)
	router, err := NewRemoteRouter("http://planner.internal", "private-token", nil, []string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	planner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Internal") != "private-token" || r.URL.Query().Get("hostname") != "socket.example.test" {
			t.Errorf("unexpected embedded Planner lookup: %s", r.URL.String())
		}
		if r.Body == nil {
			t.Error("embedded Planner lookup must provide an empty, readable request body")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"host": host, "port": port}})
	})
	request := httptest.NewRequest(http.MethodGet, "http://socket.example.test/socket", nil)
	response := httptest.NewRecorder()
	if !router.ServeWithPlanner(response, request, planner) || response.Body.String() != "embedded" {
		t.Fatalf("embedded managed route returned %d %q", response.Code, response.Body.String())
	}
}

func TestRemoteRouterNeverFollowsPlannerRedirects(t *testing.T) {
	redirected := 0
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer elsewhere.Close()
	planner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer planner.Close()
	router, err := NewRemoteRouter(planner.URL, "private-token", planner.Client(), []string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	if !router.ServeIfMatched(response, httptest.NewRequest(http.MethodGet, "http://events.example.test/socket", nil)) || response.Code != http.StatusBadGateway || redirected != 0 {
		t.Fatalf("Planner redirect was followed or accepted: status=%d redirected=%d", response.Code, redirected)
	}
}

func TestRemoteRouterProxiesManagedWebSocketUpgrade(t *testing.T) {
	upgrader := websocket.Upgrader{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("backend upgrade: %v", err)
			return
		}
		defer conn.Close()
		kind, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("backend read: %v", err)
			return
		}
		if err := conn.WriteMessage(kind, payload); err != nil {
			t.Errorf("backend write: %v", err)
		}
	}))
	defer backend.Close()
	host, port := backendAddress(t, backend)
	planner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("hostname") != "socket.example.test" || r.URL.Query().Get("path") != "/socket" {
			t.Errorf("unexpected websocket route lookup: %s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"host": host, "port": port}})
	}))
	defer planner.Close()
	router, err := NewRemoteRouter(planner.URL, "private-token", planner.Client(), []string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !router.ServeIfMatched(w, r) {
			http.NotFound(w, r)
		}
	}))
	defer public.Close()
	address := "ws" + public.URL[len("http"):]
	conn, _, err := websocket.DefaultDialer.Dial(address+"/socket", http.Header{"Host": []string{"socket.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, echoed, err := conn.ReadMessage()
	if err != nil || string(echoed) != "hello" {
		t.Fatalf("managed websocket echoed %q, err=%v", echoed, err)
	}
}
