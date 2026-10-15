package roadrunner

// RoadRunnerPlugin connects the Go edge to the PHP worker.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/webong/gateway/driver/automation"
	runtime "github.com/webong/gateway/ext/roadrunner/runtime"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	gatewayws "github.com/webong/gateway/src/spinner/cmd/bridge/websocket"
	gatewaystream "github.com/webong/gateway/src/spinner/cmd/bridge/webstream"
	automationextensions "github.com/webong/gateway/src/spinner/extensions/automation"
)

const RoadRunnerPluginName = "gateway"

// RoadRunnerPlugin composes the Go edge with RoadRunner's PHP HTTP worker.
// With an empty RelayPathPrefix every public path is offered to PHP for
// validation and route binding; PHP can return pass_through for ordinary
// Laravel pages.
type RoadRunnerPlugin struct {
	Executor        bridge.Executor
	Automation      automation.Executor
	MaxBodySize     int64
	RelayPathPrefix string
	PlanPath        string
	InternalToken   string

	protocolReady   chan struct{}
	protocolReadyDo sync.Once
	protocolMu      sync.RWMutex
	protocolPlanner bridge.ProtocolPlanner
	phpHandler      http.Handler
}

func NewRoadRunnerPlugin(executor bridge.Executor, maxBodySize int64, internalToken string) *RoadRunnerPlugin {
	return &RoadRunnerPlugin{
		Executor:      executor,
		Automation:    automationextensions.Runtime(),
		MaxBodySize:   maxBodySize,
		PlanPath:      bridge.PHPPlannerPath,
		InternalToken: internalToken,
		protocolReady: make(chan struct{}),
	}
}

func (p *RoadRunnerPlugin) Init() error {
	if p.PlanPath == "" {
		p.PlanPath = bridge.PHPPlannerPath
	}
	return nil
}

func (p *RoadRunnerPlugin) Name() string {
	return RoadRunnerPluginName
}

func (p *RoadRunnerPlugin) Middleware(next http.Handler) http.Handler {
	planner := bridge.NewPHPPlanner(next, p.MaxBodySize)
	planner.PlanPath = p.PlanPath
	planner.InternalToken = p.InternalToken
	protocolPlanner := bridge.NewPHPProtocolPlanner(next, p.MaxBodySize)
	protocolPlanner.InternalToken = p.InternalToken
	p.protocolMu.Lock()
	p.protocolPlanner = protocolPlanner
	p.phpHandler = next
	p.protocolMu.Unlock()
	p.protocolReadyDo.Do(func() { close(p.protocolReady) })
	edge := bridge.NewEdge(planner, p.Executor, p.MaxBodySize).
		SetAutomationExecutor(p.Automation).
		SetPassThrough(next)
	websocketHandler := gatewayws.NewHandler(
		protocolPlanner,
		p.Executor,
		gatewayws.Config{MaxMessageSize: p.MaxBodySize},
	)
	webstreamHandler := gatewaystream.NewHandler(protocolPlanner, executorStreamSource(p.Executor), gatewaystream.Config{HeartbeatInterval: 15 * time.Second, ReconnectDelay: 3 * time.Second})

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if isPathUnder(request.URL.Path, p.PlanPath) {
			http.NotFound(writer, request)
			return
		}
		if p.RelayPathPrefix != "" && !isPathUnder(request.URL.Path, p.RelayPathPrefix) {
			next.ServeHTTP(writer, request)
			return
		}
		if gatewayws.IsUpgrade(request) {
			websocketHandler.ServeHTTP(writer, request)
			return
		}
		if gatewaystream.IsRequest(request) {
			webstreamHandler.ServeHTTP(writer, request)
			return
		}
		edge.ServeHTTP(writer, request)
	})
}

func (p *RoadRunnerPlugin) WaitPHPHandler(ctx context.Context) (http.Handler, error) {
	if p == nil || p.protocolReady == nil {
		return nil, fmt.Errorf("RoadRunner PHP handler is not initialized")
	}
	select {
	case <-p.protocolReady:
		p.protocolMu.RLock()
		handler := p.phpHandler
		p.protocolMu.RUnlock()
		if handler == nil {
			return nil, fmt.Errorf("RoadRunner PHP handler is not configured")
		}
		return handler, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func executorStreamSource(executor bridge.Executor) gatewaystream.Source {
	source, _ := executor.(gatewaystream.Source)
	return source
}

// WaitProtocolPlanner waits until RoadRunner has assembled the HTTP
// middleware chain around the PHP worker. The returned planner invokes that
// same in-process handler, so session-oriented listeners do not need a second
// Laravel HTTP server.
func (p *RoadRunnerPlugin) WaitProtocolPlanner(ctx context.Context) (bridge.ProtocolPlanner, error) {
	if p == nil || p.protocolReady == nil {
		return nil, fmt.Errorf("RoadRunner protocol planner is not initialized")
	}
	select {
	case <-p.protocolReady:
		p.protocolMu.RLock()
		planner := p.protocolPlanner
		p.protocolMu.RUnlock()
		if planner == nil {
			return nil, fmt.Errorf("RoadRunner protocol planner is not configured")
		}
		return planner, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// EmbeddedRoadRunner owns RoadRunner's lifecycle while the Go application
// owns the delivery executor injected into the custom middleware.
type EmbeddedRoadRunner struct {
	server *runtime.Runtime
	plugin *RoadRunnerPlugin
}

func NewEmbeddedRoadRunner(configPath string, overrides []string, executor bridge.Executor, maxBodySize int64, internalToken string) (*EmbeddedRoadRunner, error) {
	if internalToken == "" {
		return nil, fmt.Errorf("RoadRunner internal token is required")
	}

	plugin := NewRoadRunnerPlugin(executor, maxBodySize, internalToken)
	server, err := runtime.New(configPath, overrides, plugin.Middleware)
	if err != nil {
		return nil, err
	}
	return &EmbeddedRoadRunner{server: server, plugin: plugin}, nil
}

func (r *EmbeddedRoadRunner) WaitProtocolPlanner(ctx context.Context) (bridge.ProtocolPlanner, error) {
	if r == nil || r.plugin == nil {
		return nil, fmt.Errorf("embedded RoadRunner protocol planner is not initialized")
	}
	return r.plugin.WaitProtocolPlanner(ctx)
}

func (r *EmbeddedRoadRunner) WaitPHPHandler(ctx context.Context) (http.Handler, error) {
	if r == nil || r.plugin == nil {
		return nil, fmt.Errorf("embedded RoadRunner PHP handler is not initialized")
	}
	return r.plugin.WaitPHPHandler(ctx)
}

func (r *EmbeddedRoadRunner) Serve() error {
	return r.server.Serve()
}

func (r *EmbeddedRoadRunner) Stop() {
	if r.server != nil {
		r.server.Stop()
	}
}

// Plugins exposes the loaded plugin names for diagnostics and integration
// tests without leaking RoadRunner's embedded server to callers.
func (r *EmbeddedRoadRunner) Plugins() []string {
	if r == nil || r.server == nil {
		return nil
	}
	return r.server.Plugins()
}

func isPathUnder(path, prefix string) bool {
	path = "/" + strings.TrimPrefix(path, "/")
	prefix = "/" + strings.Trim(strings.TrimPrefix(prefix, "/"), "/")
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

var _ interface {
	Init() error
	Name() string
	Middleware(http.Handler) http.Handler
} = (*RoadRunnerPlugin)(nil)
