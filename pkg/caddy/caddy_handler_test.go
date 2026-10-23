package caddy

import (
	"context"
	"encoding/json"
	"io"
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
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
)

type recordingExecutor struct {
	delivered []bridge.Delivery
	queued    []bridge.Delivery
}

type postStreamExecutor struct {
	recordingExecutor
	calls int
}

func (e *postStreamExecutor) OpenWebstream(_ context.Context, d bridge.GatewayDelivery) (*http.Response, error) {
	e.calls++
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + string(d.Payload) + "\n\n"))}, nil
}

func TestCaddyPostWebstreamAndBodyLimit(t *testing.T) {
	e := &postStreamExecutor{}
	g := &CaddyHandler{MaxBodySize: 4, executor: e}
	plans := 0
	next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		plans++
		var event bridge.GatewayEvent
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			return err
		}
		if event.Method != "POST" || string(event.Payload) != "test" {
			t.Error("wrong POST event")
		}
		return json.NewEncoder(w).Encode(bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream, Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://source.test", Attributes: map[string]string{"method": "POST"}, Payload: event.Payload}})
	})
	for _, body := range []string{"test", "oversized"} {
		r := httptest.NewRequest("POST", "/events", strings.NewReader(body))
		r.Header.Set("Accept", "text/event-stream")
		w := httptest.NewRecorder()
		if err := g.ServeHTTP(w, r, next); err != nil {
			t.Fatal(err)
		}
		want := 200
		if body == "oversized" {
			want = 413
		}
		if w.Code != want {
			t.Fatalf("status=%d want=%d", w.Code, want)
		}
	}
	if plans != 1 || e.calls != 1 {
		t.Fatalf("plans=%d sources=%d", plans, e.calls)
	}
}

func TestSMTPAppParsesCaddyfileGlobalOption(t *testing.T) {
	value, err := parseSMTPApp(caddyfile.NewTestDispenser(`gateway_smtp {
	listen :2526
	planner_url http://127.0.0.1:8081
	internal_token test-token
	hostname smtp.example.test
	max_message_size 2048
	max_recipients 4
	max_connections 7
	messages_per_minute 12
	message_burst 3
	read_timeout 2m
	relay_address smtp-relay.example.test:587
	relay_local_name smtp.example.test
	relay_username gateway
	relay_password secret
	relay_tls_mode starttls
	relay_timeout 12s
	dkim_resolver_url http://127.0.0.1:8081
	clamd_network unix
	clamd_address /run/clamav/clamd.sock
	clamd_timeout 7s
	domain_resolver_address 127.0.0.1:5353
	domain_lookup_timeout 2s
	quarantine_path /var/lib/gateway/email-quarantine
	quarantine_retention 720h
	dsn_domain gateway.example.test
	spool_path /var/lib/gateway/deliveries
	max_attempts 6
	initial_backoff 3s
	max_backoff 20m
	poll_interval 500ms
	receipt_retention 2160h
}`), nil)
	if err != nil {
		t.Fatal(err)
	}

	app, ok := value.(httpcaddyfile.App)
	if !ok || app.Name != "gateway.smtp" {
		t.Fatalf("unexpected Caddy app value: %#v", value)
	}

	var config SMTPApp
	if err := json.Unmarshal(app.Value, &config); err != nil {
		t.Fatal(err)
	}
	if config.Listen != ":2526" || config.PlannerURL != "http://127.0.0.1:8081" || config.InternalToken != "test-token" || config.MaxMessageSize != 2048 || config.MaxRecipients != 4 || config.MaxConnections != 7 || config.MessagesPerMinute != 12 || config.MessageBurst != 3 {
		t.Fatalf("unexpected parsed SMTP app: %+v", config)
	}
	if config.RelayAddress != "smtp-relay.example.test:587" || config.RelayLocalName != "smtp.example.test" || config.RelayUsername != "gateway" || config.RelayTLSMode != "starttls" || time.Duration(config.RelayTimeout) != 12*time.Second {
		t.Fatalf("unexpected parsed outbound SMTP relay: %+v", config)
	}
	if config.DKIMResolverURL != "http://127.0.0.1:8081" {
		t.Fatalf("unexpected DKIM resolver URL: %q", config.DKIMResolverURL)
	}
	if config.ClamdNetwork != "unix" || config.ClamdAddress != "/run/clamav/clamd.sock" || time.Duration(config.ClamdTimeout) != 7*time.Second || config.QuarantinePath != "/var/lib/gateway/email-quarantine" || time.Duration(config.QuarantineRetention) != 720*time.Hour {
		t.Fatalf("unexpected parsed malware protection: %+v", config)
	}
	if config.DomainResolverAddress != "127.0.0.1:5353" || time.Duration(config.DomainLookupTimeout) != 2*time.Second {
		t.Fatalf("unexpected author-domain resolver: %+v", config)
	}
	if config.DSNDomain != "gateway.example.test" {
		t.Fatalf("unexpected failure notice domain: %q", config.DSNDomain)
	}
	config.applyDefaults()
	config.QuarantinePath = ""
	if err := config.Validate(); err == nil {
		t.Fatal("SMTP retention without quarantine path was accepted")
	}
	if config.SpoolPath != "/var/lib/gateway/deliveries" || config.MaxAttempts != 6 || time.Duration(config.InitialBackoff) != 3*time.Second || time.Duration(config.MaxBackoff) != 20*time.Minute || time.Duration(config.PollInterval) != 500*time.Millisecond || config.ReceiptRetention == nil || time.Duration(*config.ReceiptRetention) != 90*24*time.Hour {
		t.Fatalf("unexpected parsed delivery spool: %+v", config)
	}
}

