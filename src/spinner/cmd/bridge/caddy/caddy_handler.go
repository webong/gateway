package caddy

// CaddyHandler is the FrankenPHP/Caddy HTTP middleware adapter. It keeps the
// Go gateway in front of Laravel while using the next Caddy handler as the PHP
// application and pass-through target.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/cmd/bridge/deliveryadmin"
	"github.com/webong/gateway/src/spinner/cmd/bridge/deliverymembership"
	"github.com/webong/gateway/src/spinner/cmd/bridge/deliveryqueue"
	gatewayws "github.com/webong/gateway/src/spinner/cmd/bridge/websocket"
	gatewaystream "github.com/webong/gateway/src/spinner/cmd/bridge/webstream"
	automationextensions "github.com/webong/gateway/src/spinner/extensions/automation"
	relayconfig "github.com/webong/gateway/src/spinner/internal/config"
	"github.com/webong/gateway/src/spinner/internal/forwarding"
	"github.com/webong/gateway/src/spinner/internal/logging"
	"github.com/webong/gateway/src/spinner/internal/workers"
	"github.com/webong/gateway/src/spinner/provision"
	"github.com/webong/gateway/src/spinner/transport"
)

const (
	defaultCaddyMaxBodySize     int64         = 10 * 1024 * 1024
	defaultCaddyMaxWorkers      int           = 100
	defaultCaddyMaxQueueSize    int           = 1000
	defaultCaddyRequestTimeout  time.Duration = 30 * time.Second
	defaultCaddyMaxIdleConns    int           = 100
	defaultCaddyMaxConnsPerHost int           = 100
	defaultCaddyIdleConnTimeout time.Duration = 90 * time.Second
	defaultCaddyLogLevel        string        = "info"
)

func init() {
	caddy.RegisterModule((*CaddyHandler)(nil))
	httpcaddyfile.RegisterHandlerDirective("gateway", parseCaddyfile)
}

// CaddyHandler is configured as the `gateway` HTTP handler in a Caddyfile or
// as http.handlers.gateway in JSON. It deliberately owns only the Go
// transport lifecycle; Laravel remains the next handler in the chain.
type CaddyHandler struct {
	InternalToken           string          `json:"internal_token,omitempty"`
	PlanPath                string          `json:"plan_path,omitempty"`
	RelayPathPrefix         string          `json:"relay_path_prefix,omitempty"`
	ManagedRouteCIDRs       []string        `json:"managed_route_cidrs,omitempty"`
	MaxBodySize             int64           `json:"max_body_size,omitempty"`
	MaxWorkers              int             `json:"max_workers,omitempty"`
	MaxQueueSize            int             `json:"max_queue_size,omitempty"`
	RequestTimeout          caddy.Duration  `json:"request_timeout,omitempty"`
	WebSocketMaxConnections int             `json:"websocket_max_connections,omitempty"`
	WebSocketMaxDuration    caddy.Duration  `json:"websocket_max_duration,omitempty"`
	WebstreamMaxConnections int             `json:"webstream_max_connections,omitempty"`
	WebstreamMaxDuration    caddy.Duration  `json:"webstream_max_duration,omitempty"`
	MaxIdleConns            int             `json:"max_idle_conns,omitempty"`
	MaxConnsPerHost         int             `json:"max_conns_per_host,omitempty"`
	IdleConnTimeout         caddy.Duration  `json:"idle_conn_timeout,omitempty"`
	LogLevel                string          `json:"log_level,omitempty"`
	SpoolPath               string          `json:"spool_path,omitempty"`
	DeliveryDatabaseURL     string          `json:"delivery_database_url,omitempty"`
	AdminAddress            string          `json:"admin_addr,omitempty"`
	AdminAdvertiseURL       string          `json:"admin_advertise_url,omitempty"`
	MaxAttempts             int             `json:"max_attempts,omitempty"`
	InitialBackoff          caddy.Duration  `json:"initial_backoff,omitempty"`
	MaxBackoff              caddy.Duration  `json:"max_backoff,omitempty"`
	PollInterval            caddy.Duration  `json:"poll_interval,omitempty"`
	ReceiptRetention        *caddy.Duration `json:"receipt_retention,omitempty"`

	forwarder          *forwarding.Forwarder
	workerPool         *workers.WorkerPool
	deliveryQueue      deliveryqueue.Backend
	adminServer        *http.Server
	adminListener      net.Listener
	adminLease         *deliverymembership.Lease
	executor           bridge.Executor
	closeOnce          sync.Once
	websocketOnce      sync.Once
	websocketAdmission *gatewayws.Admission
	webstreamOnce      sync.Once
	webstreamAdmission *gatewaystream.Admission
	managedRouter      *provision.RemoteRouter
}

