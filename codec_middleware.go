package forge

import (
	"encoding/json"
	"fmt"

	"github.com/twostack/go-p2p-forge/codec"
)

// FrameDecodeMiddleware reads a length-prefixed frame from the stream and
// stores the raw bytes in sc.RawBytes. Uses buffer pooling to reduce GC pressure.
func FrameDecodeMiddleware(pool *codec.BufferPool) Middleware {
	return func(sc *StreamContext, next func()) {
		buf, err := codec.ReadFramePooled(sc.Stream, pool)
		if err != nil {
			sc.Err = err
			sc.Logger.Error("failed to read frame", "error", err, "peer", sc.PeerID)
			return
		}
		sc.RawBytes = buf.Bytes()
		sc.PoolBuf = buf
		next()
	}
}

// JSONDeserialize returns a middleware that unmarshals sc.RawBytes into type T
// and stores it in sc.Request. Uses Go generics for type safety.
func JSONDeserialize[T any]() Middleware {
	return func(sc *StreamContext, next func()) {
		var req T
		if err := json.Unmarshal(sc.RawBytes, &req); err != nil {
			sc.Err = fmt.Errorf("unmarshal request: %w", err)
			sc.Logger.Error("failed to parse request", "error", err, "peer", sc.PeerID)
			return
		}
		sc.Request = &req
		next()
	}
}

// JSONResponseWriter returns a middleware that marshals sc.Response and writes
// it as a length-prefixed frame after the downstream handler completes.
// This is the outbound half of the pipeline — it wraps the handler call.
func JSONResponseWriter() Middleware {
	return func(sc *StreamContext, next func()) {
		next()

		if sc.Response == nil {
			return
		}

		data, err := json.Marshal(sc.Response)
		if err != nil {
			sc.Logger.Error("failed to marshal response", "error", err)
			return
		}

		if err := codec.WriteFrame(sc.Stream, data); err != nil {
			sc.Logger.Error("failed to write response", "error", err)
		}
	}
}
