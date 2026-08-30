package provisioning

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestRouterUsesHostnameLongestPathAndRoundRobin(t *testing.T) {
	first := namedBackend(t, "first")
	defer first.Close()
	second := namedBackend(t, "second")
	defer second.Close()
	root := namedBackend(t, "root")
	defer root.Close()

	router := NewRouter()
	server := validServerSpec()
	firstHost, firstPort := backendAddress(t, first)
	secondHost, secondPort := backendAddress(t, second)
	router.Add(server, "instance-1", firstHost, firstPort)
	router.Add(server, "instance-2", secondHost, secondPort)
	rootSpec := server
	rootSpec.ID = "root-server"
	rootSpec.Path = ""
	rootHost, rootPort := backendAddress(t, root)
	router.Add(rootSpec, "root-instance", rootHost, rootPort)

	for index, expected := range []string{"first", "second", "first"} {
		request := httptest.NewRequest(http.MethodGet, "http://socket.example.test/socket/app/key", nil)
		request.Host = "socket.example.test"
		response := httptest.NewRecorder()
		if !router.ServeIfMatched(response, request) {
			t.Fatalf("request %d was not matched", index)
		}
		if response.Body.String() != expected {
			t.Fatalf("request %d expected %q, got %q", index, expected, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, "http://socket.example.test/unprefixed", nil)
	request.Host = "socket.example.test"
	response := httptest.NewRecorder()
	if !router.ServeIfMatched(response, request) || response.Body.String() != "root" {
		t.Fatalf("expected root server fallback, got %d %q", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://other.example.test/socket/app/key", nil)
	request.Host = "other.example.test"
	if router.ServeIfMatched(httptest.NewRecorder(), request) {
		t.Fatal("unexpected match for another hostname")
	}
}

func namedBackend(t *testing.T, name string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(name))
	}))
}

func backendAddress(t *testing.T, server *httptest.Server) (string, int) {
	t.Helper()
	parsed, err := url.Parse(server.URL)
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

	return host, port
}

func validServerSpec() ServerSpec {
	return ServerSpec{
		ID:            "server-1",
		Type:          "test",
		Name:          "primary",
		Hostname:      "socket.example.test",
		Path:          "/socket",
		Driver:        DriverLocal,
		DesiredState:  DesiredRunning,
		Replicas:      1,
		Revision:      1,
		Configuration: []byte(`{}`),
	}
}
