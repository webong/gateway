package router

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	gatewayws "github.com/webong/gateway/src/spinner/cmd/bridge/websocket"
	gatewaystream "github.com/webong/gateway/src/spinner/cmd/bridge/webstream"
	automationextensions "github.com/webong/gateway/src/spinner/extensions/automation"
	"github.com/webong/gateway/src/spinner/transport"
)

// WorkerServer owns an embedded PHP worker server and its listener.
type WorkerServer interface {
	Serve() error
	Stop()
}

type plannerReady struct {
	planner bridge.ProtocolPlanner
	control *transport.ScheduleControl
	err     error
}

// RunRoadRunner composes Spinner's middleware with a managed PHP worker server.
func RunRoadRunner(ctx context.Context, newServer func(string, func(http.Handler) http.Handler) (WorkerServer, error)) error {
	if newServer == nil {
		return fmt.Errorf("a worker server factory is required")
	}
	configPath := os.Getenv("ROADRUNNER_CONFIG")
	if configPath == "" {
		return fmt.Errorf("ROADRUNNER_CONFIG is required")
	}
	spinner, err := transport.NewRouterFromEnvironment()
	if err != nil {
		return err
	}
	defer spinner.Close()
	websocketAdmission := gatewayws.NewAdmission(spinner.WebSocketMaxConnections)
	webstreamAdmission := gatewaystream.NewAdmission(spinner.WebstreamMaxConnections)
	engine := automationextensions.Runtime()
	managed, err := managedRouteProxy(spinner)
	if err != nil {
		return err
	}
	ready := make(chan plannerReady, 1)
	server, err := newServer(configPath, func(next http.Handler) http.Handler {
		planner := bridge.NewPHPPlanner(next, spinner.MaxBodySize)
		planner.InternalToken = spinner.InternalToken
		protocolPlanner := bridge.NewPHPProtocolPlanner(next, spinner.MaxBodySize)
		protocolPlanner.InternalToken = spinner.InternalToken
		control, controlErr := transport.EmbeddedScheduleControl(next, spinner, engine)
		select {
		case ready <- plannerReady{planner: protocolPlanner, control: control, err: controlErr}:
		default:
		}
		if controlErr != nil {
			return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				http.Error(writer, "Planner schedule control is unavailable", http.StatusServiceUnavailable)
			})
		}
		edge := bridge.NewEdge(planner, spinner.Executor, spinner.MaxBodySize).
			SetAutomationExecutor(engine).SetScheduleRegistrar(control).SetPassThrough(next)
		websocket := gatewayws.NewHandler(protocolPlanner, spinner.Executor, gatewayws.Config{
			MaxMessageSize: spinner.MaxBodySize, PlannerTimeout: spinner.RequestTimeout,
			MaxDuration: spinner.WebSocketMaxDuration, Admission: websocketAdmission,
		})
		webstream := gatewaystream.NewHandler(protocolPlanner, spinner.Executor, gatewaystream.Config{PlannerTimeout: spinner.RequestTimeout, HeartbeatInterval: spinner.WebstreamHeartbeat, ReconnectDelay: spinner.WebstreamRetry, MaxDuration: spinner.WebstreamMaxDuration, Admission: webstreamAdmission})
		return routerHandler(next, edge, websocket, webstream, managed)
	})
	if err != nil {
		return err
	}
	log.Print("Planner connection: managed RoadRunner PHP workers")
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	var listeners []transport.ProtocolListener
	listenerErrors := make(chan error, 3)
	if spinner.DeliveryAdminServer != nil {
		go func() {
			if err := spinner.ServeDeliveryAdmin(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				listenerErrors <- err
			}
		}()
	}
	if spinner.HasProtocolListeners() || spinner.SchedulesEnabled {
		startup, cancel := context.WithTimeout(ctx, spinner.RequestTimeout)
		var prepared plannerReady
		var err error
		select {
		case prepared = <-ready:
			err = prepared.err
		case err = <-done:
			cancel()
			server.Stop()
			if err == nil {
				err = fmt.Errorf("RoadRunner stopped before PHP became ready")
			}
			return err
		case <-startup.Done():
			err = startup.Err()
		}
		cancel()
		if err != nil {
			server.Stop()
			<-done
			return err
		}
		if spinner.SchedulesEnabled {
			scheduleCtx, cancelSchedules := context.WithCancel(ctx)
			defer cancelSchedules()
			transport.StartSchedulePolling(scheduleCtx, spinner, prepared.control, engine)
			log.Print("Schedule polling enabled")
		}
		listeners, err = spinner.ProtocolListeners(prepared.planner)
		if err != nil {
			server.Stop()
			<-done
			return err
		}
		for _, listener := range listeners {
			go func(listener transport.ProtocolListener) { listenerErrors <- listener.ListenAndServe() }(listener)
		}
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), spinner.ShutdownTimeout)
			defer cancel()
			for _, listener := range listeners {
				_ = listener.Shutdown(shutdown)
			}
		}()
	}
	select {
	case err := <-listenerErrors:
		server.Stop()
		<-done
		return err
	case err := <-done:
		server.Stop()
		return err
	case <-ctx.Done():
		server.Stop()
		return <-done
	}
}
