package forge

// MetricsCollector is the interface framework users implement to capture
// operational metrics using their preferred backend (Prometheus, StatsD, etc.).
type MetricsCollector interface {
	// StreamStarted is called when a stream begins processing.
	StreamStarted(protocol string, peerID string)

	// StreamCompleted is called when a stream finishes processing.
	StreamCompleted(protocol string, peerID string, durationMs float64, err error)

	// RateLimitHit is called when a request is rejected by the rate limiter.
	RateLimitHit(peerID string)

	// ActiveStreams reports the current number of in-flight streams.
	ActiveStreams(count int64)

	// BufferPoolStats reports buffer pool hit/miss counters.
	BufferPoolStats(hits, misses int64)
}

// NoopMetrics is the default no-op MetricsCollector that discards all metrics.
type NoopMetrics struct{}

func (NoopMetrics) StreamStarted(string, string)                   {}
func (NoopMetrics) StreamCompleted(string, string, float64, error) {}
func (NoopMetrics) RateLimitHit(string)                            {}
func (NoopMetrics) ActiveStreams(int64)                             {}
func (NoopMetrics) BufferPoolStats(int64, int64)                   {}
