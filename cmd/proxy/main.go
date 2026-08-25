package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	bridge "github.com/webong/gateway/cmd/bridge"
	roadrunner "github.com/webong/gateway/cmd/bridge/roadrunner"
	smtpgateway "github.com/webong/gateway/cmd/bridge/smtp"
	relayconfig "github.com/webong/gateway/cmd/internal/config"
	"github.com/webong/gateway/cmd/internal/forwarding"
	"github.com/webong/gateway/cmd/internal/logging"
	"github.com/webong/gateway/cmd/internal/workers"
)

type Server struct {
	config      *relayconfig.Config
	logger      *logging.Logger
	forwarder   *forwarding.Forwarder
	workerPool  *workers.WorkerPool
	relayHandle http.Handler
	httpServer  *http.Server
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

func (s *Server) NewRelayEdge(planner bridge.Planner, passThrough ...http.Handler) http.Handler {
	edge := bridge.NewEdge(
		planner,
		bridge.NewRelayExecutor(s.forwarder, s.workerPool),
		s.config.MaxBodySize,
	)
	if len(passThrough) > 0 {
		edge.SetPassThrough(passThrough[0])
	}
	return edge
}

func (s *Server) handleRelay(w http.ResponseWriter, r *http.Request) {
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

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return err
	}

	s.workerPool.Shutdown()
	return nil
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
	if config.SMTPAddress != "" && config.Runtime != "http" {
		return fmt.Errorf("GATEWAY_SMTP_ADDR requires GATEWAY_RUNTIME=http; embedded Caddy uses gateway_smtp instead")
	}
	server := NewServer(config, logger)
	switch config.Runtime {
	case "roadrunner":
		return runEmbeddedRoadRunner(config, server, logger)
	case "http":
		return runHTTPBackend(config, server, logger)
	case "standalone":
		return runStandalone(config, server, logger)
	default:
		return fmt.Errorf("unsupported runtime %q", config.Runtime)
	}
}

func runStandalone(config *relayconfig.Config, server *Server, logger *logging.Logger) error {
	return runServices(config, server, logger, nil)
}

func runServices(config *relayconfig.Config, server *Server, logger *logging.Logger, smtpServer *smtpgateway.Server) error {
	serverErrors := make(chan error, 2)
	go func() { serverErrors <- server.Start() }()
	if smtpServer != nil {
		go func() { serverErrors <- smtpServer.ListenAndServe() }()
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	var listenerErr error
	select {
	case <-sigChan:
		logger.Info("Received shutdown signal")
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed && err != smtpgateway.ErrServerClosed {
			listenerErr = err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}
	if smtpServer != nil {
		if err := smtpServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("SMTP shutdown error: %w", err)
		}
	}
	if listenerErr != nil {
		return fmt.Errorf("gateway listener failed: %w", listenerErr)
	}

	logger.Info("Server stopped gracefully")
	return nil
}

func runHTTPBackend(config *relayconfig.Config, server *Server, logger *logging.Logger) error {
	client := &http.Client{Timeout: config.RequestTimeout}
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
	logger.Info("Using HTTP Laravel backend at %s", config.LaravelBackendURL)

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

	var smtpServer *smtpgateway.Server
	if config.SMTPAddress != "" {
		smtpServer, err = smtpgateway.NewServer(smtpgateway.Config{
			Address:        config.SMTPAddress,
			Hostname:       config.SMTPHostname,
			MaxMessageSize: config.SMTPMaxMessageSize,
			MaxRecipients:  config.SMTPMaxRecipients,
			ReadTimeout:    config.SMTPReadTimeout,
			WriteTimeout:   config.SMTPWriteTimeout,
			PlannerTimeout: config.SMTPPlannerTimeout,
		}, protocolPlanner, bridge.NewRelayExecutor(server.forwarder, server.workerPool))
		if err != nil {
			server.workerPool.Shutdown()
			return fmt.Errorf("failed to initialize SMTP listener: %w", err)
		}
		logger.Info("Using SMTP listener on %s", config.SMTPAddress)
	}

	return runServices(config, server, logger, smtpServer)
}

func runEmbeddedRoadRunner(config *relayconfig.Config, server *Server, logger *logging.Logger) error {
	runner, err := roadrunner.NewEmbeddedRoadRunner(
		config.RoadRunnerConfigPath,
		nil,
		bridge.NewRelayExecutor(server.forwarder, server.workerPool),
		config.MaxBodySize,
		config.InternalToken,
	)
	if err != nil {
		server.workerPool.Shutdown()
		return fmt.Errorf("failed to initialize embedded RoadRunner: %w", err)
	}

	runnerErrors := make(chan error, 1)
	go func() { runnerErrors <- runner.Serve() }()

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
