package caddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	bridge "github.com/webong/gateway/cmd/bridge"
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

func TestCaddyHandlerUsesNextForPlanningAndPassThrough(t *testing.T) {
	executor := &recordingExecutor{}
	handler := &CaddyHandler{
		InternalToken: "test-token",
		MaxBodySize:   1024,
		executor:      executor,
	}
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		if r.URL.Path == bridge.PHPPlannerPath {
			if r.Header.Get(bridge.InternalTokenHeader) != "test-token" {
				t.Fatalf("expected configured internal planner token")
			}
			var ingress bridge.IngressRequest
			if err := json.NewDecoder(r.Body).Decode(&ingress); err != nil {
				t.Fatalf("decode planner request: %v", err)
			}
			plan := bridge.RoutePlan{Version: bridge.ProtocolVersion, Action: bridge.ActionPassThrough}
			if ingress.Path == "/owned/by/subscriber" {
				plan = bridge.RoutePlan{
					Version: bridge.ProtocolVersion,
					Action:  bridge.ActionRelay,
					Relays:  []bridge.Delivery{{URL: "https://subscriber.example.test/receive"}},
				}
			}
			return json.NewEncoder(w).Encode(plan)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Laravel application"))
		return nil
	})

	owned := httptest.NewRecorder()
	ownedRequest := httptest.NewRequest(http.MethodPost, "/owned/by/subscriber", strings.NewReader(`{"event":"created"}`))
	if err := handler.ServeHTTP(owned, ownedRequest, next); err != nil {
		t.Fatal(err)
	}
	if owned.Code != http.StatusAccepted || len(executor.queued) != 1 {
		t.Fatalf("expected owned request to be handled by Go transport, got %d and %d deliveries", owned.Code, len(executor.queued))
	}

	application := httptest.NewRecorder()
	if err := handler.ServeHTTP(application, httptest.NewRequest(http.MethodGet, "/dashboard", nil), next); err != nil {
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

func TestCaddyHandlerParsesCaddyfile(t *testing.T) {
	handler := &CaddyHandler{}
	dispenser := caddyfile.NewTestDispenser(`gateway {
	internal_token test-token
	plan_path /_internal/custom-plan
	relay_path_prefix /hooks
	max_body_size 2048
	max_workers 4
	max_queue_size 8
	request_timeout 3s
	max_idle_conns 12
	max_conns_per_host 6
	idle_conn_timeout 45s
	log_level debug
}`)

	if err := handler.UnmarshalCaddyfile(dispenser); err != nil {
		t.Fatal(err)
	}
	if handler.InternalToken != "test-token" || handler.PlanPath != "/_internal/custom-plan" || handler.RelayPathPrefix != "/hooks" {
		t.Fatalf("unexpected parsed paths or token: %+v", handler)
	}
	if handler.MaxBodySize != 2048 || handler.MaxWorkers != 4 || handler.MaxQueueSize != 8 {
		t.Fatalf("unexpected parsed capacity settings: %+v", handler)
	}
	if time.Duration(handler.RequestTimeout) != 3*time.Second || time.Duration(handler.IdleConnTimeout) != 45*time.Second {
		t.Fatalf("unexpected parsed duration settings: %+v", handler)
	}
}

func TestCaddyHandlerRequiresInternalToken(t *testing.T) {
	if err := (&CaddyHandler{}).Validate(); err == nil {
		t.Fatal("expected missing internal token to fail validation")
	}
}
