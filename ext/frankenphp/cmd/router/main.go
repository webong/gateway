package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	php "github.com/webong/gateway/ext/frankenphp/runtime"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	gatewayws "github.com/webong/gateway/src/spinner/cmd/bridge/websocket"
	"github.com/webong/gateway/src/spinner/transport"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	appDir := os.Getenv("GATEWAY_ROUTER_APP_DIR")
	if appDir == "" {
		return fmt.Errorf("GATEWAY_ROUTER_APP_DIR is required")
	}
	spinner, err := transport.NewRouterFromEnvironment()
	if err != nil {
		return err
	}
	defer spinner.Close()

	planner, err := php.Start(appDir)
	if err != nil {
		return err
	}
	defer planner.Close()

	httpPlanner := bridge.NewPHPPlanner(planner, spinner.MaxBodySize)
	httpPlanner.InternalToken = spinner.InternalToken
	edge := bridge.NewEdge(httpPlanner, spinner.Executor, spinner.MaxBodySize).SetPassThrough(planner)
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
