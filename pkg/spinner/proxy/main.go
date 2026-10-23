package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/webong/gateway/driver/automation"
	automationexecution "github.com/webong/gateway/pkg/spinner/automation/execution"
	"github.com/webong/gateway/pkg/spinner/automation/schedules"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryadmin"
	"github.com/webong/gateway/pkg/spinner/bridge/deliverymembership"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
	dnsgateway "github.com/webong/gateway/pkg/spinner/bridge/dns"
	imapgateway "github.com/webong/gateway/pkg/spinner/bridge/imap"
	pop3gateway "github.com/webong/gateway/pkg/spinner/bridge/pop3"
	smtpgateway "github.com/webong/gateway/pkg/spinner/bridge/smtp"
	gatewayws "github.com/webong/gateway/pkg/spinner/bridge/websocket"
	gatewaystream "github.com/webong/gateway/pkg/spinner/bridge/webstream"
	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
	relayconfig "github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/forwarding"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
	"github.com/webong/gateway/pkg/spinner/internal/workers"
	"github.com/webong/gateway/pkg/spinner/provision"
	"github.com/webong/gateway/pkg/spinner/transport"
)

type extensionRouteHandler interface {
	ServeIfMatched(http.ResponseWriter, *http.Request) bool
}

type protocolListener interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

type Server struct {
	config              *relayconfig.Config
	logger              *logging.Logger
	forwarder           *forwarding.Forwarder
	workerPool          *workers.WorkerPool
	executorOptions     []bridge.RelayExecutorOption
	automation          automation.Executor
	automationRuntime   automation.Executor
	automationPool      *automationexecution.Executor
	scheduleRegistrar   automation.ScheduleRegistrar
	deliveryQueue       deliveryqueue.Backend
	deliveryAdminServer *http.Server
	relayHandle         http.Handler
	readinessCheck      func(context.Context) error
	websocketHandler    http.Handler
	webstreamHandler    *gatewaystream.Handler
	extensionHandler    extensionRouteHandler
	httpServer          *http.Server
}

func NewServer(config *relayconfig.Config, logger *logging.Logger) (*Server, error) {
	forwarder := forwarding.NewForwarder(config, logger)
	workerPool := workers.NewWorkerPool(config.MaxWorkers, config.MaxQueueSize, forwarder, logger)
	runtime := automationextensions.Runtime()
	automationPool, err := automationexecution.New(runtime, automationexecution.Options{
		Mode: automationexecution.Mode(config.AutomationExecutor), Workers: config.AutomationWorkers,
		QueueSize: config.AutomationQueueSize, Timeout: config.AutomationTimeout,
	})
	if err != nil {
		workerPool.Shutdown()
		forwarder.CloseIdleConnections()
		return nil, fmt.Errorf("initialize automation executor: %w", err)
	}

	// Automation guests are composed by the host binary before the server is
	// built: Spinner's transport layer only reads automationextensions.Runtime().

	server := &Server{
		config: config, logger: logger, forwarder: forwarder, workerPool: workerPool,
		automation: automationPool, automationRuntime: runtime, automationPool: automationPool,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/ready", server.handleReady)
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

	return server, nil
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

func (s *Server) SetReadinessCheck(check func(context.Context) error) {
	s.readinessCheck = check
}

func (s *Server) SetWebSocketHandler(handler http.Handler) {
	s.websocketHandler = handler
}

func (s *Server) SetWebstreamHandler(handler *gatewaystream.Handler) {
	s.webstreamHandler = handler
}

func (s *Server) SetExtensionHandler(handler extensionRouteHandler) {
	s.extensionHandler = handler
}

func (s *Server) SetScheduleRegistrar(registrar automation.ScheduleRegistrar) {
	s.scheduleRegistrar = registrar
}

func (s *Server) NewRelayEdge(planner bridge.Planner, passThrough ...http.Handler) http.Handler {
	edge := bridge.NewEdge(
		planner,
		s.NewGatewayExecutor(),
		s.config.MaxBodySize,
	).SetAutomationExecutor(s.automation)
	if s.scheduleRegistrar != nil {
		edge.SetScheduleRegistrar(s.scheduleRegistrar)
	}
	if len(passThrough) > 0 {
		edge.SetPassThrough(passThrough[0])
	}
	return edge
}

func (s *Server) NewGatewayExecutor() *bridge.RelayExecutor {
	return bridge.NewRelayExecutor(s.forwarder, s.workerPool, s.executorOptions...)
}

func (s *Server) ConfigureDeliveryQueue() error {
	if s == nil || s.config == nil {
		return nil
	}
	queue, err := transport.NewDeliveryQueue(s.config, s.forwarder, s.logger)
	if err != nil {
		return err
	}
	if queue == nil && s.config.DeliveryAdminAddress == "" {
		return nil
	}
	if s.config.DeliveryAdminAddress != "" {
		var options []deliveryadmin.HandlerOption
		options = append(options, deliveryadmin.WithAutomationExecutor(s.automation))
		if history := automationextensions.RunHistory(); history != nil {
			options = append(options, deliveryadmin.WithShellRunHistory(history))
		}
		if s.config.SMTPRelayAddress != "" || s.config.MailboxSMTPResolverURL != "" {
			adapter, adapterErr := transport.NewSMTPAdapter(s.config)
			if adapterErr != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = queue.Close(ctx)
				cancel()
				return adapterErr
			}
			scanner, _, protectionErr := transport.NewEmailProtection(s.config)
			if protectionErr != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = queue.Close(ctx)
				cancel()
				return protectionErr
			}
			options = append(options, deliveryadmin.WithEmailSubmission(queue, adapter, scanner, s.config.SMTPMaxMessageSize, []byte(s.config.InternalToken), s.config.EmailDSNDomain != ""))
		}
		handler, err := deliveryadmin.NewHandler(queue, s.config.InternalToken, options...)
		if err != nil {
			if queue != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = queue.Close(ctx)
				cancel()
			}
			return err
		}
		s.deliveryAdminServer = &http.Server{
			Addr: s.config.DeliveryAdminAddress, Handler: handler,
			ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		}
	}
	if queue == nil {
		return nil
	}
	s.deliveryQueue = queue
	s.executorOptions = append(s.executorOptions, bridge.WithGatewayDeliveryQueue(queue))
	if s.config.SMTPRelayAddress != "" {
		s.logger.Info("Configured outbound SMTP relay at %s", s.config.SMTPRelayAddress)
	}
	if s.config.DeliveryDatabaseURL != "" {
		s.logger.Info("Using shared PostgreSQL delivery queue")
	} else {
		s.logger.Info("Using durable delivery spool at %s", s.config.DeliverySpoolPath)
	}
	return nil
}

