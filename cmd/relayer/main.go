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

	bridge "github.com/webong/web-relay/cmd/bridge"
	relayconfig "github.com/webong/web-relay/cmd/internal/config"
	"github.com/webong/web-relay/cmd/internal/forwarding"
	"github.com/webong/web-relay/cmd/internal/logging"
	"github.com/webong/web-relay/cmd/internal/workers"
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
	// Standalone mode is retained for local transport testing. Embedded
	// RoadRunner is the production single-ingress path.
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
	s.relayHandle = s.NewRelayEdge(planner)
}

func (s *Server) NewRelayEdge(planner bridge.Planner) http.Handler {
	return bridge.NewEdge(
		planner,
		newRelayExecutor(s.forwarder, s.workerPool),
		s.config.MaxBodySize,
	)
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
	s.logger.Info("Starting standalone Go relay transport on port %s", s.config.Port)
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
	server := NewServer(config, logger)
	if config.RoadRunnerEnabled {
		return runEmbeddedRoadRunner(config, server, logger)
	}

	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Start() }()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sigChan:
		logger.Info("Received shutdown signal")
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server failed: %w", err)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	logger.Info("Server stopped gracefully")
	return nil
}

func runEmbeddedRoadRunner(config *relayconfig.Config, server *Server, logger *logging.Logger) error {
	runner, err := bridge.NewEmbeddedRoadRunner(
		config.RoadRunnerConfigPath,
		nil,
		newRelayExecutor(server.forwarder, server.workerPool),
		config.MaxBodySize,
		config.RoadRunnerInternalToken,
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
