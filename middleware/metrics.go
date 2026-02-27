package middleware

import (
	"time"

	forge "github.com/twostack/go-p2p-forge"
)

// MetricsMiddleware returns a forge.Middleware that records stream timing and
// error metrics via the provided MetricsCollector.
func MetricsMiddleware(collector forge.MetricsCollector) forge.Middleware {
	return func(sc *forge.StreamContext, next func()) {
		proto := string(sc.Stream.Protocol())
		pid := sc.PeerID.String()
		collector.StreamStarted(proto, pid)
		start := time.Now()

		next()

		durationMs := float64(time.Since(start).Microseconds()) / 1000.0
		collector.StreamCompleted(proto, pid, durationMs, sc.Err)
	}
}