// CaddyModule returns the Caddy module information.
func (*CaddyHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.gateway",
		New: func() caddy.Module { return new(CaddyHandler) },
	}
}

// Provision starts the Go delivery infrastructure when Caddy loads the
// handler. A configuration reload creates a fresh pool before the old one is
// cleaned up, so in-flight deliveries are drained by Cleanup.
func (g *CaddyHandler) Provision(ctx caddy.Context) error {
	if g.AdminAdvertiseURL == "" {
		g.AdminAdvertiseURL = os.Getenv("GATEWAY_DELIVERY_ADMIN_ADVERTISE_URL")
	}
	g.applyDefaults()
	if err := g.Validate(); err != nil {
		return err
	}
	if len(g.ManagedRouteCIDRs) > 0 {
		managedRouter, routeErr := provision.NewRemoteRouter("http://planner.internal", g.InternalToken, nil, g.ManagedRouteCIDRs)
		if routeErr != nil {
			return routeErr
		}
		g.managedRouter = managedRouter
	}

	settings := &relayconfig.Config{
		MaxBodySize:              g.MaxBodySize,
		MaxWorkers:               g.MaxWorkers,
		MaxQueueSize:             g.MaxQueueSize,
		RequestTimeout:           time.Duration(g.RequestTimeout),
		MaxIdleConns:             g.MaxIdleConns,
		MaxConnsPerHost:          g.MaxConnsPerHost,
		IdleConnTimeout:          time.Duration(g.IdleConnTimeout),
		DeliveryMaxAttempts:      g.MaxAttempts,
		DeliveryInitialBackoff:   time.Duration(g.InitialBackoff),
		DeliveryMaxBackoff:       time.Duration(g.MaxBackoff),
		DeliveryPollInterval:     time.Duration(g.PollInterval),
		DeliveryReceiptRetention: time.Duration(*g.ReceiptRetention),
	}
	if g.SpoolPath != "" {
		// The SMTP Caddy app may use the root spool path. Its files and
		// worker must never overlap with this HTTP handler's queue.
		settings.DeliverySpoolPath = filepath.Join(g.SpoolPath, "http")
	}
	settings.DeliveryDatabaseURL = g.DeliveryDatabaseURL
	logger := logging.NewLogger(g.LogLevel)
	g.forwarder = forwarding.NewForwarder(settings, logger)
	g.workerPool = workers.NewWorkerPool(g.MaxWorkers, g.MaxQueueSize, g.forwarder, logger)
	queue, err := transport.NewDeliveryQueue(settings, g.forwarder, logger)
	if err != nil {
		g.workerPool.Shutdown()
		g.forwarder.CloseIdleConnections()
		return err
	}
	g.deliveryQueue = queue
	if queue != nil {
		g.executor = bridge.NewRelayExecutor(g.forwarder, g.workerPool, bridge.WithGatewayDeliveryQueue(queue))
	} else {
		g.executor = bridge.NewRelayExecutor(g.forwarder, g.workerPool)
	}
	if g.AdminAddress != "" {
		if queue == nil {
			_ = g.Cleanup()
			return fmt.Errorf("gateway admin_addr requires spool_path or delivery_database_url")
		}
		adminQueue := deliveryadmin.NewMultiQueue(queue)
		var options []deliveryadmin.HandlerOption
		if configured, appErr := ctx.AppIfConfigured("gateway.smtp"); appErr == nil {
			if smtpApp, ok := configured.(*SMTPApp); ok && smtpApp.deliveryQueue != nil {
				adminQueue = deliveryadmin.NewMultiQueue(queue, smtpApp.deliveryQueue)
				if smtpApp.submissionChecker != nil {
					options = append(options, deliveryadmin.WithEmailSubmission(smtpApp.deliveryQueue, smtpApp.submissionChecker, smtpApp.submissionScanner, smtpApp.MaxMessageSize, []byte(smtpApp.InternalToken), smtpApp.DSNDomain != ""))
				}
			}
		} else if !errors.Is(appErr, caddy.ErrNotConfigured) {
			_ = g.Cleanup()
			return fmt.Errorf("load gateway.smtp delivery spool: %w", appErr)
		}
		handler, err := deliveryadmin.NewHandler(adminQueue, g.InternalToken, options...)
		if err != nil {
			_ = g.Cleanup()
			return err
		}
		listener, err := net.Listen("tcp", g.AdminAddress)
		if err != nil {
			_ = g.Cleanup()
			return fmt.Errorf("listen for gateway delivery admin: %w", err)
		}
		g.adminListener = listener
		g.adminLease, err = deliverymembership.Start(g.DeliveryDatabaseURL, g.AdminAdvertiseURL, g.InternalToken)
		if err != nil {
			_ = listener.Close()
			_ = g.Cleanup()
			return fmt.Errorf("register gateway delivery admin endpoint: %w", err)
		}
		g.adminServer = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
		go func() {
			if err := g.adminServer.Serve(listener); err != nil && err != http.ErrServerClosed {
				log.Printf("gateway delivery admin listener stopped: %v", err)
			}
		}()
	}

	return nil
}

