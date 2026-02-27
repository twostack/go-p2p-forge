package middleware

import (
	"testing"

	forge "github.com/twostack/go-p2p-forge"
	"github.com/twostack/go-p2p-forge/forgetest"
)

type recordingCollector struct {
	started   int
	completed int
	lastProto string
	lastPeer  string
	lastErr   error
}

func (r *recordingCollector) StreamStarted(proto, peer string) {
	r.started++
	r.lastProto = proto
	r.lastPeer = peer
}
func (r *recordingCollector) StreamCompleted(proto, peer string, durationMs float64, err error) {
	r.completed++
	r.lastErr = err
}
func (r *recordingCollector) RateLimitHit(string)        {}
func (r *recordingCollector) ActiveStreams(int64)         {}
func (r *recordingCollector) BufferPoolStats(int64, int64) {}

func TestMetricsMiddleware(t *testing.T) {
	collector := &recordingCollector{}
	mw := MetricsMiddleware(collector)

	pid := forgetest.GenerateTestPeerID()
	ms := forgetest.NewMockStream(pid, nil)
	sc := &forge.StreamContext{
		Stream: ms,
		PeerID: pid,
	}

	called := false
	mw(sc, func() { called = true })

	if !called {
		t.Error("next should have been called")
	}
	if collector.started != 1 {
		t.Errorf("expected 1 StreamStarted call, got %d", collector.started)
	}
	if collector.completed != 1 {
		t.Errorf("expected 1 StreamCompleted call, got %d", collector.completed)
	}
}

func TestNoopMetrics_Satisfies_Interface(t *testing.T) {
	var _ forge.MetricsCollector = forge.NoopMetrics{}
}
