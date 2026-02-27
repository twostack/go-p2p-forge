package middleware

import (
	"encoding/json"
	"errors"
	"fmt"

	forge "github.com/twostack/go-p2p-forge"
)

// Sentinel errors for operation routing.
var (
	// ErrUnknownOperation indicates the operation field value did not match any registered handler.
	ErrUnknownOperation = errors.New("unknown operation")

	// ErrMissingOperation indicates the operation field was not found in the request payload.
	ErrMissingOperation = errors.New("missing operation field")
)

// OperationRouter returns a middleware that dispatches to sub-handlers based on
// a JSON field in sc.RawBytes. The fieldName parameter specifies which JSON key
// to inspect (e.g., "op", "type", "action"). The routes map associates field
// values to middleware handlers.
//
// Place this middleware after FrameDecodeMiddleware (so sc.RawBytes is populated)
// and before any type-specific deserialization.
//
// If the field is missing, sc.Err is set to ErrMissingOperation.
// If the field value has no matching route, sc.Err is set to ErrUnknownOperation.
func OperationRouter(fieldName string, routes map[string]forge.Middleware) forge.Middleware {
	return func(sc *forge.StreamContext, next func()) {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(sc.RawBytes, &envelope); err != nil {
			sc.Err = fmt.Errorf("parse operation envelope: %w", err)
			sc.Logger.Error("failed to parse operation envelope", "error", err)
			return
		}

		raw, ok := envelope[fieldName]
		if !ok {
			sc.Err = ErrMissingOperation
			sc.Logger.Error("missing operation field", "field", fieldName)
			return
		}

		var opValue string
		if err := json.Unmarshal(raw, &opValue); err != nil {
			sc.Err = fmt.Errorf("parse operation value: %w", err)
			sc.Logger.Error("operation field is not a string", "field", fieldName)
			return
		}

		handler, ok := routes[opValue]
		if !ok {
			sc.Err = ErrUnknownOperation
			sc.Logger.Warn("unknown operation", "field", fieldName, "value", opValue)
			return
		}

		handler(sc, next)
	}
}

// Chain composes multiple middleware into a single middleware.
// This is useful for OperationRouter routes that need more than one step
// (e.g., deserialize + handler).
func Chain(mws ...forge.Middleware) forge.Middleware {
	return func(sc *forge.StreamContext, next func()) {
		chained := next
		for i := len(mws) - 1; i >= 0; i-- {
			mw := mws[i]
			prevNext := chained
			chained = func() {
				if sc.Err != nil {
					return
				}
				mw(sc, prevNext)
			}
		}
		chained()
	}
}