// Validate checks only explicit configuration. Zero values are valid because
// Provision applies the documented defaults before constructing the transport.
func (g *CaddyHandler) Validate() error {
	if strings.TrimSpace(g.InternalToken) == "" {
		return fmt.Errorf("gateway internal_token is required")
	}
	if g.MaxBodySize < 0 {
		return fmt.Errorf("gateway max_body_size cannot be negative")
	}
	if g.MaxWorkers < 0 {
		return fmt.Errorf("gateway max_workers cannot be negative")
	}
	if g.MaxQueueSize < 0 {
		return fmt.Errorf("gateway max_queue_size cannot be negative")
	}
	if g.MaxIdleConns < 0 {
		return fmt.Errorf("gateway max_idle_conns cannot be negative")
	}
	if g.MaxConnsPerHost < 0 {
		return fmt.Errorf("gateway max_conns_per_host cannot be negative")
	}
	if g.RequestTimeout < 0 {
		return fmt.Errorf("gateway request_timeout cannot be negative")
	}
	if g.WebstreamMaxConnections < 0 || g.WebstreamMaxDuration < 0 {
		return fmt.Errorf("gateway webstream limits cannot be negative")
	}
	if g.WebSocketMaxConnections < 0 || g.WebSocketMaxDuration < 0 {
		return fmt.Errorf("gateway websocket limits cannot be negative")
	}
	if g.IdleConnTimeout < 0 {
		return fmt.Errorf("gateway idle_conn_timeout cannot be negative")
	}
	if g.MaxAttempts < 0 || g.InitialBackoff < 0 || g.MaxBackoff < 0 || g.PollInterval < 0 || (g.ReceiptRetention != nil && *g.ReceiptRetention < 0) {
		return fmt.Errorf("gateway delivery spool attempts and durations must be non-negative")
	}
	if strings.TrimSpace(g.SpoolPath) != "" && strings.TrimSpace(g.DeliveryDatabaseURL) != "" {
		return fmt.Errorf("gateway spool_path and delivery_database_url are mutually exclusive")
	}
	if g.AdminAdvertiseURL != "" && (g.AdminAddress == "" || g.DeliveryDatabaseURL == "") {
		return fmt.Errorf("gateway admin_advertise_url requires admin_addr and delivery_database_url")
	}

	return nil
}

