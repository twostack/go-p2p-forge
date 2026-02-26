package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
)

type recordingService struct {
	name    string
	started bool
	stopped bool
	startFn func() error
	stopFn  func() error
	mu      sync.Mutex
	order   *[]string
}

func (s *recordingService) Name() string { return s.name }

func (s *recordingService) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	if s.order != nil {
		*s.order = append(*s.order, "start:"+s.name)
	}
	if s.startFn != nil {
		return s.startFn()
	}
	return nil
}

func (s *recordingService) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.order != nil {
		*s.order = append(*s.order, "stop:"+s.name)
	}
	if s.stopFn != nil {
		return s.stopFn()
	}
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLifecycle_StartOrder(t *testing.T) {
	var order []string
	lc := NewLifecycle(testLogger())

	lc.Register(&recordingService{name: "a", order: &order})
	lc.Register(&recordingService{name: "b", order: &order})
	lc.Register(&recordingService{name: "c", order: &order})

	if err := lc.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}

	expected := []string{"start:a", "start:b", "start:c"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(order), order)
	}
	for i, e := range expected {
		if order[i] != e {
			t.Errorf("position %d: expected %q, got %q", i, e, order[i])
		}
	}
}

func TestLifecycle_StopReverseOrder(t *testing.T) {
	var order []string
	lc := NewLifecycle(testLogger())

	lc.Register(&recordingService{name: "a", order: &order})
	lc.Register(&recordingService{name: "b", order: &order})
	lc.Register(&recordingService{name: "c", order: &order})

	lc.StartAll(context.Background())
	order = nil // reset

	lc.StopAll()

	expected := []string{"stop:c", "stop:b", "stop:a"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(order), order)
	}
	for i, e := range expected {
		if order[i] != e {
			t.Errorf("position %d: expected %q, got %q", i, e, order[i])
		}
	}
}

func TestLifecycle_StartFailureRollback(t *testing.T) {
	var order []string
	lc := NewLifecycle(testLogger())

	lc.Register(&recordingService{name: "a", order: &order})
	lc.Register(&recordingService{
		name:  "b",
		order: &order,
		startFn: func() error {
			return fmt.Errorf("b failed")
		},
	})
	lc.Register(&recordingService{name: "c", order: &order})

	err := lc.StartAll(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	// "a" should have been started then rolled back. "c" never started.
	expected := []string{"start:a", "start:b", "stop:a"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(order), order)
	}
	for i, e := range expected {
		if order[i] != e {
			t.Errorf("position %d: expected %q, got %q", i, e, order[i])
		}
	}
}

func TestLifecycle_Empty(t *testing.T) {
	lc := NewLifecycle(testLogger())
	if err := lc.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lc.StopAll(); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycle_StopError(t *testing.T) {
	lc := NewLifecycle(testLogger())

	lc.Register(&recordingService{
		name:   "failing",
		stopFn: func() error { return fmt.Errorf("stop failed") },
	})
	lc.Register(&recordingService{name: "ok"})

	lc.StartAll(context.Background())
	err := lc.StopAll()
	// "ok" stops first (reverse order), then "failing" — first error returned
	if err == nil {
		t.Error("expected error from StopAll")
	}
}
