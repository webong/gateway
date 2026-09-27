// Package runtime embeds RoadRunner without depending on Gateway transport code.
package runtime

import (
	"fmt"
	"net/http"

	"github.com/roadrunner-server/roadrunner/v2025/lib"
)

// Runtime owns the configured RoadRunner server and its PHP worker pool.
type Runtime struct{ server *lib.RR }

// New supplies a transport-neutral middleware adapter to RoadRunner.
func New(configPath string, overrides []string, middleware func(http.Handler) http.Handler) (*Runtime, error) {
	if middleware == nil {
		return nil, fmt.Errorf("Router middleware is required")
	}
	plugins := append(lib.DefaultPluginsList(), &plugin{middleware: middleware})
	server, err := lib.NewRR(configPath, overrides, plugins)
	if err != nil {
		return nil, err
	}
	return &Runtime{server: server}, nil
}

func (r *Runtime) Serve() error      { return r.server.Serve() }
func (r *Runtime) Stop()             { r.server.Stop() }
func (r *Runtime) Plugins() []string { return r.server.Plugins() }

type plugin struct {
	middleware func(http.Handler) http.Handler
}

func (*plugin) Init() error                                 { return nil }
func (*plugin) Name() string                                { return "gateway" }
func (p *plugin) Middleware(next http.Handler) http.Handler { return p.middleware(next) }