func TestSMTPAppRequiresPlannerAndToken(t *testing.T) {
	app := &SMTPApp{Listen: ":2525", MaxMessageSize: 1, MaxLineSize: 1, MaxRecipients: 1, MaxWorkers: 1, MaxQueueSize: 1, MaxIdleConns: 1, MaxConnsPerHost: 1, ReadTimeout: 1, WriteTimeout: 1, PlannerTimeout: 1, RequestTimeout: 1, IdleConnTimeout: 1}
	if err := app.Validate(); err == nil {
		t.Fatal("expected missing SMTP planner URL and token to fail validation")
	}
}

func TestSMTPAppRecipientPreflightDefaultsOnForRelayAndCanBeDisabled(t *testing.T) {
	app := &SMTPApp{RelayAddress: "relay.example.test:587"}
	app.applyDefaults()
	if app.RecipientPreflight == nil || !*app.RecipientPreflight {
		t.Fatal("relay-backed SMTP should preflight recipients by default")
	}

	value, err := parseSMTPApp(caddyfile.NewTestDispenser(`gateway_smtp {
		relay_address relay.example.test:587
		recipient_preflight false
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed := value.(httpcaddyfile.App)
	var disabled SMTPApp
	if err := json.Unmarshal(parsed.Value, &disabled); err != nil {
		t.Fatal(err)
	}
	disabled.applyDefaults()
	if disabled.RecipientPreflight == nil || *disabled.RecipientPreflight {
		t.Fatal("explicit preflight opt-out was lost")
	}
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
			if root := caddyhttp.GetVar(r.Context(), "root"); root != "/app/public" {
				t.Fatalf("expected PHP document root in synthetic context, got %v", root)
			}
			original, ok := r.Context().Value(caddyhttp.OriginalRequestCtxKey).(http.Request)
			if !ok || original.URL.Path != bridge.PHPPlannerPath {
				t.Fatalf("expected synthetic planner path in Caddy original request context, got %#v", original.URL)
			}
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
	websocket_max_connections 9
	websocket_max_duration 3h
	webstream_max_connections 7
	webstream_max_duration 2h
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
	if handler.WebstreamMaxConnections != 7 || time.Duration(handler.WebstreamMaxDuration) != 2*time.Hour {
		t.Fatalf("unexpected parsed Webstream bounds: %+v", handler)
	}
	if handler.WebSocketMaxConnections != 9 || time.Duration(handler.WebSocketMaxDuration) != 3*time.Hour {
		t.Fatalf("unexpected parsed WebSocket bounds: %+v", handler)
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

func TestSMTPAppSpoolsHTTPDeliveriesWithoutOutboundRelay(t *testing.T) {
	app := &SMTPApp{InternalToken: "test-token", SpoolPath: t.TempDir()}
	if err := app.Provision(caddyserver.Context{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Stop() })
	if app.deliveryQueue == nil {
		t.Fatal("SMTP ingress did not create a durable HTTP queue without an outbound SMTP relay")
	}
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