// Cleanup drains asynchronous deliveries and releases the forwarder's idle
// HTTP connections when Caddy unloads this module.
func (g *CaddyHandler) Cleanup() error {
	var queueErr error
	g.closeOnce.Do(func() {
		if g.adminServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := g.adminServer.Shutdown(ctx); err != nil {
				_ = g.adminServer.Close()
			}
			cancel()
		}
		if g.adminLease != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := g.adminLease.Close(ctx); err != nil {
				log.Printf("gateway delivery admin membership cleanup failed: %v", err)
			}
			cancel()
		}
		if g.deliveryQueue != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			queueErr = g.deliveryQueue.Close(ctx)
			cancel()
		}
		if g.workerPool != nil {
			g.workerPool.Shutdown()
		}
		if g.forwarder != nil {
			g.forwarder.CloseIdleConnections()
		}
	})

	return queueErr
}

// ServeHTTP captures requests for the existing Go edge. The PHP planner and
// pass-through handler are both represented by the next Caddy handler, so the
// module does not open a second Laravel listener.
func (g *CaddyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if next == nil {
		return caddyhttp.Error(http.StatusInternalServerError, fmt.Errorf("gateway next handler is not configured"))
	}
	if isPathUnder(r.URL.Path, "/_internal") {
		http.NotFound(w, r)
		return nil
	}
	if g.managedRouter != nil && g.managedRouter.ServeWithPlanner(w, r, &caddyManagedPlanner{handler: next}) {
		return nil
	}
	if g.RelayPathPrefix != "" && !isPathUnder(r.URL.Path, g.RelayPathPrefix) {
		return next.ServeHTTP(w, r)
	}
	if g.executor == nil {
		return caddyhttp.Error(http.StatusServiceUnavailable, fmt.Errorf("gateway transport is not configured"))
	}
	if r.URL.Path == "/email/submissions" {
		passThrough := &caddyPassThrough{handler: next}
		bridge.NewEdge(nil, nil, g.MaxBodySize).SetPassThrough(passThrough).ServeHTTP(w, r)
		return passThrough.err
	}
	if gatewayws.IsUpgrade(r) {
		protocolPlanner := bridge.NewPHPProtocolPlanner(
			&caddyHTTPAdapter{handler: next},
			g.MaxBodySize,
		)
		protocolPlanner.InternalToken = g.InternalToken
		g.websocketOnce.Do(func() { g.websocketAdmission = gatewayws.NewAdmission(g.WebSocketMaxConnections) })
		gatewayws.NewHandler(
			protocolPlanner,
			g.executor,
			gatewayws.Config{MaxMessageSize: g.MaxBodySize, PlannerTimeout: time.Duration(g.RequestTimeout), MaxDuration: time.Duration(g.WebSocketMaxDuration), Admission: g.websocketAdmission},
		).ServeHTTP(w, r)
		return nil
	}
	if gatewaystream.IsRequest(r) {
		protocolPlanner := bridge.NewPHPProtocolPlanner(&caddyHTTPAdapter{handler: next}, g.MaxBodySize)
		protocolPlanner.InternalToken = g.InternalToken
		source, _ := g.executor.(gatewaystream.Source)
		g.webstreamOnce.Do(func() { g.webstreamAdmission = gatewaystream.NewAdmission(g.WebstreamMaxConnections) })
		gatewaystream.NewHandler(protocolPlanner, source, gatewaystream.Config{PlannerTimeout: time.Duration(g.RequestTimeout), HeartbeatInterval: 15 * time.Second, ReconnectDelay: 3 * time.Second, MaxDuration: time.Duration(g.WebstreamMaxDuration), Admission: g.webstreamAdmission}).ServeHTTP(w, r)
		return nil
	}

	planner := &CaddyPHPPlanner{
		PHPHandler:    next,
		PlanPath:      g.planPath(),
		InternalToken: g.InternalToken,
		MaxBodySize:   g.MaxBodySize,
	}
	passThrough := &caddyPassThrough{handler: next}
	edge := bridge.NewEdge(planner, g.executor, g.MaxBodySize).
		SetAutomationExecutor(automationextensions.Runtime()).
		SetPassThrough(passThrough)
	edge.ServeHTTP(w, r)

	return passThrough.err
}

