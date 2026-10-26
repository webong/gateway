package caddy

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	caddyserver "github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
	integrationextensions "github.com/webong/gateway/pkg/spinner/extensions/integration"
)

type recordingExecutor struct {
	delivered []bridge.Delivery
	queued    []bridge.Delivery
}

func (e *recordingExecutor) Deliver(_ context.Context, delivery bridge.Delivery) (bridge.Response, error) {
	e.delivered = append(e.delivered, delivery)
	return bridge.Response{StatusCode: http.StatusAccepted}, nil
}

func (e *recordingExecutor) Enqueue(delivery bridge.Delivery) bool {
	e.queued = append(e.queued, delivery)
	return true
}

func (e *recordingExecutor) EnqueueGateway(delivery bridge.GatewayDelivery) bool {
	if delivery.Protocol != bridge.ProtocolHTTP {
		return false
	}
	return e.Enqueue(bridge.Delivery{URL: delivery.Target, Body: delivery.Payload, Headers: delivery.Headers})
}

func TestCaddyHandlerUsesNextForPlanningAndPassThrough(t *testing.T) {
	executor := &recordingExecutor{}
	handler := &CaddyHandler{
		InternalToken: "test-token",
		MaxBodySize:   1024,
		executor:      executor,
	}
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Laravel application"))
		return nil
	})
	if err := integrationextensions.DefaultRegistry.RegisterHTTP("test/caddy-web", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/owned/by/subscriber" {
			executor.Enqueue(bridge.Delivery{URL: "https://subscriber.example.test/receive"})
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_ = next.ServeHTTP(w, r)
	})); err != nil {
		t.Fatal(err)
	}
	defer integrationextensions.DefaultRegistry.Unregister("test/caddy-web")

	owned := httptest.NewRecorder()
	ownedRequest := httptest.NewRequest(http.MethodPost, "/owned/by/subscriber", strings.NewReader(`{"event":"created"}`))
	ownedContext := context.WithValue(ownedRequest.Context(), caddyhttp.VarsCtxKey, map[string]any{"root": "/app/public"})
	ownedRequest = ownedRequest.WithContext(context.WithValue(ownedContext, caddyhttp.OriginalRequestCtxKey, *ownedRequest))
	if err := handler.ServeHTTP(owned, ownedRequest, next); err != nil {
		t.Fatal(err)
	}
	if owned.Code != http.StatusAccepted || len(executor.queued) != 1 {
		t.Fatalf("expected owned request to be handled by Go transport, got %d and %d deliveries", owned.Code, len(executor.queued))
	}

	application := httptest.NewRecorder()
	applicationRequest := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	applicationRequest = applicationRequest.WithContext(context.WithValue(applicationRequest.Context(), caddyhttp.VarsCtxKey, map[string]any{"root": "/app/public"}))
	if err := handler.ServeHTTP(application, applicationRequest, next); err != nil {
		t.Fatal(err)
	}
	if application.Code != http.StatusOK || application.Body.String() != "Laravel application" {
		t.Fatalf("expected PHP pass-through, got %d %q", application.Code, application.Body.String())
	}
}

func TestCaddyHandlerBlocksPublicPlannerPath(t *testing.T) {
	handler := &CaddyHandler{InternalToken: "test-token", executor: &recordingExecutor{}}
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusTeapot)
		return nil
	})

	record := httptest.NewRecorder()
	if err := handler.ServeHTTP(record, httptest.NewRequest(http.MethodPost, bridge.PHPPlannerPath, nil), next); err != nil {
		t.Fatal(err)
	}
	if record.Code != http.StatusNotFound {
		t.Fatalf("expected planner path to be private, got %d", record.Code)
	}
}

func TestCaddyHandlerRoutesManagedWorkloadThroughPrivatePHP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("managed"))
	}))
	defer backend.Close()
	parsed, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	handler := &CaddyHandler{InternalToken: "test-token", RelayPathPrefix: "/hooks", ManagedRouteCIDRs: []string{"127.0.0.0/8"}}
	if err := handler.Provision(caddyserver.Context{}); err != nil {
		t.Fatal(err)
	}
	defer handler.Cleanup()
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		if r.URL.Path == "/_internal/provisioning/routes/resolve" {
			if r.Header.Get("X-Gateway-Internal") != "test-token" || r.URL.Query().Get("hostname") != "managed.example.test" {
				t.Errorf("unexpected internal managed lookup: %s", r.URL.String())
			}
			if r.URL.Query().Get("path") != "/socket" {
				w.WriteHeader(http.StatusNoContent)
				return nil
			}
			return json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"host": host, "port": portNumber}})
		}
		_, _ = w.Write([]byte("Laravel"))
		return nil
	})
	request := httptest.NewRequest(http.MethodGet, "http://managed.example.test/socket", nil)
	request = request.WithContext(context.WithValue(request.Context(), caddyhttp.VarsCtxKey, map[string]any{"root": "/app/public"}))
	response := httptest.NewRecorder()
	if err := handler.ServeHTTP(response, request, next); err != nil || response.Body.String() != "managed" {
		t.Fatalf("managed Caddy request returned %d %q err=%v", response.Code, response.Body.String(), err)
	}
	request = httptest.NewRequest(http.MethodGet, "http://managed.example.test/dashboard", nil)
	request = request.WithContext(context.WithValue(request.Context(), caddyhttp.VarsCtxKey, map[string]any{"root": "/app/public"}))
	response = httptest.NewRecorder()
	if err := handler.ServeHTTP(response, request, next); err != nil || response.Body.String() != "Laravel" {
		t.Fatalf("unmatched Caddy request returned %d %q err=%v", response.Code, response.Body.String(), err)
	}
}

