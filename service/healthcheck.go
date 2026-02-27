package service

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// HealthCheck is an opt-in service that exposes HTTP /healthz and /readyz
// endpoints for orchestrator integration (Kubernetes, Docker, etc.).
//
// /healthz always returns 200 (liveness).
// /readyz returns 200 only if all registered checks pass (readiness).
type HealthCheck struct {
	addr   string
	server *http.Server
	checks []func() error
	logger *slog.Logger
}

// NewHealthCheck creates a health check service listening on the given address (e.g. ":8080").
func NewHealthCheck(addr string, logger *slog.Logger) *HealthCheck {
	return &HealthCheck{
		addr:   addr,
		logger: logger,
	}
}

// AddCheck registers a readiness check function. If any check returns an error,
// /readyz will respond with 503 Service Unavailable.
func (h *HealthCheck) AddCheck(fn func() error) {
	h.checks = append(h.checks, fn)
}

func (h *HealthCheck) Name() string { return "healthcheck" }

func (h *HealthCheck) Start(_ context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		for _, check := range h.checks {
			if err := check(); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(err.Error()))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	h.server = &http.Server{
		Addr:    h.addr,
		Handler: mux,
	}

	ln, err := net.Listen("tcp", h.addr)
	if err != nil {
		return err
	}

	go func() {
		if err := h.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			h.logger.Error("health check server error", "error", err)
		}
	}()

	h.logger.Info("health check service started", "addr", h.addr)
	return nil
}

func (h *HealthCheck) Stop() error {
	if h.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.server.Shutdown(ctx)
}