func (g *CaddyHandler) applyDefaults() {
	if g.PlanPath == "" {
		g.PlanPath = bridge.PHPPlannerPath
	}
	if g.MaxBodySize == 0 {
		g.MaxBodySize = defaultCaddyMaxBodySize
	}
	if g.MaxWorkers == 0 {
		g.MaxWorkers = defaultCaddyMaxWorkers
	}
	if g.MaxQueueSize == 0 {
		g.MaxQueueSize = defaultCaddyMaxQueueSize
	}
	if g.RequestTimeout == 0 {
		g.RequestTimeout = caddy.Duration(defaultCaddyRequestTimeout)
	}
	if g.WebstreamMaxConnections == 0 {
		g.WebstreamMaxConnections = 1000
	}
	if g.WebSocketMaxConnections == 0 {
		g.WebSocketMaxConnections = 1000
	}
	if g.WebSocketMaxDuration == 0 {
		g.WebSocketMaxDuration = caddy.Duration(24 * time.Hour)
	}
	if g.WebstreamMaxDuration == 0 {
		g.WebstreamMaxDuration = caddy.Duration(24 * time.Hour)
	}
	if g.MaxIdleConns == 0 {
		g.MaxIdleConns = defaultCaddyMaxIdleConns
	}
	if g.MaxConnsPerHost == 0 {
		g.MaxConnsPerHost = defaultCaddyMaxConnsPerHost
	}
	if g.IdleConnTimeout == 0 {
		g.IdleConnTimeout = caddy.Duration(defaultCaddyIdleConnTimeout)
	}
	if g.LogLevel == "" {
		g.LogLevel = defaultCaddyLogLevel
	}
	if g.MaxAttempts == 0 {
		g.MaxAttempts = 8
	}
	if g.InitialBackoff == 0 {
		g.InitialBackoff = caddy.Duration(5 * time.Second)
	}
	if g.MaxBackoff == 0 {
		g.MaxBackoff = caddy.Duration(time.Hour)
	}
	if g.PollInterval == 0 {
		g.PollInterval = caddy.Duration(time.Second)
	}
	if g.ReceiptRetention == nil {
		value := caddy.Duration(90 * 24 * time.Hour)
		g.ReceiptRetention = &value
	}
}

func (g *CaddyHandler) planPath() string {
	if g.PlanPath == "" {
		return bridge.PHPPlannerPath
	}
	return g.PlanPath
}

