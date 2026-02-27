package forge

import (
	"io"

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
// This is equivalent to DeserializeMiddleware[T](JSONCodec{}).
func JSONDeserialize[T any]() Middleware {
	return DeserializeMiddleware[T](JSONCodec{})
}

// JSONResponseWriter returns a middleware that marshals sc.Response and writes
// it as a length-prefixed frame after the downstream handler completes.
// This is equivalent to ResponseWriterMiddleware(JSONCodec{}).
//
// Supports both single-frame responses and multi-frame responses via FrameIterator.
func JSONResponseWriter() Middleware {
	return ResponseWriterMiddleware(JSONCodec{})
}

// FrameIterator produces a sequence of response frames.
// Implementations return each frame's bytes via Next().
// Return io.EOF to signal the end of the sequence.
//
// When sc.Response implements FrameIterator, ResponseWriterMiddleware
// writes each frame individually instead of marshaling a single object.
type FrameIterator interface {
	Next() ([]byte, error)
}

// SliceIterator implements FrameIterator by marshaling a slice of objects
// into individual frames using the provided Codec, one per call to Next().
type SliceIterator struct {
	codec Codec
	items []any
	idx   int
}

// NewSliceIterator creates a FrameIterator from a slice of response objects.
// Each object is marshaled into a separate frame using the provided codec.
func NewSliceIterator(c Codec, items ...any) *SliceIterator {
	return &SliceIterator{codec: c, items: items}
}

// Next returns the next frame's bytes, or io.EOF when all items have been yielded.
func (si *SliceIterator) Next() ([]byte, error) {
	if si.idx >= len(si.items) {
		return nil, io.EOF
	}
	data, err := si.codec.Marshal(si.items[si.idx])
	si.idx++
	if err != nil {
		return nil, err
	}
	return data, nil
}
