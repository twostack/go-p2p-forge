package middleware

import (
	"errors"

	forge "github.com/twostack/go-p2p-forge"
)

// ErrPanic indicates a handler panicked and was recovered.
var ErrPanic = errors.New("handler panicked")

// Recovery returns a middleware that catches panics from downstream handlers
// and logs them without crashing the server.
func Recovery() forge.Middleware {
	return func(sc *forge.StreamContext, next func()) {
		defer func() {
			if r := recover(); r != nil {
				sc.Logger.Error("handler panicked", "panic", r, "peer", sc.PeerID)
				sc.Err = ErrPanic
			}
		}()
		next()
	}
}
