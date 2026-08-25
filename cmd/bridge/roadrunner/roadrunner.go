package roadrunner

// RoadRunnerPlugin connects the Go edge to the PHP worker.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/roadrunner-server/roadrunner/v2025/lib"
	bridge "github.com/webong/gateway/cmd/bridge"
	gatewayws "github.com/webong/gateway/cmd/bridge/websocket"
)

const RoadRunnerPluginName = "gateway"

// RoadRunnerPlugin composes the Go edge with RoadRunner's PHP HTTP worker.
// With an empty RelayPathPrefix every public path is offered to PHP for
// validation and route binding; PHP can return pass_through for ordinary
// Laravel pages.
type RoadRunnerPlugin struct {
	Executor        bridge.Executor
	MaxBodySize     int64
	RelayPathPrefix string
	PlanPath        string
	InternalToken   string

	protocolReady   chan struct{}
	protocolReadyDo sync.Once
	protocolMu      sync.RWMutex
	protocolPlanner bridge.ProtocolPlanner
}

func NewRoadRunnerPlugin(executor bridge.Executor, maxBodySize int64, internalToken string) *RoadRunnerPlugin {
	return &RoadRunnerPlugin{
		Executor:      executor,
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
	p.protocolMu.Unlock()
	p.protocolReadyDo.Do(func() { close(p.protocolReady) })
	edge := bridge.NewEdge(planner, p.Executor, p.MaxBodySize).SetPassThrough(next)
	websocketHandler := gatewayws.NewHandler(
		protocolPlanner,
		p.Executor,
		gatewayws.Config{MaxMessageSize: p.MaxBodySize},
	)

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
		edge.ServeHTTP(writer, request)
	})
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
	server *lib.RR
	plugin *RoadRunnerPlugin
}

func NewEmbeddedRoadRunner(configPath string, overrides []string, executor bridge.Executor, maxBodySize int64, internalToken string) (*EmbeddedRoadRunner, error) {
	if internalToken == "" {
		return nil, fmt.Errorf("RoadRunner internal token is required")
	}

	plugin := NewRoadRunnerPlugin(executor, maxBodySize, internalToken)
	plugins := append(lib.DefaultPluginsList(), plugin)
	server, err := lib.NewRR(configPath, overrides, plugins)
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
