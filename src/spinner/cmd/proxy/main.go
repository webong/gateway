package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	centrifugoruntime "github.com/webong/gateway/ext/centrifugo/runtime"
	mercureruntime "github.com/webong/gateway/ext/mercure/runtime"
	reverbruntime "github.com/webong/gateway/ext/reverb/runtime"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/cmd/bridge/deliveryqueue"
	dnsgateway "github.com/webong/gateway/src/spinner/cmd/bridge/dns"
	roadrunner "github.com/webong/gateway/src/spinner/cmd/bridge/roadrunner"
	smtpgateway "github.com/webong/gateway/src/spinner/cmd/bridge/smtp"
	smtpout "github.com/webong/gateway/src/spinner/cmd/bridge/smtpout"
	gatewayws "github.com/webong/gateway/src/spinner/cmd/bridge/websocket"
	"github.com/webong/gateway/src/spinner/internal/automation"
	"github.com/webong/gateway/src/spinner/internal/automation/schedules"
	relayconfig "github.com/webong/gateway/src/spinner/internal/config"
	"github.com/webong/gateway/src/spinner/internal/forwarding"
	"github.com/webong/gateway/src/spinner/internal/logging"
	"github.com/webong/gateway/src/spinner/internal/workers"
	"github.com/webong/gateway/src/spinner/provision"
)

type extensionRouteHandler interface {
	ServeIfMatched(http.ResponseWriter, *http.Request) bool
}

type protocolListener interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

type Server struct {
	config           *relayconfig.Config
	logger           *logging.Logger
	forwarder        *forwarding.Forwarder
	workerPool       *workers.WorkerPool
	executorOptions  []bridge.RelayExecutorOption
	deliveryQueue    *deliveryqueue.Queue
	relayHandle      http.Handler
	websocketHandler http.Handler
	extensionHandler extensionRouteHandler
	httpServer       *http.Server
}

