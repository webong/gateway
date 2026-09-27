// Package router assembles Spinner with an embedded PHP Planner runtime.
package router

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	gatewayws "github.com/webong/gateway/src/spinner/cmd/bridge/websocket"
	automationextensions "github.com/webong/gateway/src/spinner/extensions/automation"
	"github.com/webong/gateway/src/spinner/transport"
)

// Planner is an in-process PHP handler with an owned runtime lifecycle.
type Planner interface {
	http.Handler
	Close()
}

// Run starts the bundled Router and stops it when ctx is canceled.
// The executable selects the PHP implementation; this library has no native
// PHP dependency and optional extensions never need to import Spinner.
func Run(ctx context.Context, startPlanner func(string) (Planner, error)) error {
	if startPlanner == nil {
		return fmt.Errorf("a Planner runtime factory is required")
	}
	appDir := os.Getenv("GATEWAY_ROUTER_APP_DIR")
	if appDir == "" {
		return fmt.Errorf("GATEWAY_ROUTER_APP_DIR is required")
	}
	spinner, err := transport.NewRouterFromEnvironment()
	if err != nil {
		return err
	}
	defer spinner.Close()

	planner, err := startPlanner(appDir)
	if err != nil {
		return err
	}
	defer planner.Close()
	log.Print("Planner connection: in-process PHP")

	httpPlanner := bridge.NewPHPPlanner(planner, spinner.MaxBodySize)
	httpPlanner.InternalToken = spinner.InternalToken
	edge := bridge.NewEdge(httpPlanner, spinner.Executor, spinner.MaxBodySize).
		SetAutomationExecutor(automationextensions.Runtime()).
		SetPassThrough(planner)
	protocolPlanner := bridge.NewPHPProtocolPlanner(planner, spinner.MaxBodySize)
	protocolPlanner.InternalToken = spinner.InternalToken
	websocket := gatewayws.NewHandler(protocolPlanner, spinner.Executor, gatewayws.Config{
		MaxMessageSize: spinner.MaxBodySize,
		PlannerTimeout: spinner.RequestTimeout,
	})
	handler := routerHandler(planner, edge, websocket)
	listen := os.Getenv("GATEWAY_ROUTER_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen for Router requests: %w", err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), spinner.ShutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdown)
}

func routerHandler(planner, edge, websocket http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if under(r.URL.Path, "/_internal") {
			http.NotFound(w, r)
			return
		}
		if under(r.URL.Path, "/hooks") {
			if gatewayws.IsUpgrade(r) {
				websocket.ServeHTTP(w, r)
				return
			}
			edge.ServeHTTP(w, r)
			return
		}
		planner.ServeHTTP(w, r)
	})
}

func under(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
