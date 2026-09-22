package main

import (
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	relayconfig "github.com/webong/gateway/cmd/internal/config"
)

func TestLaravelBackendClientDialsUnixSocket(t *testing.T) {
	file, err := os.CreateTemp("", "gateway-uds-*")
	if err != nil {
		t.Fatal(err)
	}
	socket := file.Name()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(socket) })
	defer listener.Close()

	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != "gateway-planner" {
			t.Errorf("unexpected Host %q", request.Host)
		}
		if request.URL.Path != "/_internal/gateway/plan" {
			t.Errorf("unexpected path %q", request.URL.Path)
		}
		writer.WriteHeader(http.StatusNoContent)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	client := newLaravelBackendClient(&relayconfig.Config{
		RequestTimeout:       time.Second,
		LaravelBackendSocket: socket,
	})
	response, err := client.Get("http://gateway-planner/_internal/gateway/plan")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected status %d", response.StatusCode)
	}
}
