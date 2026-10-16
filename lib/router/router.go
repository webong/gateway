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
	gatewaystream "github.com/webong/gateway/src/spinner/cmd/bridge/webstream"
	automationextensions "github.com/webong/gateway/src/spinner/extensions/automation"
	"github.com/webong/gateway/src/spinner/provision"
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
	if err := automationextensions.ConfigureShellSSHHandler(planner, spinner.InternalToken); err != nil {
		return fmt.Errorf("configure private Shell credential resolver: %w", err)
	}

	httpPlanner := bridge.NewPHPPlanner(planner, spinner.MaxBodySize)
	httpPlanner.InternalToken = spinner.InternalToken
	engine := automationextensions.Runtime()
	edge := bridge.NewEdge(httpPlanner, spinner.Executor, spinner.MaxBodySize).
		SetAutomationExecutor(engine).
		SetPassThrough(planner)
	protocolPlanner := bridge.NewPHPProtocolPlanner(planner, spinner.MaxBodySize)
	protocolPlanner.InternalToken = spinner.InternalToken
	control, err := transport.EmbeddedScheduleControl(planner, spinner, engine)
	if err != nil {
		return err
	}
	edge.SetScheduleRegistrar(control)
	if spinner.SchedulesEnabled {
		scheduleCtx, cancelSchedules := context.WithCancel(ctx)
		defer cancelSchedules()
		transport.StartSchedulePolling(scheduleCtx, spinner, control, engine)
	}
	websocket := gatewayws.NewHandler(protocolPlanner, spinner.Executor, gatewayws.Config{
		MaxMessageSize: spinner.MaxBodySize,
		PlannerTimeout: spinner.RequestTimeout,
		MaxConnections: spinner.WebSocketMaxConnections,
		MaxDuration:    spinner.WebSocketMaxDuration,
	})
	webstream := gatewaystream.NewHandler(protocolPlanner, spinner.Executor, gatewaystream.Config{PlannerTimeout: spinner.RequestTimeout, HeartbeatInterval: spinner.WebstreamHeartbeat, ReconnectDelay: spinner.WebstreamRetry, MaxConnections: spinner.WebstreamMaxConnections, MaxDuration: spinner.WebstreamMaxDuration})
	managed, err := ManagedRouteProxy(spinner)
	if err != nil {
		return err
	}
	handler := NewHandler(planner, edge, websocket, webstream, managed)
	listen := os.Getenv("GATEWAY_ROUTER_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen for Router requests: %w", err)
	}
	var controlServer *http.Server
	var controlListener net.Listener
	if controlListen := os.Getenv("GATEWAY_ROUTER_CONTROL_LISTEN"); controlListen != "" {
		controlListener, err = net.Listen("tcp", controlListen)
		if err != nil {
			_ = listener.Close()
			return fmt.Errorf("listen for private provisioning control: %w", err)
		}
		controlServer = NewProvisioningControlServer(planner)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	controlErrors := make(chan error, 1)
	if controlServer != nil {
		go func() { controlErrors <- controlServer.Serve(controlListener) }()
	}
	adminErrors := make(chan error, 1)
	if spinner.DeliveryAdminServer != nil {
		go func() { adminErrors <- spinner.ServeDeliveryAdmin() }()
	}
	var runErr error
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case err := <-adminErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("delivery admin listener: %w", err)
		}
	case err := <-controlErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("private provisioning control listener: %w", err)
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), spinner.ShutdownTimeout)
	defer cancel()
	publicShutdownErr := server.Shutdown(shutdown)
	if controlServer != nil {
		if err := controlServer.Shutdown(shutdown); err != nil {
			return errors.Join(publicShutdownErr, err)
		}
	}
	return errors.Join(runErr, publicShutdownErr)
}

type ManagedRouteHandler interface {
	ServeWithPlanner(http.ResponseWriter, *http.Request, http.Handler) bool
}

func ManagedRouteProxy(spinner *transport.Router) (ManagedRouteHandler, error) {
	if !spinner.ProvisioningRouteProxy {
		return nil, nil
	}
	return provision.NewRemoteRouter("http://planner.internal", spinner.InternalToken, nil, spinner.ProvisioningRouteCIDRs)
}

func NewHandler(planner, edge, websocket, webstream http.Handler, managed ManagedRouteHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if under(r.URL.Path, "/_internal") {
			http.NotFound(w, r)
			return
		}
		if managed != nil && managed.ServeWithPlanner(w, r, planner) {
			return
		}
		if under(r.URL.Path, "/hooks") {
			if gatewayws.IsUpgrade(r) {
				websocket.ServeHTTP(w, r)
				return
			}
			if webstream != nil && gatewaystream.IsRequest(r) {
				webstream.ServeHTTP(w, r)
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
