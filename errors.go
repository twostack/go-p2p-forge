package forge

import "errors"

// Sentinel errors used across the framework.
var (
	// ErrPanic indicates a handler panicked and was recovered by the Recovery middleware.
	ErrPanic = errors.New("handler panicked")

	// ErrRateLimited indicates a request was rejected due to rate limiting.
	ErrRateLimited = errors.New("rate limit exceeded")

	// ErrServerNotStarted indicates an operation requires the server to be started.
	ErrServerNotStarted = errors.New("server not started")
)
