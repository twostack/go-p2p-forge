package middleware

import (
	forge "github.com/twostack/go-p2p-forge"
)

// RateLimitMiddleware returns a forge.Middleware that enforces per-peer rate
// limits, charging one unit per request. Requests exceeding the limit are
// short-circuited with forge.ErrRateLimited.
func RateLimitMiddleware(limiter Limiter) forge.Middleware {
	return func(sc *forge.StreamContext, next func()) {
		if !limiter.Allow(sc.PeerID) {
			sc.Err = forge.ErrRateLimited
			sc.Logger.Debug("rate limited", "peer", sc.PeerID)
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
		if !limiter.AllowN(sc.PeerID, n) {
			sc.Err = forge.ErrRateLimited
			sc.Logger.Debug("rate limited", "peer", sc.PeerID, "cost", n)
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
		if !limiter.Allow(sc.PeerID, write) {
			sc.Err = forge.ErrRateLimited
			sc.Logger.Debug("rate limited", "peer", sc.PeerID, "isWrite", write)
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
		if !limiter.AllowN(sc.PeerID, n, write) {
			sc.Err = forge.ErrRateLimited
			sc.Logger.Debug("rate limited", "peer", sc.PeerID, "cost", n, "isWrite", write)
			return
		}
		next()
	}
}
