// Package router assembles Spinner with an embedded PHP Planner runtime.
package router

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	arrangerrouter "github.com/webong/gateway/arranger/router"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	gatewayws "github.com/webong/gateway/pkg/spinner/bridge/websocket"
	gatewaystream "github.com/webong/gateway/pkg/spinner/bridge/webstream"
	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
	companionextensions "github.com/webong/gateway/pkg/spinner/extensions/companion"
	"github.com/webong/gateway/pkg/spinner/provision"
	"github.com/webong/gateway/pkg/spinner/transport"
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
	companionActions, err := bridge.NewPHPCompanionRunner(planner, spinner.MaxBodySize, spinner.InternalToken)
	if err != nil {
		return fmt.Errorf("configure trusted PHP companion actions: %w", err)
	}
	spinner.CompanionActions = companionActions
	companionextensions.Configure(companionActions)
	if pluginDir := automationextensions.DefaultLoadConfig().ExternalDir; pluginDir != "" {
		plannerClient := &http.Client{
			Transport:     handlerRoundTripper{handler: planner},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		artifactClient := &http.Client{
			Timeout:       60 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		if err := automationextensions.StartArtifactReconciliation(
			ctx, "http://gateway.internal", spinner.InternalToken, pluginDir,
			plannerClient, artifactClient, automationextensions.Runtime(), 15*time.Second, log.Printf,
		); err != nil {
			return fmt.Errorf("initialize extension artifact reconciler: %w", err)
		}
	}

	httpPlanner := bridge.NewPHPPlanner(planner, spinner.MaxBodySize)
	httpPlanner.InternalToken = spinner.InternalToken
	engine := automationextensions.Runtime()
	edge := bridge.NewEdge(httpPlanner, spinner.Executor, spinner.MaxBodySize).
		SetAutomationExecutor(spinner.Automation).
		SetPassThrough(planner)
	protocolPlanner := bridge.NewPHPProtocolPlanner(planner, spinner.MaxBodySize)
	protocolPlanner.InternalToken = spinner.InternalToken
	protocolListeners, err := spinner.ProtocolListeners(protocolPlanner)
	if err != nil {
		return fmt.Errorf("configure protocol listeners: %w", err)
	}
	control, err := transport.EmbeddedScheduleControl(planner, spinner, engine)
	if err != nil {
		return err
	}
	mailboxPoller, err := transport.EmbeddedMailboxPoller(planner, spinner)
	if err != nil {
		return err
	}
	control.EnableNativeIMAP()
	edge.SetScheduleRegistrar(control)
	if spinner.SchedulesEnabled {
		scheduleCtx, cancelSchedules := context.WithCancel(ctx)
		defer cancelSchedules()
		if err := transport.StartSchedulePolling(scheduleCtx, spinner, control, engine, mailboxPoller.Run); err != nil {
			return err
		}
	}
	websocket := gatewayws.NewHandler(protocolPlanner, spinner.Executor, gatewayws.Config{
		MaxMessageSize: spinner.MaxBodySize,
		PlannerTimeout: spinner.RequestTimeout,
		MaxConnections: spinner.WebSocketMaxConnections,
		MaxDuration:    spinner.WebSocketMaxDuration,
	})
	webstream := gatewaystream.NewHandler(protocolPlanner, spinner.Executor, gatewaystream.Config{PlannerTimeout: spinner.RequestTimeout, HeartbeatInterval: spinner.WebstreamHeartbeat, ReconnectDelay: spinner.WebstreamRetry, MaxConnections: spinner.WebstreamMaxConnections, MaxDuration: spinner.WebstreamMaxDuration})
	workerIngress := spinner.WorkerIngressHandler(planner)
	managed, err := ManagedRouteProxy(spinner)
	if err != nil {
		return err
	}
	handler := NewHandler(planner, edge, websocket, webstream, managed, workerIngress)
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
			return fmt.Errorf("listen for private Planner control: %w", err)
		}
		controlServer = NewInternalControlServer(planner)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	var tlsServer *http.Server
	var tlsListener net.Listener
	if tlsListen := os.Getenv("GATEWAY_ROUTER_TLS_LISTEN"); tlsListen != "" {
		certificate, err := tls.LoadX509KeyPair(os.Getenv("GATEWAY_ROUTER_TLS_CERT_FILE"), os.Getenv("GATEWAY_ROUTER_TLS_KEY_FILE"))
		if err != nil {
			_ = listener.Close()
			if controlListener != nil {
				_ = controlListener.Close()
			}
			return fmt.Errorf("load Router TLS certificate: %w", err)
		}
		plainListener, err := net.Listen("tcp", tlsListen)
		if err != nil {
			_ = listener.Close()
			if controlListener != nil {
				_ = controlListener.Close()
			}
			return fmt.Errorf("listen for Router TLS requests: %w", err)
		}
		tlsListener = tls.NewListener(plainListener, &tls.Config{
			MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate},
		})
		tlsServer = &http.Server{
			Handler: handler, ReadHeaderTimeout: 15 * time.Second,
			IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
		}
	}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	tlsErrors := make(chan error, 1)
	if tlsServer != nil {
		go func() { tlsErrors <- tlsServer.Serve(tlsListener) }()
	}
	controlErrors := make(chan error, 1)
	if controlServer != nil {
		go func() { controlErrors <- controlServer.Serve(controlListener) }()
	}
	adminErrors := make(chan error, 1)
	if spinner.DeliveryAdminServer != nil {
		go func() { adminErrors <- spinner.ServeDeliveryAdmin() }()
	}
	protocolErrors := make(chan error, len(protocolListeners))
	for _, protocolListener := range protocolListeners {
		go func(listener transport.ProtocolListener) {
			protocolErrors <- listener.ListenAndServe()
		}(protocolListener)
	}
	var runErr error
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case err := <-tlsErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("Router TLS listener: %w", err)
		}
	case err := <-adminErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("delivery admin listener: %w", err)
		}
	case err := <-controlErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("private Planner control listener: %w", err)
		}
	case err := <-protocolErrors:
		runErr = fmt.Errorf("protocol listener stopped: %w", err)
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), spinner.ShutdownTimeout)
	defer cancel()
	publicShutdownErr := server.Shutdown(shutdown)
	if tlsServer != nil {
		publicShutdownErr = errors.Join(publicShutdownErr, tlsServer.Shutdown(shutdown))
	}
	for _, listener := range protocolListeners {
		_ = listener.Shutdown(shutdown)
	}
	if controlServer != nil {
		if err := controlServer.Shutdown(shutdown); err != nil {
			return errors.Join(publicShutdownErr, err)
		}
	}
	return errors.Join(runErr, publicShutdownErr)
}