func TestCaddyHandlerParsesCaddyfile(t *testing.T) {
	handler := &CaddyHandler{}
	dispenser := caddyfile.NewTestDispenser(`gateway {
	internal_token test-token
	plan_path /_internal/custom-plan
	relay_path_prefix /hooks
	managed_route_cidrs 10.244.0.0/16,10.0.0.10/32
	max_body_size 2048
	max_workers 4
	max_queue_size 8
	request_timeout 3s
	max_idle_conns 12
	max_conns_per_host 6
	idle_conn_timeout 45s
	log_level debug
	spool_path /var/lib/gateway/deliveries
	admin_addr 127.0.0.1:5052
	max_attempts 6
	initial_backoff 3s
	max_backoff 20m
	poll_interval 500ms
	receipt_retention 2160h
}`)

	if err := handler.UnmarshalCaddyfile(dispenser); err != nil {
		t.Fatal(err)
	}
	if handler.InternalToken != "test-token" || handler.PlanPath != "/_internal/custom-plan" || handler.RelayPathPrefix != "/hooks" {
		t.Fatalf("unexpected parsed paths or token: %+v", handler)
	}
	if len(handler.ManagedRouteCIDRs) != 2 || handler.ManagedRouteCIDRs[0] != "10.244.0.0/16" {
		t.Fatalf("unexpected managed route CIDRs: %v", handler.ManagedRouteCIDRs)
	}
	if handler.MaxBodySize != 2048 || handler.MaxWorkers != 4 || handler.MaxQueueSize != 8 {
		t.Fatalf("unexpected parsed capacity settings: %+v", handler)
	}
	if time.Duration(handler.RequestTimeout) != 3*time.Second || time.Duration(handler.IdleConnTimeout) != 45*time.Second {
		t.Fatalf("unexpected parsed duration settings: %+v", handler)
	}
	if handler.SpoolPath != "/var/lib/gateway/deliveries" || handler.AdminAddress != "127.0.0.1:5052" || handler.MaxAttempts != 6 || handler.ReceiptRetention == nil || time.Duration(*handler.ReceiptRetention) != 90*24*time.Hour {
		t.Fatalf("unexpected parsed spool settings: %+v", handler)
	}
}

func TestCaddyHandlerUsesSeparateHTTPSpoolAndRejectsOverlappingReload(t *testing.T) {
	root := t.TempDir()
	first := &CaddyHandler{InternalToken: "test-token", SpoolPath: root}
	if err := first.Provision(caddyserver.Context{}); err != nil {
		t.Fatal(err)
	}
	if first.deliveryQueue == nil {
		t.Fatal("Caddy HTTP handler did not create a durable queue")
	}
	second := &CaddyHandler{InternalToken: "test-token", SpoolPath: root}
	if err := second.Provision(caddyserver.Context{}); err != deliveryqueue.ErrSpoolInUse {
		t.Fatalf("overlapping Caddy spool worker was not rejected: %v", err)
	}
	if err := first.Cleanup(); err != nil {
		t.Fatal(err)
	}
	third := &CaddyHandler{InternalToken: "test-token", SpoolPath: root}
	if err := third.Provision(caddyserver.Context{}); err != nil {
		t.Fatalf("spool did not reopen after old handler stopped: %v", err)
	}
	t.Cleanup(func() { _ = third.Cleanup() })
}

func TestCaddyHandlerPrivateDeliveryAdminListener(t *testing.T) {
	handler := &CaddyHandler{InternalToken: "test-token", SpoolPath: t.TempDir(), AdminAddress: "127.0.0.1:0"}
	if err := handler.Provision(caddyserver.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Cleanup() })
	if handler.adminListener == nil {
		t.Fatal("private delivery listener was not started")
	}
	url := "http://" + handler.adminListener.Addr().String() + "/deliveries"
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request returned %d", response.StatusCode)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated request returned %d", response.StatusCode)
	}
}

func TestCaddyHandlerRequiresInternalToken(t *testing.T) {
	if err := (&CaddyHandler{}).Validate(); err == nil {
		t.Fatal("expected missing internal token to fail validation")
	}
}
