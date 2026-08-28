package middleware

import (
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	forge "github.com/twostack/go-p2p-forge"
)

// rateLimited builds the pipeline error for a rejected request. When the
// limiter can say how long the peer must wait, that travels with the error so
// a response writer can pass it on; otherwise the caller sees the plain
// sentinel it always saw.
func rateLimited(retryAfter time.Duration) error {
	if retryAfter <= 0 {
		return forge.ErrRateLimited
	}
	return &forge.RateLimitedError{RetryAfter: retryAfter}
}

// allowN charges the limiter, using its retry-reporting form when it has one.
func allowN(limiter Limiter, peerID peer.ID, n int) (bool, time.Duration) {
	if rl, ok := limiter.(RetryLimiter); ok {
		return rl.AllowNWithRetry(peerID, n)
	}
	return limiter.AllowN(peerID, n), 0
}

// allowNDual is allowN for a limiter with separate read and write buckets.
func allowNDual(limiter DualLimiter, peerID peer.ID, n int, isWrite bool) (bool, time.Duration) {
	if rl, ok := limiter.(DualRetryLimiter); ok {
		return rl.AllowNWithRetry(peerID, n, isWrite)
	}
	return limiter.AllowN(peerID, n, isWrite), 0
}

// RateLimitMiddleware returns a forge.Middleware that enforces per-peer rate
// limits, charging one unit per request. Requests exceeding the limit are
// short-circuited with forge.ErrRateLimited.
func RateLimitMiddleware(limiter Limiter) forge.Middleware {
	return func(sc *forge.StreamContext, next func()) {
		ok, retryAfter := allowN(limiter, sc.PeerID, 1)
		if !ok {
			sc.Err = rateLimited(retryAfter)
			sc.Logger.Debug("rate limited", "peer", sc.PeerID, "retryAfter", retryAfter)
			return
		}
		next()
	}
}

// RateLimitCostMiddleware is RateLimitMiddleware with a caller-supplied cost.
// The cost function inspects the raw request bytes and returns how many units
// the request should be charged, letting a limit be denominated in the work a
// request actually does rather than in requests. It must run after the frame
// has been decoded, so that sc.RawBytes is populated.
func RateLimitCostMiddleware(limiter Limiter, cost func(raw []byte) int) forge.Middleware {
	return func(sc *forge.StreamContext, next func()) {
		n := cost(sc.RawBytes)
		ok, retryAfter := allowN(limiter, sc.PeerID, n)
		if !ok {
			sc.Err = rateLimited(retryAfter)
			sc.Logger.Debug("rate limited", "peer", sc.PeerID, "cost", n, "retryAfter", retryAfter)
			return
		}
		next()
	}
}

// DualRateLimitMiddleware returns a forge.Middleware that enforces separate
// read/write rate limits. The isWrite function inspects the raw bytes to
// classify the request.
func DualRateLimitMiddleware(limiter DualLimiter, isWrite func(raw []byte) bool) forge.Middleware {
	return func(sc *forge.StreamContext, next func()) {
		write := isWrite(sc.RawBytes)
		ok, retryAfter := allowNDual(limiter, sc.PeerID, 1, write)
		if !ok {
			sc.Err = rateLimited(retryAfter)
			sc.Logger.Debug("rate limited", "peer", sc.PeerID, "isWrite", write, "retryAfter", retryAfter)
			return
		}
		next()
	}
}

// DualRateLimitCostMiddleware is DualRateLimitMiddleware with a caller-supplied
// cost. The classify function inspects the raw bytes and returns both how many
// units to charge and which bucket to charge them to.
func DualRateLimitCostMiddleware(limiter DualLimiter, classify func(raw []byte) (cost int, isWrite bool)) forge.Middleware {
	return func(sc *forge.StreamContext, next func()) {
		n, write := classify(sc.RawBytes)
		ok, retryAfter := allowNDual(limiter, sc.PeerID, n, write)
		if !ok {
			sc.Err = rateLimited(retryAfter)
			sc.Logger.Debug("rate limited", "peer", sc.PeerID, "cost", n, "isWrite", write, "retryAfter", retryAfter)
			return
		}
		next()
	}
}
