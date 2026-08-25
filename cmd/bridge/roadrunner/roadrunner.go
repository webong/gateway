package roadrunner

// RoadRunnerPlugin connects the Go edge to the PHP worker.

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/roadrunner-server/roadrunner/v2025/lib"
	bridge "github.com/webong/gateway/cmd/bridge"
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
}

func NewRoadRunnerPlugin(executor bridge.Executor, maxBodySize int64, internalToken string) *RoadRunnerPlugin {
	return &RoadRunnerPlugin{
		Executor:      executor,
		MaxBodySize:   maxBodySize,
		PlanPath:      bridge.PHPPlannerPath,
		InternalToken: internalToken,
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
	edge := bridge.NewEdge(planner, p.Executor, p.MaxBodySize).SetPassThrough(next)

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if isPathUnder(request.URL.Path, p.PlanPath) {
			http.NotFound(writer, request)
			return
		}
		if p.RelayPathPrefix != "" && !isPathUnder(request.URL.Path, p.RelayPathPrefix) {
			next.ServeHTTP(writer, request)
			return
		}
		edge.ServeHTTP(writer, request)
	})
}

// EmbeddedRoadRunner owns RoadRunner's lifecycle while the Go application
// owns the delivery executor injected into the custom middleware.
type EmbeddedRoadRunner struct {
	server *lib.RR
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
	return &EmbeddedRoadRunner{server: server}, nil
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
