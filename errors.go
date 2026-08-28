package forge

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors used across the framework.
var (
	// ErrPanic indicates a handler panicked and was recovered by the Recovery middleware.
	ErrPanic = errors.New("handler panicked")

	// ErrRateLimited indicates a request was rejected due to rate limiting.
	ErrRateLimited = errors.New("rate limit exceeded")

	// ErrServerNotStarted indicates an operation requires the server to be started.
	ErrServerNotStarted = errors.New("server not started")
)

// RateLimitedError reports a rate-limit rejection along with how long the
// caller should wait before trying again.
//
// It exists because "rate limit exceeded" on its own leaves a client with
// nothing to do but guess, and clients that guess converge on pacing
// themselves far below what the limiter would actually allow. The limiter
// knows precisely when the next token arrives; this is how it says so.
//
// It unwraps to ErrRateLimited, so every existing errors.Is check keeps
// working and callers can opt into the detail with errors.As.
type RateLimitedError struct {
	// RetryAfter is how long until the limiter would admit the same request.
	// Zero means the limiter could not say — including the case of a request
	// that costs more than the bucket can ever hold, which no amount of
	// waiting will admit.
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	if e.RetryAfter <= 0 {
		return ErrRateLimited.Error()
	}
	return fmt.Sprintf("%s, retry after %s", ErrRateLimited.Error(), e.RetryAfter)
}

func (e *RateLimitedError) Unwrap() error { return ErrRateLimited }
