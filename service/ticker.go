package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// TickerService is a convenience Service implementation for the common
// "do work on a ticker" pattern. This replaces the identical ticker-select-context
// loops found in go-ricochet's maintenanceLoop, heartbeatLoop, announceLoop,
// subscriptionLoop, and timeoutCheckLoop.
type TickerService struct {
	name     string
	interval time.Duration
	work     func(ctx context.Context) error
	logger   *slog.Logger
	cancel   context.CancelFunc

	mu      sync.Mutex
	lastRun time.Time
	lastErr error
	healthy bool
}

// NewTickerService creates a new ticker-based background service.
func NewTickerService(name string, interval time.Duration, work func(ctx context.Context) error, logger *slog.Logger) *TickerService {
	return &TickerService{
		name:     name,
		interval: interval,
		work:     work,
		logger:   logger,
		healthy:  true,
	}
}

// Name returns the service name.
func (ts *TickerService) Name() string { return ts.name }

// Start begins the ticker loop in a background goroutine.
// The work function is called immediately on start, then on each tick.
func (ts *TickerService) Start(ctx context.Context) error {
	ctx, ts.cancel = context.WithCancel(ctx)
	go ts.loop(ctx)
	return nil
}

// Stop cancels the ticker loop.
func (ts *TickerService) Stop() error {
	if ts.cancel != nil {
		ts.cancel()
	}
	return nil
}

// Healthy returns whether the last tick completed without error.
func (ts *TickerService) Healthy() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.healthy
}

// LastRun returns when the work function last executed.
func (ts *TickerService) LastRun() time.Time {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.lastRun
}

// LastError returns the error from the last tick, if any.
func (ts *TickerService) LastError() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.lastErr
}

func (ts *TickerService) loop(ctx context.Context) {
	// Run immediately on start.
	ts.runOnce(ctx)

	ticker := time.NewTicker(ts.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ts.runOnce(ctx)
		}
	}
}

func (ts *TickerService) runOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			ts.mu.Lock()
			ts.healthy = false
			ts.lastErr = fmt.Errorf("panic: %v", r)
			ts.mu.Unlock()
			ts.logger.Error("service panicked", "service", ts.name, "panic", r)
		}
	}()

	err := ts.work(ctx)

	ts.mu.Lock()
	ts.lastRun = time.Now()
	ts.lastErr = err
	ts.healthy = err == nil
	ts.mu.Unlock()

	if err != nil {
		ts.logger.Warn("service tick error", "service", ts.name, "error", err)
	}
}