func (s *Server) handleRelay(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/email/submissions" {
		if s.relayHandle == nil {
			http.Error(w, "PHP relay planner is not configured", http.StatusServiceUnavailable)
			return
		}
		s.relayHandle.ServeHTTP(w, r)
		return
	}
	if s.extensionHandler != nil && s.extensionHandler.ServeIfMatched(w, r) {
		return
	}
	if s.websocketHandler != nil && gatewayws.IsUpgrade(r) {
		s.websocketHandler.ServeHTTP(w, r)
		return
	}
	if s.webstreamHandler != nil && gatewaystream.IsRequest(r) {
		s.webstreamHandler.ServeHTTP(w, r)
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

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.readinessCheck != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.readinessCheck(ctx); err != nil {
			http.Error(w, "Planner unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
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
	if s.webstreamHandler != nil {
		streamMetrics := s.webstreamHandler.Metrics()
		metrics["webstreams_active"] = streamMetrics.Active
		metrics["webstreams_opened_total"] = streamMetrics.Opened
		metrics["webstreams_failed_total"] = streamMetrics.Failed
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
	if s.automationPool != nil {
		s.automationPool.Close()
	}
	s.workerPool.Shutdown()
	return queueErr
}

// Main runs the standalone Spinner HTTP service.
func Main() {
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
	if config.Runtime == "diagnostic" && (config.SMTPAddress != "" || config.DNSAddress != "" || config.POP3Address != "" || config.IMAPAddress != "") {
		return fmt.Errorf("protocol listeners require GATEWAY_RUNTIME=http")
	}
	server, err := NewServer(config, logger)
	if err != nil {
		return err
	}
	defer server.automationPool.Close()
	if err := server.ConfigureDeliveryQueue(); err != nil {
		server.workerPool.Shutdown()
		return fmt.Errorf("failed to configure delivery queue: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if err := server.closeExecutionResources(ctx); err != nil {
			logger.Error("Failed to close delivery resources: %v", err)
		}
	}()
	switch config.Runtime {
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
	serverErrors := make(chan error, 2+len(listeners))
	go func() { serverErrors <- server.Start() }()
	if server.deliveryAdminServer != nil {
		go func() {
			serverErrors <- deliverymembership.ListenAndServe(server.deliveryAdminServer, config.DeliveryDatabaseURL, config.DeliveryAdminAdvertiseURL, config.InternalToken)
		}()
	}
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
	if server.deliveryAdminServer != nil {
		if err := server.deliveryAdminServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("delivery admin shutdown error: %w", err)
		}
	}

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
	return err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, smtpgateway.ErrServerClosed) || errors.Is(err, dnsgateway.ErrServerClosed) || errors.Is(err, pop3gateway.ErrServerClosed) || errors.Is(err, imapgateway.ErrServerClosed)
}

func newSMTPServer(config *relayconfig.Config, planner bridge.ProtocolPlanner, executor bridge.GatewayExecutor) (*smtpgateway.Server, error) {
	tlsConfig, err := smtpgateway.LoadTLSConfig(config.SMTPTLSCertFile, config.SMTPTLSKeyFile)
	if err != nil {
		return nil, err
	}
	submitter, err := transport.NewSMTPAdapter(config)
	if err != nil {
		return nil, err
	}
	var submitAdapter bridge.GatewayDeliveryAdapter
	var signingChecker smtpgateway.SigningDomainChecker
	if submitter != nil {
		submitAdapter = submitter
		signingChecker = submitter
	}
	scanner, quarantine, err := transport.NewEmailProtection(config)
	if err != nil {
		return nil, err
	}
	domainResolver, err := transport.NewEmailDomainResolver(config)
	if err != nil {
		return nil, err
	}

	return smtpgateway.NewServer(smtpgateway.Config{
		Address:                 config.SMTPAddress,
		Hostname:                config.SMTPHostname,
		MaxMessageSize:          config.SMTPMaxMessageSize,
		MaxRecipients:           config.SMTPMaxRecipients,
		MaxConnections:          config.SMTPMaxConnections,
		MessagesPerMinute:       config.SMTPMessagesPerMinute,
		MessageBurst:            config.SMTPMessageBurst,
		ReadTimeout:             config.SMTPReadTimeout,
		WriteTimeout:            config.SMTPWriteTimeout,
		PlannerTimeout:          config.SMTPPlannerTimeout,
		SubmitTimeout:           config.SMTPRelayTimeout,
		Submitter:               submitAdapter,
		SigningDomainChecker:    signingChecker,
		Scanner:                 scanner,
		ScanTimeout:             config.EmailClamdTimeout,
		Quarantine:              quarantine,
		QuarantineRetention:     config.EmailQuarantineRetention,
		DomainExistenceResolver: domainResolver,
		DomainExistenceTimeout:  config.EmailDomainLookupTimeout,
		LoopKey:                 []byte(config.InternalToken),
		TLSConfig:               tlsConfig,
		ImplicitTLS:             config.SMTPImplicitTLS,
		AuthEnabled:             config.SMTPAuthEnabled,
		RecipientPreflight:      config.SMTPRecipientPreflight,
	}, planner, executor)
}

func newDNSServer(config *relayconfig.Config, planner bridge.ProtocolPlanner, executor *bridge.RelayExecutor) (*dnsgateway.Server, error) {
	return dnsgateway.NewServer(dnsgateway.Config{
		Address:            config.DNSAddress,
		Zone:               config.DNSZone,
		Zones:              transport.DNSZones(config),
		Nameservers:        config.DNSNameservers,
		SOAEmail:           config.DNSSOAEmail,
		TTL:                config.DNSTTL,
		PlannerTimeout:     config.DNSPlannerTimeout,
		ReadTimeout:        config.DNSReadTimeout,
		WriteTimeout:       config.DNSWriteTimeout,
		MaxResponseRecords: config.DNSMaxResponseRecords,
		RateLimit:          config.DNSRateLimit,
		RateBurst:          config.DNSRateBurst,
		AllowEmptyZones:    config.Runtime == "http",
	}, planner, executor)
}

func runHTTPBackend(config *relayconfig.Config, server *Server, logger *logging.Logger) error {
	client := newPlannerClient(config)
	if err := automationextensions.ConfigureShellSSHHTTP(config.PlannerURL, config.InternalToken, client, config.PlannerSocket != ""); err != nil {
		return fmt.Errorf("failed to initialize private Shell credential resolver: %w", err)
	}
	control, controlErr := schedules.NewHTTPControlPlane(
		config.PlannerURL,
		config.InternalToken,
		client,
		automation.Languages(server.automationRuntime)...,
	)
	if controlErr != nil {
		return fmt.Errorf("failed to initialize schedule control plane: %w", controlErr)
	}
	server.SetScheduleRegistrar(control)
	if config.SchedulesEnabled {
		scheduleCtx, cancelSchedules := context.WithCancel(context.Background())
		defer cancelSchedules()
		executor := server.NewGatewayExecutor()
		if err := (schedules.Runtime{
			Control:   control,
			Executor:  server.automationRuntime,
			Registrar: control,
			Interval:  config.SchedulePollInterval,
			LogError:  logger.Error,
			Enqueue: func(runID string, delivery automation.Delivery) bool {
				return transport.EnqueueScheduledDelivery(executor, runID, delivery)
			},
		}).Start(scheduleCtx); err != nil {
			return err
		}
		logger.Info("Schedule polling enabled")
	}
	runtime, err := bridge.NewHTTPRuntime(
		config.PlannerURL,
		config.InternalToken,
		config.MaxBodySize,
		client,
	)
	if err != nil {
		server.workerPool.Shutdown()
		return fmt.Errorf("failed to initialize HTTP Planner runtime: %w", err)
	}
	defer runtime.Close()
	readyURL, err := url.JoinPath(config.PlannerURL, "_internal/gateway/ready")
	if err != nil {
		server.workerPool.Shutdown()
		return fmt.Errorf("invalid Planner readiness URL: %w", err)
	}
	server.SetReadinessCheck(newPlannerReadinessCheck(readyURL, config.InternalToken, client))

	edge := server.NewRelayEdge(runtime.Planner(), runtime.PassThrough())
	server.SetRelayHandler(runtime.Handler(edge))
	if config.PlannerSocket != "" {
		logger.Info("Planner connection: external HTTP through Unix socket %s", config.PlannerSocket)
	} else {
		logger.Info("Planner connection: external HTTP at %s", config.PlannerURL)
	}

	protocolPlanner, err := bridge.NewHTTPProtocolPlanner(
		config.PlannerURL,
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
		gatewayws.Config{MaxMessageSize: config.MaxBodySize, PlannerTimeout: config.RequestTimeout, MaxConnections: config.WebSocketMaxConnections, MaxDuration: config.WebSocketMaxDuration},
	))
	server.SetWebstreamHandler(gatewaystream.NewHandler(
		protocolPlanner,
		server.NewGatewayExecutor(),
		gatewaystream.Config{MaxBodySize: config.MaxBodySize, PlannerTimeout: config.RequestTimeout, HeartbeatInterval: config.WebstreamHeartbeat, ReconnectDelay: config.WebstreamRetry, MaxConnections: config.WebstreamMaxConnections, MaxDuration: config.WebstreamMaxDuration},
	))
	if config.ProvisioningRouteProxy {
		managedRouter, routeErr := provision.NewRemoteRouter(config.PlannerURL, config.InternalToken, client, config.ProvisioningRouteCIDRs)
		if routeErr != nil {
			server.workerPool.Shutdown()
			return fmt.Errorf("initialize managed route proxy: %w", routeErr)
		}
		server.SetExtensionHandler(managedRouter)
		logger.Info("Managed route proxy enabled through private Planner")
	}

	var provisioningRuntime *provision.Runtime
	if config.Provisioning.Enabled {
		paths, pathErr := provision.PluginPaths()
		if pathErr != nil {
			server.workerPool.Shutdown()
			return pathErr
		}
		plugins, workloads, pluginErr := provision.LoadPlugins(paths)
		if pluginErr != nil {
			server.workerPool.Shutdown()
			return pluginErr
		}
		defer plugins.Close()
		provisioningRuntime, err = provision.Start(
			config.Provisioning.Config,
			config.PlannerURL,
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
	listeners := make([]protocolListener, 0, 4)
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
		zoneSource, sourceErr := dnsgateway.NewHTTPZoneSource(config.PlannerURL, config.InternalToken, client)
		if sourceErr != nil {
			server.workerPool.Shutdown()
			return fmt.Errorf("failed to initialize DNS zone source: %w", sourceErr)
		}
		reloadContext, cancelReload := context.WithCancel(context.Background())
		defer cancelReload()
		reloader := dnsgateway.ZoneReloader{
			Server: dnsServer, Source: zoneSource, Interval: config.DNSZoneRefreshInterval, OnError: logger.Error,
		}
		initialContext, cancelInitial := context.WithTimeout(reloadContext, config.RequestTimeout)
		if refreshErr := reloader.Refresh(initialContext); refreshErr != nil {
			logger.Error("Initial authoritative DNS zone refresh failed; retaining the last valid snapshot: %v", refreshErr)
		}
		cancelInitial()
		reloader.Start(reloadContext)
		logger.Info("Using authoritative DNS listener on %s with Planner-managed zones (refresh %s)", config.DNSAddress, config.DNSZoneRefreshInterval)
		listeners = append(listeners, dnsServer)
	}
	if config.POP3Address != "" {
		pop3Server, pop3Err := transport.NewPOP3Server(config, protocolPlanner)
		if pop3Err != nil {
			server.workerPool.Shutdown()
			return fmt.Errorf("failed to initialize POP3 listener: %w", pop3Err)
		}
		logger.Info("Using TLS POP3 listener on %s", config.POP3Address)
		listeners = append(listeners, pop3Server)
	}
	if config.IMAPAddress != "" {
		imapServer, imapErr := transport.NewIMAPServer(config, protocolPlanner)
		if imapErr != nil {
			server.workerPool.Shutdown()
			return fmt.Errorf("failed to initialize IMAP listener: %w", imapErr)
		}
		logger.Info("Using TLS IMAP listener on %s", config.IMAPAddress)
		listeners = append(listeners, imapServer)
	}

	return runServices(config, server, logger, listeners...)
}
