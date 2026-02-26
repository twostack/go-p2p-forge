package service

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestTickerService_RunsImmediately(t *testing.T) {
	var count atomic.Int32
	ts := NewTickerService("test", time.Hour, func(ctx context.Context) error {
		count.Add(1)
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ts.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	if count.Load() < 1 {
		t.Error("work function should run immediately on start")
	}

	ts.Stop()
}

func TestTickerService_RunsPeriodically(t *testing.T) {
	var count atomic.Int32
	ts := NewTickerService("test", 30*time.Millisecond, func(ctx context.Context) error {
		count.Add(1)
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ts.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	ts.Stop()

	// Should have run at least twice (immediate + at least one tick)
	if count.Load() < 2 {
		t.Errorf("expected at least 2 runs, got %d", count.Load())
	}
}

func TestTickerService_StopsOnCancel(t *testing.T) {
	var count atomic.Int32
	ts := NewTickerService("test", 10*time.Millisecond, func(ctx context.Context) error {
		count.Add(1)
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	ts.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond)

	snapshot := count.Load()
	time.Sleep(50 * time.Millisecond)

	if count.Load() > snapshot+1 {
		t.Error("service should have stopped after context cancel")
	}
}

func TestTickerService_HealthTracking(t *testing.T) {
	ts := NewTickerService("test", time.Hour, func(ctx context.Context) error {
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ts.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	if !ts.Healthy() {
		t.Error("service should be healthy after successful tick")
	}
	if ts.LastRun().IsZero() {
		t.Error("LastRun should be set")
	}
	if ts.LastError() != nil {
		t.Errorf("LastError should be nil, got %v", ts.LastError())
	}

	ts.Stop()
}

func TestTickerService_PanicRecovery(t *testing.T) {
	var count atomic.Int32
	ts := NewTickerService("panicky", 30*time.Millisecond, func(ctx context.Context) error {
		if count.Add(1) == 1 {
			panic("test panic")
		}
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ts.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	ts.Stop()

	// Should survive the panic and continue ticking
	if count.Load() < 2 {
		t.Errorf("expected at least 2 runs despite panic, got %d", count.Load())
	}
}

func TestTickerService_Name(t *testing.T) {
	ts := NewTickerService("my-service", time.Hour, func(ctx context.Context) error {
		return nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if ts.Name() != "my-service" {
		t.Errorf("expected name 'my-service', got %q", ts.Name())
	}
}
