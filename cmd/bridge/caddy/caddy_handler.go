package caddy

// CaddyHandler is the FrankenPHP/Caddy HTTP middleware adapter. It keeps the
// Go gateway in front of Laravel while using the next Caddy handler as the PHP
// application and pass-through target.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	bridge "github.com/webong/gateway/cmd/bridge"
	gatewayws "github.com/webong/gateway/cmd/bridge/websocket"
	relayconfig "github.com/webong/gateway/cmd/internal/config"
	"github.com/webong/gateway/cmd/internal/forwarding"
	"github.com/webong/gateway/cmd/internal/logging"
	"github.com/webong/gateway/cmd/internal/workers"
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
	InternalToken   string         `json:"internal_token,omitempty"`
	PlanPath        string         `json:"plan_path,omitempty"`
	RelayPathPrefix string         `json:"relay_path_prefix,omitempty"`
	MaxBodySize     int64          `json:"max_body_size,omitempty"`
	MaxWorkers      int            `json:"max_workers,omitempty"`
	MaxQueueSize    int            `json:"max_queue_size,omitempty"`
	RequestTimeout  caddy.Duration `json:"request_timeout,omitempty"`
	MaxIdleConns    int            `json:"max_idle_conns,omitempty"`
	MaxConnsPerHost int            `json:"max_conns_per_host,omitempty"`
	IdleConnTimeout caddy.Duration `json:"idle_conn_timeout,omitempty"`
	LogLevel        string         `json:"log_level,omitempty"`

	forwarder  *forwarding.Forwarder
	workerPool *workers.WorkerPool
	executor   bridge.Executor
	closeOnce  sync.Once
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
func (g *CaddyHandler) Provision(_ caddy.Context) error {
	g.applyDefaults()
	if err := g.Validate(); err != nil {
		return err
	}

	settings := &relayconfig.Config{
		MaxBodySize:     g.MaxBodySize,
		MaxWorkers:      g.MaxWorkers,
		MaxQueueSize:    g.MaxQueueSize,
		RequestTimeout:  time.Duration(g.RequestTimeout),
		MaxIdleConns:    g.MaxIdleConns,
		MaxConnsPerHost: g.MaxConnsPerHost,
		IdleConnTimeout: time.Duration(g.IdleConnTimeout),
	}
	logger := logging.NewLogger(g.LogLevel)
	g.forwarder = forwarding.NewForwarder(settings, logger)
	g.workerPool = workers.NewWorkerPool(g.MaxWorkers, g.MaxQueueSize, g.forwarder, logger)
	g.executor = bridge.NewRelayExecutor(g.forwarder, g.workerPool)

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
	if g.IdleConnTimeout < 0 {
		return fmt.Errorf("gateway idle_conn_timeout cannot be negative")
	}

	return nil
}

// Cleanup drains asynchronous deliveries and releases the forwarder's idle
// HTTP connections when Caddy unloads this module.
func (g *CaddyHandler) Cleanup() error {
	g.closeOnce.Do(func() {
		if g.workerPool != nil {
			g.workerPool.Shutdown()
		}
		if g.forwarder != nil {
			g.forwarder.CloseIdleConnections()
		}
	})

	return nil
}

// ServeHTTP captures requests for the existing Go edge. The PHP planner and
// pass-through handler are both represented by the next Caddy handler, so the
// module does not open a second Laravel listener.
func (g *CaddyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if next == nil {
		return caddyhttp.Error(http.StatusInternalServerError, fmt.Errorf("gateway next handler is not configured"))
	}
	if isPathUnder(r.URL.Path, g.planPath()) {
		http.NotFound(w, r)
		return nil
	}
	if g.RelayPathPrefix != "" && !isPathUnder(r.URL.Path, g.RelayPathPrefix) {
		return next.ServeHTTP(w, r)
	}
	if g.executor == nil {
		return caddyhttp.Error(http.StatusServiceUnavailable, fmt.Errorf("gateway transport is not configured"))
	}
	if gatewayws.IsUpgrade(r) {
		protocolPlanner := bridge.NewPHPProtocolPlanner(
			&caddyHTTPAdapter{handler: next},
			g.MaxBodySize,
		)
		protocolPlanner.InternalToken = g.InternalToken
		gatewayws.NewHandler(
			protocolPlanner,
			g.executor,
			gatewayws.Config{MaxMessageSize: g.MaxBodySize, PlannerTimeout: time.Duration(g.RequestTimeout)},
		).ServeHTTP(w, r)
		return nil
	}

	planner := &CaddyPHPPlanner{
		PHPHandler:    next,
		PlanPath:      g.planPath(),
		InternalToken: g.InternalToken,
		MaxBodySize:   g.MaxBodySize,
	}
	passThrough := &caddyPassThrough{handler: next}
	edge := bridge.NewEdge(planner, g.executor, g.MaxBodySize).SetPassThrough(passThrough)
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
	if err := p.PHPHandler.ServeHTTP(response, request); err != nil {
		return bridge.RoutePlan{}, fmt.Errorf("Caddy PHP planner handler: %w", err)
	}
	if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
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