func isPathUnder(path, prefix string) bool {
	path = "/" + strings.TrimPrefix(path, "/")
	prefix = "/" + strings.Trim(strings.TrimPrefix(prefix, "/"), "/")
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// CaddyPHPPlanner invokes Laravel through the next Caddy handler using an
// internal in-process request, mirroring the existing RoadRunner planner.
type CaddyPHPPlanner struct {
	PHPHandler    caddyhttp.Handler
	PlanPath      string
	InternalToken string
	MaxBodySize   int64
}

func (p *CaddyPHPPlanner) Plan(ctx context.Context, ingress bridge.IngressRequest) (bridge.RoutePlan, error) {
	if p == nil || p.PHPHandler == nil {
		return bridge.RoutePlan{}, fmt.Errorf("Caddy PHP planner handler is not configured")
	}

	payload, err := json.Marshal(ingress)
	if err != nil {
		return bridge.RoutePlan{}, fmt.Errorf("marshal ingress request: %w", err)
	}
	planPath := p.PlanPath
	if planPath == "" {
		planPath = bridge.PHPPlannerPath
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://caddy.internal"+planPath, bytes.NewReader(payload))
	if err != nil {
		return bridge.RoutePlan{}, fmt.Errorf("create Caddy planner request: %w", err)
	}
	request.Host = ingress.Host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(bridge.InternalTokenHeader, p.InternalToken)

	response := httptest.NewRecorder()
	// FrankenPHP and php_server use Caddy's replacer, vars, and original-request
	// context. Prepare a distinct internal request while retaining cancellation.
	root := caddyhttp.GetVar(ctx, "root")
	server, _ := ctx.Value(caddyhttp.ServerCtxKey).(*caddyhttp.Server)
	request = caddyhttp.PrepareRequest(request, caddy.NewReplacer(), response, server)
	if root != nil {
		caddyhttp.SetVar(request.Context(), "root", root)
	}
	if err := p.PHPHandler.ServeHTTP(response, request); err != nil {
		log.Printf("gateway planner PHP handler failed: %v", err)
		return bridge.RoutePlan{}, fmt.Errorf("Caddy PHP planner handler: %w", err)
	}
	if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
		log.Printf("gateway planner PHP handler returned status %d", response.Code)
		return bridge.RoutePlan{}, fmt.Errorf("Caddy PHP planner returned status %d", response.Code)
	}

	return bridge.DecodeRoutePlan(response.Body, p.MaxBodySize)
}

type caddyPassThrough struct {
	handler caddyhttp.Handler
	err     error
}

type caddyHTTPAdapter struct {
	handler caddyhttp.Handler
}

type caddyManagedPlanner struct{ handler caddyhttp.Handler }

func (p *caddyManagedPlanner) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	root := caddyhttp.GetVar(request.Context(), "root")
	server, _ := request.Context().Value(caddyhttp.ServerCtxKey).(*caddyhttp.Server)
	request = caddyhttp.PrepareRequest(request, caddy.NewReplacer(), writer, server)
	if root != nil {
		caddyhttp.SetVar(request.Context(), "root", root)
	}
	if err := p.handler.ServeHTTP(writer, request); err != nil {
		http.Error(writer, "Managed route Planner failed", http.StatusBadGateway)
	}
}

func (a *caddyHTTPAdapter) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if err := a.handler.ServeHTTP(writer, request); err != nil {
		http.Error(writer, "PHP protocol planner failed", http.StatusBadGateway)
	}
}

func (p *caddyPassThrough) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.err = p.handler.ServeHTTP(w, r)
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var handler CaddyHandler
	if err := handler.UnmarshalCaddyfile(h.Dispenser); err != nil {
		return nil, err
	}
	return &handler, nil
}

