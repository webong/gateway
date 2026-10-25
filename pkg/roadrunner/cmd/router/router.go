package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	emailspinner "github.com/webong/gateway/pkg/email/spinner"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
	companionextensions "github.com/webong/gateway/pkg/spinner/extensions/companion"
	"github.com/webong/gateway/pkg/spinner/router"
	"github.com/webong/gateway/pkg/spinner/transport"
	gatewayws "github.com/webong/gateway/pkg/web/spinner/websocket"
	gatewaystream "github.com/webong/gateway/pkg/web/spinner/webstream"
)

// workerServer owns an embedded PHP worker server and its listener.
type workerServer interface {
	Serve() error
	Stop()
}

type plannerReady struct {
	planner bridge.ProtocolPlanner
	handler http.Handler
	control *transport.ScheduleControl
	err     error
}

// runRoadRunner composes Spinner's middleware with a managed PHP worker server.
func runRoadRunner(ctx context.Context, newServer func(string, func(http.Handler) http.Handler) (workerServer, error)) error {
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
	managed, err := router.ManagedRouteProxy(spinner)
	if err != nil {
		return err
	}
	ready := make(chan plannerReady, 1)
	var extensionOnce sync.Once
	var extensionErr error
	server, err := newServer(configPath, func(next http.Handler) http.Handler {
		extensionOnce.Do(func() {
			pluginDir := automationextensions.DefaultLoadConfig().ExternalDir
			if pluginDir == "" {
				return
			}
			plannerClient := &http.Client{
				Transport:     routerHandlerTransport{handler: next},
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}
			artifactClient := &http.Client{
				Timeout:       60 * time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}
			extensionErr = automationextensions.StartArtifactReconciliation(
				ctx, "http://gateway.internal", spinner.InternalToken, pluginDir,
				plannerClient, artifactClient, automationextensions.Runtime(), 15*time.Second, log.Printf,
			)
		})
		if extensionErr != nil {
			return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				http.Error(writer, "Planner extension reconciliation is unavailable", http.StatusServiceUnavailable)
			})
		}
		if resolverErr := automationextensions.ConfigureShellSSHHandler(next, spinner.InternalToken); resolverErr != nil {
			return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				http.Error(writer, "Planner Shell credential control is unavailable", http.StatusServiceUnavailable)
			})
		}
		companionActions, companionErr := bridge.NewPHPCompanionRunner(next, spinner.MaxBodySize, spinner.InternalToken)
		if companionErr != nil {
			select {
			case ready <- plannerReady{err: companionErr}:
			default:
			}
			return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				http.Error(writer, "Planner companion actions are unavailable", http.StatusServiceUnavailable)
			})
		}
		spinner.CompanionActions = companionActions
		companionextensions.Configure(companionActions)
		planner := bridge.NewPHPPlanner(next, spinner.MaxBodySize)
		planner.InternalToken = spinner.InternalToken
		protocolPlanner := bridge.NewPHPProtocolPlanner(next, spinner.MaxBodySize)
		protocolPlanner.InternalToken = spinner.InternalToken
		control, controlErr := transport.EmbeddedScheduleControl(next, spinner, engine)
		select {
		case ready <- plannerReady{planner: protocolPlanner, handler: next, control: control, err: controlErr}:
		default:
		}
		if controlErr != nil {
			return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				http.Error(writer, "Planner schedule control is unavailable", http.StatusServiceUnavailable)
			})
		}
		edge := bridge.NewEdge(planner, spinner.Executor, spinner.MaxBodySize).
			SetAutomationExecutor(spinner.Automation).SetScheduleRegistrar(control).SetPassThrough(next)
		websocket := gatewayws.NewHandler(protocolPlanner, spinner.Executor, gatewayws.Config{
			MaxMessageSize: spinner.MaxBodySize, PlannerTimeout: spinner.RequestTimeout,
			MaxDuration: spinner.WebSocketMaxDuration, Admission: websocketAdmission,
		})
		webstream := gatewaystream.NewHandler(protocolPlanner, spinner.Executor, gatewaystream.Config{PlannerTimeout: spinner.RequestTimeout, HeartbeatInterval: spinner.WebstreamHeartbeat, ReconnectDelay: spinner.WebstreamRetry, MaxDuration: spinner.WebstreamMaxDuration, Admission: webstreamAdmission})
		publicEdge := emailspinner.SubmissionHandler(edge, next, spinner.MaxBodySize)
		return router.NewHandler(next, publicEdge, websocket, webstream, managed)
	})
	if err != nil {
		return err
	}
	log.Print("Planner connection: managed RoadRunner PHP workers")
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	var listeners []transport.ProtocolListener
	listenerErrors := make(chan error, 4)
	controlListen := os.Getenv("GATEWAY_ROUTER_CONTROL_LISTEN")
	var controlServer *http.Server
	if spinner.DeliveryAdminServer != nil {
		go func() {
			if err := spinner.ServeDeliveryAdmin(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				listenerErrors <- err
			}
		}()
	}
	if spinner.HasProtocolListeners() || spinner.SchedulesEnabled || controlListen != "" {
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
		if controlListen != "" {
			controlListener, listenErr := net.Listen("tcp", controlListen)
			if listenErr != nil {
				server.Stop()
				<-done
				return fmt.Errorf("listen for private Planner control: %w", listenErr)
			}
			controlServer = router.NewInternalControlServer(prepared.handler)
			go func() {
				if serveErr := controlServer.Serve(controlListener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					listenerErrors <- fmt.Errorf("private Planner control listener: %w", serveErr)
				}
			}()
			defer func() {
				shutdown, cancel := context.WithTimeout(context.Background(), spinner.ShutdownTimeout)
				defer cancel()
				_ = controlServer.Shutdown(shutdown)
			}()
		}
		if spinner.SchedulesEnabled {
			scheduleCtx, cancelSchedules := context.WithCancel(ctx)
			defer cancelSchedules()
			if err := transport.StartSchedulePolling(scheduleCtx, spinner, prepared.control, engine, nil); err != nil {
				server.Stop()
				<-done
				return err
			}
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
