// Package middleware provides reusable middleware components for libp2p stream pipelines.
package middleware

import (
	"hash/fnv"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const defaultShardCount = 64

// shard holds a subset of peer rate-limit histories behind its own mutex,
// reducing lock contention compared to a single global mutex.
type shard struct {
	mu      sync.Mutex
	history map[string][]time.Time
}

// SingleBucket is a per-peer sliding window rate limiter with a single request bucket.
// It uses sharded locking to reduce contention under high concurrency, and runs a
// background goroutine to evict stale peer entries.
type SingleBucket struct {
	shards      []shard
	shardCount  uint64
	window      time.Duration
	maxRequests int
	stopCleanup chan struct{}
}

// NewSingleBucket creates a rate limiter that allows maxRequests within each sliding window.
func NewSingleBucket(window time.Duration, maxRequests int) *SingleBucket {
	b := &SingleBucket{
		shards:      make([]shard, defaultShardCount),
		shardCount:  defaultShardCount,
		window:      window,
		maxRequests: maxRequests,
		stopCleanup: make(chan struct{}),
	}
	for i := range b.shards {
		b.shards[i].history = make(map[string][]time.Time)
	}
	go b.cleanupLoop()
	return b
}

// Allow returns true if the peer is within rate limits, and records the request.
func (b *SingleBucket) Allow(peerID peer.ID) bool {
	key := peerID.String()
	s := &b.shards[fnvHash(key)%b.shardCount]
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-b.window)

	history := s.history[key]
	filtered := history[:0]
	for _, ts := range history {
		if ts.After(cutoff) {
			filtered = append(filtered, ts)
		}
	}

	if len(filtered) >= b.maxRequests {
		s.history[key] = filtered
		return false
	}

	s.history[key] = append(filtered, now)
	return true
}

// Close stops the background cleanup goroutine. Should be called when the
// rate limiter is no longer needed.
func (b *SingleBucket) Close() {
	close(b.stopCleanup)
}

func (b *SingleBucket) cleanupLoop() {
	interval := b.window * 2
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.cleanup()
		case <-b.stopCleanup:
			return
		}
	}
}

func (b *SingleBucket) cleanup() {
	cutoff := time.Now().Add(-b.window)
	for i := range b.shards {
		s := &b.shards[i]
		s.mu.Lock()
		for key, times := range s.history {
			filtered := times[:0]
			for _, ts := range times {
				if ts.After(cutoff) {
					filtered = append(filtered, ts)
				}
			}
			if len(filtered) == 0 {
				delete(s.history, key)
			} else {
				s.history[key] = filtered
			}
		}
		s.mu.Unlock()
	}
}

func fnvHash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
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

// Close stops cleanup goroutines for both buckets.
func (d *DualBucket) Close() {
	d.read.Close()
	d.write.Close()
}