// UnmarshalCaddyfile supports a block so the gateway can be placed before
// FrankenPHP's php_server handler inside an explicit route.
func (g *CaddyHandler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next()
	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		case "internal_token":
			if !d.NextArg() {
				return d.ArgErr()
			}
			g.InternalToken = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "plan_path":
			if !d.NextArg() {
				return d.ArgErr()
			}
			g.PlanPath = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "relay_path_prefix":
			if !d.NextArg() {
				return d.ArgErr()
			}
			g.RelayPathPrefix = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "managed_route_cidrs":
			if !d.NextArg() {
				return d.ArgErr()
			}
			g.ManagedRouteCIDRs = strings.Split(d.Val(), ",")
			if d.NextArg() {
				return d.ArgErr()
			}
		case "max_body_size":
			value, err := nextInt64(d)
			if err != nil {
				return err
			}
			g.MaxBodySize = value
		case "max_workers":
			value, err := nextInt(d)
			if err != nil {
				return err
			}
			g.MaxWorkers = value
		case "max_queue_size":
			value, err := nextInt(d)
			if err != nil {
				return err
			}
			g.MaxQueueSize = value
		case "request_timeout":
			value, err := nextDuration(d)
			if err != nil {
				return err
			}
			g.RequestTimeout = caddy.Duration(value)
		case "webstream_max_connections":
			value, err := nextInt(d)
			if err != nil {
				return err
			}
			g.WebstreamMaxConnections = value
		case "websocket_max_connections":
			value, err := nextInt(d)
			if err != nil {
				return err
			}
			g.WebSocketMaxConnections = value
		case "websocket_max_duration":
			value, err := nextDuration(d)
			if err != nil {
				return err
			}
			g.WebSocketMaxDuration = caddy.Duration(value)
		case "webstream_max_duration":
			value, err := nextDuration(d)
			if err != nil {
				return err
			}
			g.WebstreamMaxDuration = caddy.Duration(value)
		case "max_idle_conns":
			value, err := nextInt(d)
			if err != nil {
				return err
			}
			g.MaxIdleConns = value
		case "max_conns_per_host":
			value, err := nextInt(d)
			if err != nil {
				return err
			}
			g.MaxConnsPerHost = value
		case "idle_conn_timeout":
			value, err := nextDuration(d)
			if err != nil {
				return err
			}
			g.IdleConnTimeout = caddy.Duration(value)
		case "log_level":
			if !d.NextArg() {
				return d.ArgErr()
			}
			g.LogLevel = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "spool_path":
			if !d.NextArg() {
				return d.ArgErr()
			}
			g.SpoolPath = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "delivery_database_url":
			if !d.NextArg() {
				return d.ArgErr()
			}
			g.DeliveryDatabaseURL = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "admin_addr":
			if !d.NextArg() {
				return d.ArgErr()
			}
			g.AdminAddress = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "admin_advertise_url":
			if !d.NextArg() {
				return d.ArgErr()
			}
			g.AdminAdvertiseURL = d.Val()
			if d.NextArg() {
				return d.ArgErr()
			}
		case "max_attempts":
			value, err := nextInt(d)
			if err != nil {
				return err
			}
			g.MaxAttempts = value
		case "initial_backoff":
			value, err := nextDuration(d)
			if err != nil {
				return err
			}
			g.InitialBackoff = caddy.Duration(value)
		case "max_backoff":
			value, err := nextDuration(d)
			if err != nil {
				return err
			}
			g.MaxBackoff = caddy.Duration(value)
		case "poll_interval":
			value, err := nextDuration(d)
			if err != nil {
				return err
			}
			g.PollInterval = caddy.Duration(value)
		case "receipt_retention":
			value, err := nextDuration(d)
			if err != nil {
				return err
			}
			converted := caddy.Duration(value)
			g.ReceiptRetention = &converted
		default:
			return d.Errf("unrecognized gateway option %q", d.Val())
		}
	}

	return nil
}

func nextInt(d *caddyfile.Dispenser) (int, error) {
	if !d.NextArg() {
		return 0, d.ArgErr()
	}
	value, err := strconv.Atoi(d.Val())
	if err != nil {
		return 0, d.Errf("invalid integer %q: %v", d.Val(), err)
	}
	if d.NextArg() {
		return 0, d.ArgErr()
	}
	return value, nil
}

func nextInt64(d *caddyfile.Dispenser) (int64, error) {
	if !d.NextArg() {
		return 0, d.ArgErr()
	}
	value, err := strconv.ParseInt(d.Val(), 10, 64)
	if err != nil {
		return 0, d.Errf("invalid integer %q: %v", d.Val(), err)
	}
	if d.NextArg() {
		return 0, d.ArgErr()
	}
	return value, nil
}

func nextDuration(d *caddyfile.Dispenser) (time.Duration, error) {
	if !d.NextArg() {
		return 0, d.ArgErr()
	}
	value, err := time.ParseDuration(d.Val())
	if err != nil {
		return 0, d.Errf("invalid duration %q: %v", d.Val(), err)
	}
	if d.NextArg() {
		return 0, d.ArgErr()
	}
	return value, nil
}

var (
	_ caddy.Module                = (*CaddyHandler)(nil)
	_ caddy.Provisioner           = (*CaddyHandler)(nil)
	_ caddy.Validator             = (*CaddyHandler)(nil)
	_ caddy.CleanerUpper          = (*CaddyHandler)(nil)
	_ caddyhttp.MiddlewareHandler = (*CaddyHandler)(nil)
	_ caddyfile.Unmarshaler       = (*CaddyHandler)(nil)
	_ bridge.Planner              = (*CaddyPHPPlanner)(nil)
)
