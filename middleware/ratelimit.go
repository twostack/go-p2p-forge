// Package middleware provides reusable middleware components for libp2p stream pipelines.
package middleware

import (
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// SingleBucket is a per-peer sliding window rate limiter with a single request bucket.
// Extracted from the identical checkRateLimit methods duplicated across go-ricochet handlers.
type SingleBucket struct {
	mu          sync.Mutex
	history     map[string][]time.Time
	window      time.Duration
	maxRequests int
}

// NewSingleBucket creates a rate limiter that allows maxRequests within each sliding window.
func NewSingleBucket(window time.Duration, maxRequests int) *SingleBucket {
	return &SingleBucket{
		history:     make(map[string][]time.Time),
		window:      window,
		maxRequests: maxRequests,
	}
}

// Allow returns true if the peer is within rate limits, and records the request.
func (b *SingleBucket) Allow(peerID peer.ID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	key := peerID.String()
	cutoff := now.Add(-b.window)

	history := b.history[key]
	filtered := history[:0]
	for _, ts := range history {
		if ts.After(cutoff) {
			filtered = append(filtered, ts)
		}
	}

	if len(filtered) >= b.maxRequests {
		b.history[key] = filtered
		return false
	}

	b.history[key] = append(filtered, now)
	return true
}

// DualBucket provides separate read/write rate limiting buckets.
// This supports the pattern used in go-ricochet's SDA, SFA, and SCA handlers
// where read and write operations have different rate limits.
type DualBucket struct {
	read  *SingleBucket
	write *SingleBucket
}

// NewDualBucket creates a rate limiter with separate read and write limits.
func NewDualBucket(window time.Duration, maxRead, maxWrite int) *DualBucket {
	return &DualBucket{
		read:  NewSingleBucket(window, maxRead),
		write: NewSingleBucket(window, maxWrite),
	}
}

// Allow checks the appropriate bucket based on the isWrite flag.
func (d *DualBucket) Allow(peerID peer.ID, isWrite bool) bool {
	if isWrite {
		return d.write.Allow(peerID)
	}
	return d.read.Allow(peerID)
}
