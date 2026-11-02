// Package lifecycle coordinates services owned by one Spinner host.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Service runs until its host stops. Shutdown must respect its context.
type Service struct {
	Name     string
	Run      func(context.Context) error
	Shutdown func(context.Context) error
}

type result struct {
	name string
	err  error
}

// Run starts all services, stops them after cancellation or the first exit,
// and waits for their runners within the shutdown deadline.
func Run(ctx context.Context, timeout time.Duration, services ...Service) error {
	if timeout <= 0 {
		return errors.New("service shutdown timeout must be positive")
	}
	for _, service := range services {
		if service.Name == "" || service.Run == nil || service.Shutdown == nil {
			return errors.New("service name, runner, and shutdown are required")
		}
	}
	if len(services) == 0 {
		return nil
	}
	results := make(chan result, len(services))
	runCtx, cancelRuns := context.WithCancel(ctx)
	defer cancelRuns()
	for _, service := range services {
		go func() { results <- result{service.Name, service.Run(runCtx)} }()
	}
	var runErr error
	remaining := len(services)
	select {
	case ended := <-results:
		remaining--
		if ctx.Err() == nil {
			if ended.err == nil {
				runErr = fmt.Errorf("%s stopped unexpectedly", ended.name)
			} else {
				runErr = fmt.Errorf("%s: %w", ended.name, ended.err)
			}
		}
	case <-ctx.Done():
	}
	cancelRuns()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	shutdowns := make(chan result, len(services))
	for _, service := range services {
		go func() { shutdowns <- result{service.Name, service.Shutdown(shutdownCtx)} }()
	}
	for count := 0; count < len(services); count++ {
		select {
		case ended := <-shutdowns:
			if ended.err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("shutdown %s: %w", ended.name, ended.err))
			}
		case <-shutdownCtx.Done():
			return errors.Join(runErr, fmt.Errorf("service shutdown: %w", shutdownCtx.Err()))
		}
	}
	for remaining > 0 {
		select {
		case <-results:
			remaining--
		case <-shutdownCtx.Done():
			return errors.Join(runErr, fmt.Errorf("waiting for services: %w", shutdownCtx.Err()))
		}
	}
	return runErr
}