func NewServer(config *relayconfig.Config, logger *logging.Logger) *Server {
	forwarder := forwarding.NewForwarder(config, logger)
	workerPool := workers.NewWorkerPool(config.MaxWorkers, config.MaxQueueSize, forwarder, logger)

	server := &Server{
		config:     config,
		logger:     logger,
		forwarder:  forwarder,
		workerPool: workerPool,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/metrics", server.handleMetrics)
	// The selected runtime populates the relay handler. Health and metrics stay
	// on the Go host in every mode.
	mux.HandleFunc("/", server.handleRelay)

	server.httpServer = &http.Server{
		Addr:         ":" + config.Port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return server
}

// SetRelayPlanner connects the Go transport edge to the PHP control plane.
// PHP owns validation, registry lookup, subscriber resolution, and route
// binding; Go only executes the resulting network plan.
func (s *Server) SetRelayPlanner(planner bridge.Planner) {
	s.SetRelayHandler(s.NewRelayEdge(planner))
}

// SetRelayHandler mounts a fully composed Go edge. Runtime adapters use this
// when they need to provide both a planner and a Laravel pass-through handler.
func (s *Server) SetRelayHandler(handler http.Handler) {
	s.relayHandle = handler
}

func (s *Server) SetWebSocketHandler(handler http.Handler) {
	s.websocketHandler = handler
}

func (s *Server) SetExtensionHandler(handler extensionRouteHandler) {
	s.extensionHandler = handler
}

func (s *Server) NewRelayEdge(planner bridge.Planner, passThrough ...http.Handler) http.Handler {
	edge := bridge.NewEdge(
		planner,
		s.NewGatewayExecutor(),
		s.config.MaxBodySize,
	)
	if len(passThrough) > 0 {
		edge.SetPassThrough(passThrough[0])
	}
	return edge
}

func (s *Server) NewGatewayExecutor() *bridge.RelayExecutor {
	return bridge.NewRelayExecutor(s.forwarder, s.workerPool, s.executorOptions...)
}

func (s *Server) ConfigureSMTPRelay() error {
	if s == nil || s.config == nil || s.config.SMTPRelayAddress == "" {
		return nil
	}

	sender, err := smtpout.NewSender(smtpout.Config{
		Address:    s.config.SMTPRelayAddress,
		LocalName:  s.config.SMTPRelayLocalName,
		ServerName: s.config.SMTPRelayServerName,
		Username:   s.config.SMTPRelayUsername,
		Password:   s.config.SMTPRelayPassword,
		TLSMode:    smtpout.TLSMode(s.config.SMTPRelayTLSMode),
		Timeout:    s.config.SMTPRelayTimeout,
	})
	if err != nil {
		return err
	}
	adapter, err := smtpout.NewGatewayAdapter(sender)
	if err != nil {
		return err
	}
	queue, err := deliveryqueue.New(deliveryqueue.Config{
		Path: s.config.DeliverySpoolPath,
		Adapters: map[string]bridge.GatewayDeliveryAdapter{
			string(bridge.ProtocolSMTP): adapter,
		},
		Observer: deliveryqueue.ObserverFunc(func(event deliveryqueue.Event) {
			switch event.State {
			case deliveryqueue.StateFailed:
				s.logger.Error("Delivery %s failed after %d attempts: %s", event.ID, event.Attempts, event.Error)
			case deliveryqueue.StateDeferred:
				s.logger.Warn("Delivery %s deferred after attempt %d: %s", event.ID, event.Attempts, event.Error)
			default:
				s.logger.Debug("Delivery %s entered state %s", event.ID, event.State)
			}
		}),
		MaxAttempts:     s.config.DeliveryMaxAttempts,
		InitialBackoff:  s.config.DeliveryInitialBackoff,
		MaxBackoff:      s.config.DeliveryMaxBackoff,
		DeliveryTimeout: s.config.SMTPRelayTimeout,
		PollInterval:    s.config.DeliveryPollInterval,
	})
	if err != nil {
		return err
	}
	s.deliveryQueue = queue
	s.executorOptions = append(s.executorOptions, bridge.WithGatewayDeliveryQueue(queue))
	s.logger.Info("Configured outbound SMTP relay at %s", s.config.SMTPRelayAddress)
	s.logger.Info("Using durable delivery spool at %s", s.config.DeliverySpoolPath)
	return nil
}

func (s *Server) handleRelay(w http.ResponseWriter, r *http.Request) {
	if s.extensionHandler != nil && s.extensionHandler.ServeIfMatched(w, r) {
		return
	}
	if s.websocketHandler != nil && gatewayws.IsUpgrade(r) {
		s.websocketHandler.ServeHTTP(w, r)
		return
	}
	if s.relayHandle == nil {
		http.Error(w, "PHP relay planner is not configured", http.StatusServiceUnavailable)
		return
	}
	s.relayHandle.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	total, success, errors := s.forwarder.GetStats()

	metrics := map[string]interface{}{
		"forwarded_requests_total":   total,
		"forwarded_requests_success": success,
		"forwarded_requests_errors":  errors,
		"worker_pool_size":           s.config.MaxWorkers,
		"worker_queue_capacity":      s.config.MaxQueueSize,
	}
	if s.deliveryQueue != nil {
		pending, failed, err := s.deliveryQueue.Counts()
		if err != nil {
			s.logger.Error("Failed to read delivery spool metrics: %v", err)
		} else {
			metrics["delivery_spool_pending"] = pending
			metrics["delivery_spool_failed"] = failed
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		s.logger.Error("Failed to write metrics: %v", err)
	}
}

func (s *Server) Start() error {
	s.logger.Info("Starting Go relay host on port %s", s.config.Port)
	s.logger.Info("Worker pool size: %d", s.config.MaxWorkers)
	s.logger.Info("Max queue size: %d", s.config.MaxQueueSize)
	s.logger.Info("Request timeout: %v", s.config.RequestTimeout)
	s.logger.Info("Max body size: %d bytes", s.config.MaxBodySize)

	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down server...")

	httpErr := s.httpServer.Shutdown(ctx)
	return errors.Join(httpErr, s.closeExecutionResources(ctx))
}

func (s *Server) closeExecutionResources(ctx context.Context) error {
	var queueErr error
	if s.deliveryQueue != nil {
		queueErr = s.deliveryQueue.Close(ctx)
	}
	s.workerPool.Shutdown()
	return queueErr
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := relayconfig.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger := logging.NewLogger(config.LogLevel)
	if config.Runtime == "diagnostic" && (config.SMTPAddress != "" || config.DNSAddress != "") {
		return fmt.Errorf("protocol listeners require GATEWAY_RUNTIME=http or roadrunner")
	}
	server := NewServer(config, logger)
	if err := server.ConfigureSMTPRelay(); err != nil {
		server.workerPool.Shutdown()
		return fmt.Errorf("failed to configure outbound SMTP relay: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if err := server.closeExecutionResources(ctx); err != nil {
			logger.Error("Failed to close delivery resources: %v", err)
		}
	}()
	switch config.Runtime {
	case "roadrunner":
		return runEmbeddedRoadRunner(config, server, logger)
	case "http":
		return runHTTPBackend(config, server, logger)
	case "diagnostic":
		return runDiagnostic(config, server, logger)
	default:
		return fmt.Errorf("unsupported runtime %q", config.Runtime)
	}
}

func runDiagnostic(config *relayconfig.Config, server *Server, logger *logging.Logger) error {
	return runServices(config, server, logger)
}

func runServices(config *relayconfig.Config, server *Server, logger *logging.Logger, listeners ...protocolListener) error {
	serverErrors := make(chan error, 1+len(listeners))
	go func() { serverErrors <- server.Start() }()
	for _, listener := range listeners {
		go func(listener protocolListener) { serverErrors <- listener.ListenAndServe() }(listener)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	var listenerErr error
	select {
	case <-sigChan:
		logger.Info("Received shutdown signal")
	case err := <-serverErrors:
		if !expectedListenerClose(err) {
			listenerErr = err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer cancel()

	for _, listener := range listeners {
		if err := listener.Shutdown(ctx); err != nil && !expectedListenerClose(err) {
			return fmt.Errorf("protocol listener shutdown error: %w", err)
		}
	}
	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}
	if listenerErr != nil {
		return fmt.Errorf("gateway listener failed: %w", listenerErr)
	}

	logger.Info("Server stopped gracefully")
	return nil
}

func expectedListenerClose(err error) bool {
	return err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, smtpgateway.ErrServerClosed) || errors.Is(err, dnsgateway.ErrServerClosed)
}

func newSMTPServer(config *relayconfig.Config, planner bridge.ProtocolPlanner, executor bridge.GatewayExecutor) (*smtpgateway.Server, error) {
	tlsConfig, err := smtpgateway.LoadTLSConfig(config.SMTPTLSCertFile, config.SMTPTLSKeyFile)
	if err != nil {
		return nil, err
	}

	return smtpgateway.NewServer(smtpgateway.Config{
		Address:        config.SMTPAddress,
		Hostname:       config.SMTPHostname,
		MaxMessageSize: config.SMTPMaxMessageSize,
		MaxRecipients:  config.SMTPMaxRecipients,
		ReadTimeout:    config.SMTPReadTimeout,
		WriteTimeout:   config.SMTPWriteTimeout,
		PlannerTimeout: config.SMTPPlannerTimeout,
		TLSConfig:      tlsConfig,
		ImplicitTLS:    config.SMTPImplicitTLS,
		AuthEnabled:    config.SMTPAuthEnabled,
	}, planner, executor)
}

func newDNSServer(config *relayconfig.Config, planner bridge.ProtocolPlanner, executor *bridge.RelayExecutor) (*dnsgateway.Server, error) {
	return dnsgateway.NewServer(dnsgateway.Config{
		Address:            config.DNSAddress,
		Zone:               config.DNSZone,
		Nameservers:        config.DNSNameservers,
		SOAEmail:           config.DNSSOAEmail,
		TTL:                config.DNSTTL,
		PlannerTimeout:     config.DNSPlannerTimeout,
		ReadTimeout:        config.DNSReadTimeout,
		WriteTimeout:       config.DNSWriteTimeout,
		MaxResponseRecords: config.DNSMaxResponseRecords,
		RateLimit:          config.DNSRateLimit,
		RateBurst:          config.DNSRateBurst,
	}, planner, executor)
}

func runHTTPBackend(config *relayconfig.Config, server *Server, logger *logging.Logger) error {
	client := newLaravelBackendClient(config)
	if config.SchedulesEnabled {
		control, controlErr := schedules.NewHTTPControlPlane(config.LaravelBackendURL, config.InternalToken, client)
		if controlErr != nil {
			return fmt.Errorf("failed to initialize schedules: %w", controlErr)
		}
		scheduleCtx, cancelSchedules := context.WithCancel(context.Background())
		defer cancelSchedules()
		executor := server.NewGatewayExecutor()
		(schedules.Runtime{
			Control:  control,
			Interval: config.SchedulePollInterval,
			LogError: logger.Error,
			Enqueue: func(delivery automation.Delivery) bool {
				return executor.Enqueue(bridge.Delivery{
					URL: delivery.URL, Method: delivery.Method,
					Headers: delivery.Headers, Body: []byte(delivery.Body),
				})
			},
		}).Start(scheduleCtx)
		logger.Info("Schedule polling enabled")
	}
	runtime, err := bridge.NewHTTPRuntime(
		config.LaravelBackendURL,
		config.InternalToken,
		config.MaxBodySize,
		client,
	)
	if err != nil {
		server.workerPool.Shutdown()
		return fmt.Errorf("failed to initialize HTTP Laravel runtime: %w", err)
	}
	defer runtime.Close()

	edge := server.NewRelayEdge(runtime.Planner(), runtime.PassThrough())
	server.SetRelayHandler(runtime.Handler(edge))
	if config.LaravelBackendSocket != "" {
		logger.Info("Using HTTP Laravel backend through Unix socket %s", config.LaravelBackendSocket)
	} else {
		logger.Info("Using HTTP Laravel backend at %s", config.LaravelBackendURL)
	}

	protocolPlanner, err := bridge.NewHTTPProtocolPlanner(
		config.LaravelBackendURL,
		client,
		config.MaxBodySize,
		config.InternalToken,
	)
	if err != nil {
		server.workerPool.Shutdown()
		return fmt.Errorf("failed to initialize HTTP protocol planner: %w", err)
	}
	server.SetWebSocketHandler(gatewayws.NewHandler(
		protocolPlanner,
		server.NewGatewayExecutor(),
		gatewayws.Config{MaxMessageSize: config.MaxBodySize, PlannerTimeout: config.RequestTimeout},
	))

	var provisioningRuntime *provision.Runtime
	if config.Provisioning.Enabled {
		workloads := make([]provision.Workload, 0, 3)
		if config.Reverb.Enabled {
			workloads = append(workloads, reverbruntime.NewWorkload(config.Reverb))
		}
		if config.Mercure.Enabled {
			workloads = append(workloads, mercureruntime.NewWorkload(config.Mercure))
		}
		if config.Centrifugo.Enabled {
			workloads = append(workloads, centrifugoruntime.NewWorkload(config.Centrifugo))
		}
		provisioningRuntime, err = provision.Start(
			config.Provisioning.Config,
			config.LaravelBackendURL,
			config.InternalToken,
			client,
			logger,
			workloads...,
		)
		if err != nil {
			server.workerPool.Shutdown()
			return fmt.Errorf("failed to start provisioning extension: %w", err)
		}
		defer provisioningRuntime.Stop()
		server.SetExtensionHandler(provisioningRuntime)
		logger.Info("Provisioning reconciliation enabled for node %s", config.Provisioning.NodeID)
	}

	executor := server.NewGatewayExecutor()
	listeners := make([]protocolListener, 0, 2)
	if config.SMTPAddress != "" {
		smtpServer, smtpErr := newSMTPServer(config, protocolPlanner, executor)
		if smtpErr != nil {
			server.workerPool.Shutdown()
			return fmt.Errorf("failed to initialize SMTP listener: %w", smtpErr)
		}
		logger.Info("Using SMTP listener on %s", config.SMTPAddress)
		listeners = append(listeners, smtpServer)
	}
	if config.DNSAddress != "" {
		dnsServer, dnsErr := newDNSServer(config, protocolPlanner, executor)
		if dnsErr != nil {
			server.workerPool.Shutdown()
			return fmt.Errorf("failed to initialize DNS listener: %w", dnsErr)
		}
		logger.Info("Using authoritative DNS listener on %s for %s", config.DNSAddress, config.DNSZone)
		listeners = append(listeners, dnsServer)
	}

	return runServices(config, server, logger, listeners...)
}

func runEmbeddedRoadRunner(config *relayconfig.Config, server *Server, logger *logging.Logger) error {
	runner, err := roadrunner.NewEmbeddedRoadRunner(
		config.RoadRunnerConfigPath,
		nil,
		server.NewGatewayExecutor(),
		config.MaxBodySize,
		config.InternalToken,
	)
	if err != nil {
		server.workerPool.Shutdown()
		return fmt.Errorf("failed to initialize embedded RoadRunner: %w", err)
	}

	runnerErrors := make(chan error, 1)
	go func() { runnerErrors <- runner.Serve() }()

	if config.SMTPAddress != "" || config.DNSAddress != "" {
		startupContext, cancelStartup := context.WithTimeout(context.Background(), config.RequestTimeout)
		protocolPlanner, plannerErr := runner.WaitProtocolPlanner(startupContext)
		cancelStartup()
		if plannerErr != nil {
			runner.Stop()
			<-runnerErrors
			server.workerPool.Shutdown()
			return fmt.Errorf("failed to initialize RoadRunner protocol planner: %w", plannerErr)
		}

		executor := server.NewGatewayExecutor()
		listeners := make([]protocolListener, 0, 2)
		if config.SMTPAddress != "" {
			smtpServer, smtpErr := newSMTPServer(config, protocolPlanner, executor)
			if smtpErr != nil {
				runner.Stop()
				<-runnerErrors
				server.workerPool.Shutdown()
				return fmt.Errorf("failed to initialize SMTP listener: %w", smtpErr)
			}
			listeners = append(listeners, smtpServer)
		}
		if config.DNSAddress != "" {
			dnsServer, dnsErr := newDNSServer(config, protocolPlanner, executor)
			if dnsErr != nil {
				runner.Stop()
				<-runnerErrors
				server.workerPool.Shutdown()
				return fmt.Errorf("failed to initialize DNS listener: %w", dnsErr)
			}
			listeners = append(listeners, dnsServer)
		}

		listenerErrors := make(chan error, len(listeners))
		for _, listener := range listeners {
			go func(listener protocolListener) { listenerErrors <- listener.ListenAndServe() }(listener)
		}

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		var runnerErr error
		runnerExited := false
		var listenerRunErr error
		select {
		case <-sigChan:
			logger.Info("Received shutdown signal")
		case runnerErr = <-runnerErrors:
			runnerExited = true
		case listenerRunErr = <-listenerErrors:
		}

		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		var listenerShutdownErr error
		for _, listener := range listeners {
			if shutdownErr := listener.Shutdown(shutdownContext); shutdownErr != nil && !expectedListenerClose(shutdownErr) {
				listenerShutdownErr = errors.Join(listenerShutdownErr, shutdownErr)
			}
		}
		cancelShutdown()

		if !runnerExited {
			runner.Stop()
			runnerErr = <-runnerErrors
		}
		server.workerPool.Shutdown()

		if listenerShutdownErr != nil {
			return fmt.Errorf("protocol listener shutdown error: %w", listenerShutdownErr)
		}
		if !expectedListenerClose(listenerRunErr) {
			return fmt.Errorf("protocol listener failed: %w", listenerRunErr)
		}
		if runnerErr != nil {
			return fmt.Errorf("embedded RoadRunner failed: %w", runnerErr)
		}
		logger.Info("RoadRunner and protocol listeners stopped gracefully")
		return nil
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigChan:
		logger.Info("Received shutdown signal")
		runner.Stop()
		err = <-runnerErrors
	case err = <-runnerErrors:
	}

	server.workerPool.Shutdown()
	if err != nil {
		return fmt.Errorf("embedded RoadRunner failed: %w", err)
	}
	return nil
}
