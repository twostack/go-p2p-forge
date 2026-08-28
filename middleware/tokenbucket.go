package middleware

import (
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// Limiter is a per-peer limiter with a single bucket.
//
// Both SingleBucket (sliding window) and TokenBucket (rate plus burst)
// satisfy it, so middleware can accept either.
type Limiter interface {
	// Allow reports whether one unit of work may proceed, consuming it.
	Allow(peerID peer.ID) bool
	// AllowN reports whether n units of work may proceed, consuming them.
	// Either all n are consumed or none are.
	AllowN(peerID peer.ID, n int) bool
}

// DualLimiter is a per-peer limiter with separate read and write buckets.
type DualLimiter interface {
	Allow(peerID peer.ID, isWrite bool) bool
	AllowN(peerID peer.ID, n int, isWrite bool) bool
}

// tokenState is one peer's bucket: how many tokens it holds, and when that
// count was last brought up to date. Tokens are refilled lazily on access
// rather than by a ticker, so an idle peer costs nothing until it returns.
type tokenState struct {
	tokens float64
	last   time.Time
}

// tokenShard holds a subset of peer buckets behind its own mutex.
type tokenShard struct {
	mu    sync.Mutex
	peers map[string]*tokenState
}

// TokenBucket is a per-peer token bucket rate limiter.
//
// It differs from SingleBucket in separating sustained rate from burst
// capacity. A sliding window can only express "n requests per window", which
// permits a full window's worth of traffic instantly and then nothing —
// exactly the lumpy behaviour a bursty client suffers from. A token bucket
// refills continuously at rate/window and lets a peer accumulate up to burst
// tokens while idle, so a client that has been quiet can spend a backlog in
// one go and still be held to the sustained rate over time.
//
// Locking is sharded and eviction runs in the background, as in SingleBucket.
type TokenBucket struct {
	shards       []tokenShard
	shardCount   uint64
	refillPerSec float64
	burst        float64
	unlimited    bool
	idleTTL      time.Duration
	stopCleanup  chan struct{}
	closeOnce    sync.Once
}

// NewTokenBucket creates a limiter that refills rate tokens per window and
// allows up to burst tokens to accumulate.
//
// A rate of zero or less disables limiting entirely — every request is
// allowed and no per-peer state is kept. A burst of zero or less defaults to
// rate, giving a peer one window's worth of credit to spend at once. A burst
// below rate is honoured rather than corrected: refill is continuous, so the
// peer still reaches the full rate over a window, it just cannot spend it all
// in one moment. That is a useful way to pace a client without lowering its
// throughput.
func NewTokenBucket(window time.Duration, rate, burst int) *TokenBucket {
	if window <= 0 {
		window = time.Minute
	}
	b := &TokenBucket{
		shards:      make([]tokenShard, defaultShardCount),
		shardCount:  defaultShardCount,
		stopCleanup: make(chan struct{}),
	}
	if rate <= 0 {
		b.unlimited = true
		return b
	}
	if burst <= 0 {
		burst = rate
	}
	b.refillPerSec = float64(rate) / window.Seconds()
	b.burst = float64(burst)
	b.idleTTL = 2 * window

	for i := range b.shards {
		b.shards[i].peers = make(map[string]*tokenState)
	}
	go b.cleanupLoop()
	return b
}

// Allow consumes a single token, reporting whether one was available.
func (b *TokenBucket) Allow(peerID peer.ID) bool {
	return b.AllowN(peerID, 1)
}

// AllowN consumes n tokens, reporting whether all n were available. When the
// bucket is short, nothing is consumed — a rejected request does not eat into
// the peer's allowance.
func (b *TokenBucket) AllowN(peerID peer.ID, n int) bool {
	if b.unlimited || n <= 0 {
		return true
	}
	// A request costing more than the whole bucket could never succeed, no
	// matter how long the peer waits. Reject it rather than spin forever.
	cost := float64(n)
	if cost > b.burst {
		return false
	}

	key := peerID.String()
	s := &b.shards[fnvHash(key)%b.shardCount]
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	st, ok := s.peers[key]
	if !ok {
		// A peer we have not seen (or have since evicted) starts full.
		st = &tokenState{tokens: b.burst, last: now}
		s.peers[key] = st
	} else {
		st.tokens += now.Sub(st.last).Seconds() * b.refillPerSec
		if st.tokens > b.burst {
			st.tokens = b.burst
		}
		st.last = now
	}

	if st.tokens < cost {
		return false
	}
	st.tokens -= cost
	return true
}

// TrackedPeers reports how many peers currently hold bucket state. Intended
// for stats and diagnostics.
func (b *TokenBucket) TrackedPeers() int {
	if b.unlimited {
		return 0
	}
	total := 0
	for i := range b.shards {
		s := &b.shards[i]
		s.mu.Lock()
		total += len(s.peers)
		s.mu.Unlock()
	}
	return total
}

// Close stops the background eviction goroutine. Safe to call more than once.
func (b *TokenBucket) Close() {
	b.closeOnce.Do(func() {
		close(b.stopCleanup)
	})
}

func (b *TokenBucket) cleanupLoop() {
	interval := b.idleTTL
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.evictIdle()
		case <-b.stopCleanup:
			return
		}
	}
}

// evictIdle drops peers whose buckets have refilled to capacity and have gone
// untouched for idleTTL. Such an entry is indistinguishable from a peer we
// have never seen, so forgetting it changes no decision.
func (b *TokenBucket) evictIdle() {
	now := time.Now()
	for i := range b.shards {
		s := &b.shards[i]
		s.mu.Lock()
		for key, st := range s.peers {
			if now.Sub(st.last) < b.idleTTL {
				continue
			}
			if st.tokens+now.Sub(st.last).Seconds()*b.refillPerSec >= b.burst {
				delete(s.peers, key)
			}
		}
		s.mu.Unlock()
	}
}

// DualTokenBucket provides separate read and write token buckets, matching the
// pattern DualBucket establishes for sliding windows.
type DualTokenBucket struct {
	read  *TokenBucket
	write *TokenBucket
}

// NewDualTokenBucket creates a limiter with independent read and write buckets.
func NewDualTokenBucket(window time.Duration, readRate, readBurst, writeRate, writeBurst int) *DualTokenBucket {
	return &DualTokenBucket{
		read:  NewTokenBucket(window, readRate, readBurst),
		write: NewTokenBucket(window, writeRate, writeBurst),
	}
}

// Allow consumes one token from the bucket selected by isWrite.
func (d *DualTokenBucket) Allow(peerID peer.ID, isWrite bool) bool {
	return d.AllowN(peerID, 1, isWrite)
}

// AllowN consumes n tokens from the bucket selected by isWrite.
func (d *DualTokenBucket) AllowN(peerID peer.ID, n int, isWrite bool) bool {
	if isWrite {
		return d.write.AllowN(peerID, n)
	}
	return d.read.AllowN(peerID, n)
}

// Close stops eviction goroutines for both buckets.
func (d *DualTokenBucket) Close() {
	d.read.Close()
	d.write.Close()
}
