package forge

import (
	"fmt"
	"io"

	"github.com/twostack/go-p2p-forge/codec"
)

// Codec defines a symmetric encoding/decoding interface for stream payloads.
// Implement this interface to support custom serialization formats (e.g., Protobuf).
type Codec interface {
	// Marshal encodes a value into bytes.
	Marshal(v any) ([]byte, error)
	// Unmarshal decodes bytes into the provided pointer.
	Unmarshal(data []byte, v any) error
	// ContentType returns a human-readable name for logging (e.g., "json", "protobuf").
	ContentType() string
}

// DeserializeMiddleware returns a middleware that unmarshals sc.RawBytes into type T
// using the provided codec, storing the result in sc.Request.
func DeserializeMiddleware[T any](c Codec) Middleware {
	return func(sc *StreamContext, next func()) {
		var req T
		if err := c.Unmarshal(sc.RawBytes, &req); err != nil {
			sc.Err = fmt.Errorf("unmarshal %s request: %w", c.ContentType(), err)
			sc.Logger.Error("failed to parse request",
				"codec", c.ContentType(), "error", err, "peer", sc.PeerID)
			return
		}
		sc.Request = &req
		next()
	}
}

// ResponseWriterMiddleware returns a middleware that marshals sc.Response
// using the provided codec and writes it as a length-prefixed frame after
// the downstream handler completes.
//
// If sc.Response implements FrameIterator, each frame is written individually,
// enabling multi-frame compound responses (e.g., metadata + N result frames).
// The FrameIterator produces raw bytes, so the codec is not involved in the
// multi-frame path — marshaling is the iterator's responsibility.
//
// Partial-stream behavior: if an error occurs mid-stream (iterator failure on
// frame N, or a network write error), the client will have received frames 0
// through N-1 with no trailing sentinel. sc.Err is set so upstream middleware
// can detect the incomplete response. Callers that need guaranteed atomicity
// should pre-marshal all frames before setting sc.Response.
func ResponseWriterMiddleware(c Codec) Middleware {
	return func(sc *StreamContext, next func()) {
		next()

		if sc.Response == nil {
			return
		}

		// Multi-frame path: response implements FrameIterator.
		if iter, ok := sc.Response.(FrameIterator); ok {
			for {
				data, err := iter.Next()
				if err == io.EOF {
					return
				}
				if err != nil {
					sc.Err = fmt.Errorf("produce frame: %w", err)
					sc.Logger.Error("failed to produce frame",
						"codec", c.ContentType(), "error", err)
					return
				}
				if err := codec.WriteFrame(sc.Stream, data); err != nil {
					sc.Err = fmt.Errorf("write frame: %w", err)
					sc.Logger.Error("failed to write frame",
						"codec", c.ContentType(), "error", err)
					return
				}
			}
		}

		// Single-frame path.
		data, err := c.Marshal(sc.Response)
		if err != nil {
			sc.Logger.Error("failed to marshal response",
				"codec", c.ContentType(), "error", err)
			return
		}

		if err := codec.WriteFrame(sc.Stream, data); err != nil {
			sc.Logger.Error("failed to write response",
				"codec", c.ContentType(), "error", err)
		}
	}
}
