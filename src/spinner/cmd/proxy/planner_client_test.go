package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	relayconfig "github.com/webong/gateway/src/spinner/internal/config"
)

func TestPlannerClientDialsUnixSocket(t *testing.T) {
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

	client := newPlannerClient(&relayconfig.Config{
		RequestTimeout: time.Second,
		PlannerSocket:  socket,
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

func TestPlannerReadinessCheckAcceptsOnlyNoContentAndDoesNotFollowRedirects(t *testing.T) {
	status := http.StatusNoContent
	redirectsFollowed := 0
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirected" {
			redirectsFollowed++
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if request.URL.Path != "/gateway/_internal/gateway/ready" {
			t.Errorf("unexpected readiness path %q", request.URL.Path)
		}
		if request.Header.Get("X-Gateway-Internal") != "private-token" {
			t.Errorf("missing private Planner token")
		}
		if status == http.StatusFound {
			writer.Header().Set("Location", "/redirected")
		}
		writer.WriteHeader(status)
	}))
	defer backend.Close()
	check := newPlannerReadinessCheck(backend.URL+"/gateway/_internal/gateway/ready", "private-token", &http.Client{Timeout: time.Second})
	if err := check(context.Background()); err != nil {
		t.Fatalf("healthy Planner was rejected: %v", err)
	}
	for _, failure := range []int{http.StatusServiceUnavailable, http.StatusFound} {
		status = failure
		if err := check(context.Background()); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", failure)) {
			t.Fatalf("Planner HTTP %d was accepted: %v", failure, err)
		}
	}
	if redirectsFollowed != 0 {
		t.Fatalf("readiness check followed %d redirects", redirectsFollowed)
	}
}

func TestSpinnerReadinessFailureDoesNotChangeLiveness(t *testing.T) {
	server := &Server{}
	server.SetReadinessCheck(func(context.Context) error { return context.DeadlineExceeded })
	ready := httptest.NewRecorder()
	server.handleReady(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	health := httptest.NewRecorder()
	server.handleHealth(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if ready.Code != http.StatusServiceUnavailable || health.Code != http.StatusOK {
		t.Fatalf("unexpected readiness/liveness status: %d/%d", ready.Code, health.Code)
	}
}
