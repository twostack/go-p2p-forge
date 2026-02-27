package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

func TestHealthCheck_Readyz_AllPass(t *testing.T) {
	hc := NewHealthCheck("127.0.0.1:18923", slog.Default())
	hc.AddCheck(func() error { return nil })

	if err := hc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer hc.Stop()

	time.Sleep(10 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:18923/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("expected 'ok', got %q", string(body))
	}
}

func TestHealthCheck_Readyz_Failing(t *testing.T) {
	hc := NewHealthCheck("127.0.0.1:18924", slog.Default())
	hc.AddCheck(func() error { return errors.New("not ready") })

	if err := hc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer hc.Stop()

	time.Sleep(10 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:18924/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "not ready" {
		t.Errorf("expected 'not ready', got %q", string(body))
	}
}

func TestHealthCheck_Healthz_AlwaysOK(t *testing.T) {
	hc := NewHealthCheck("127.0.0.1:18925", slog.Default())
	// Even with failing readiness checks, healthz should be ok.
	hc.AddCheck(func() error { return errors.New("broken") })

	if err := hc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer hc.Stop()

	time.Sleep(10 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:18925/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for healthz, got %d", resp.StatusCode)
	}
}
