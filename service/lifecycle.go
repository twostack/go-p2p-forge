// Package service provides lifecycle management for background services.
package service

import (
	"context"
	"fmt"
	"log/slog"
)

// Service is a long-running background component that participates in
// server startup and shutdown.
type Service interface {
	// Name returns a human-readable identifier for logging.
	Name() string

	// Start begins the service. The context is cancelled on shutdown.
	// Start must not block; it should spawn goroutines internally.
	Start(ctx context.Context) error

	// Stop gracefully shuts down the service.
	Stop() error
}

// Lifecycle manages ordered startup and shutdown of services.
// Services start in registration order and stop in reverse order.
// If any service fails to start, previously started services are stopped
// in reverse order (rollback).
type Lifecycle struct {
	services []Service
	logger   *slog.Logger
}

// NewLifecycle creates a new lifecycle manager.
func NewLifecycle(logger *slog.Logger) *Lifecycle {
	return &Lifecycle{logger: logger}
}

// Register adds a service to the lifecycle. Services start in registration order.
func (lc *Lifecycle) Register(svc Service) {
	lc.services = append(lc.services, svc)
}

// StartAll starts all services in registration order. If any service fails
// to start, previously started services are stopped in reverse order.
func (lc *Lifecycle) StartAll(ctx context.Context) error {
	for i, svc := range lc.services {
		lc.logger.Info("starting service", "service", svc.Name(), "order", i)
		if err := svc.Start(ctx); err != nil {
			lc.logger.Error("service failed to start", "service", svc.Name(), "error", err)
			// Rollback: stop already-started services in reverse
			for j := i - 1; j >= 0; j-- {
				lc.logger.Info("rolling back service", "service", lc.services[j].Name())
				if stopErr := lc.services[j].Stop(); stopErr != nil {
					lc.logger.Warn("error during rollback", "service", lc.services[j].Name(), "error", stopErr)
				}
			}
			return fmt.Errorf("start %s: %w", svc.Name(), err)
		}
	}
	return nil
}

// StopAll stops all services in reverse registration order.
// Returns the first error encountered, but always attempts to stop all services.
func (lc *Lifecycle) StopAll() error {
	var firstErr error
	for i := len(lc.services) - 1; i >= 0; i-- {
		svc := lc.services[i]
		lc.logger.Info("stopping service", "service", svc.Name())
		if err := svc.Stop(); err != nil {
			lc.logger.Warn("error stopping service", "service", svc.Name(), "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Services returns the registered services (for inspection/testing).
func (lc *Lifecycle) Services() []Service {
	return lc.services
}