type handlerRoundTripper struct{ handler http.Handler }

func (transport handlerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	serverRequest := request.Clone(request.Context())
	if serverRequest.Body == nil {
		serverRequest.Body = http.NoBody
	}
	serverRequest.RequestURI = serverRequest.URL.RequestURI()
	serverRequest.RemoteAddr = "127.0.0.1:0"
	if serverRequest.Host == "" {
		serverRequest.Host = serverRequest.URL.Host
	}
	completed := make(chan *http.Response, 1)
	go func() {
		recorder := httptest.NewRecorder()
		transport.handler.ServeHTTP(recorder, serverRequest)
		completed <- recorder.Result()
	}()
	select {
	case response := <-completed:
		return response, nil
	case <-request.Context().Done():
		return nil, request.Context().Err()
	}
}

type ManagedRouteHandler = arrangerrouter.ManagedRouteHandler

// ingressStatus records only the HTTP outcome; credentials and MIME stay out of logs.
type ingressStatus struct {
	http.ResponseWriter
	code int
}

func (w *ingressStatus) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func ManagedRouteProxy(spinner *transport.Router) (ManagedRouteHandler, error) {
	if !spinner.ProvisioningRouteProxy {
		return nil, nil
	}
	return provision.NewRemoteRouter("http://planner.internal", spinner.InternalToken, nil, spinner.ProvisioningRouteCIDRs)
}

func NewHandler(planner, edge, websocket, webstream http.Handler, managed ManagedRouteHandler, workerIngress ...http.Handler) http.Handler {
	hooks := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gatewayws.IsUpgrade(r) {
			websocket.ServeHTTP(w, r)
			return
		}
		if webstream != nil && gatewaystream.IsRequest(r) {
			webstream.ServeHTTP(w, r)
			return
		}
		edge.ServeHTTP(w, r)
	})
	workerIngressHandler := http.NotFoundHandler()
	if len(workerIngress) != 0 && workerIngress[0] != nil {
		workerIngressHandler = loggedWorkerIngress(workerIngress[0])
	}
	return arrangerrouter.NewHandler(planner, hooks, managed, arrangerrouter.PublicRoute{
		Prefix:  "/email/worker-ingress",
		Handler: workerIngressHandler,
	})
}

func loggedWorkerIngress(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outcome := &ingressStatus{ResponseWriter: w, code: http.StatusOK}
		defer func() { log.Printf("worker ingress method=%s status=%d", r.Method, outcome.code) }()
		handler.ServeHTTP(outcome, r)
	})
}

var internalPlannerRequest = arrangerrouter.AnyInternalRequest(
	provision.InternalPlannerRequest,
	transport.EmailInternalPlannerRequest,
)

// InternalPlannerRequest allows only private routes required by capabilities
// compiled into Spinner. Each capability owns its own route policy.
func InternalPlannerRequest(request *http.Request) bool { return internalPlannerRequest(request) }

func NewInternalControlServer(planner http.Handler) *http.Server {
	return arrangerrouter.NewInternalControlServer(planner, InternalPlannerRequest)
}
