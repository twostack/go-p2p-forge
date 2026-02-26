package middleware

import (
	forge "github.com/twostack/go-p2p-forge"
)

// RateLimitMiddleware returns a forge.Middleware that enforces per-peer rate limits
// using a SingleBucket. Requests exceeding the limit are short-circuited.
func RateLimitMiddleware(limiter *SingleBucket) forge.Middleware {
	return func(sc *forge.StreamContext, next func()) {
		if !limiter.Allow(sc.PeerID) {
			sc.Err = forge.ErrRateLimited
			sc.Logger.Debug("rate limited", "peer", sc.PeerID)
			return
		}
		next()
	}
}

// DualRateLimitMiddleware returns a forge.Middleware that enforces separate
// read/write rate limits. The isWrite function inspects the raw bytes to
// classify the request.
func DualRateLimitMiddleware(limiter *DualBucket, isWrite func(raw []byte) bool) forge.Middleware {
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
