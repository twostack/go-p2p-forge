package forge

import (
	"context"
	"log/slog"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/twostack/go-p2p-forge/codec"
)

// StreamContext carries request-scoped data through the middleware pipeline.
// Analogous to Netty's ChannelHandlerContext.
type StreamContext struct {
	// Ctx is the request-scoped context, cancelled on stream close.
	Ctx context.Context

	// Stream is the underlying libp2p stream.
	Stream network.Stream

	// PeerID is the remote peer's identity.
	PeerID peer.ID

	// Logger is pre-tagged with peer and protocol info.
	Logger *slog.Logger

	// RawBytes holds the raw frame payload (set by FrameDecode middleware).
	RawBytes []byte

	// Request holds the decoded request object (set by deserialize middleware).
	Request any

	// Response holds the response to be encoded (set by the handler).
	Response any

	// Err signals an error that short-circuits the pipeline.
	Err error

	// PoolBuf tracks a pooled buffer for lifecycle management.
	PoolBuf *codec.PoolBuffer

	// values is a bag for inter-middleware communication.
	values map[any]any
}

// Set stores a key-value pair for inter-middleware communication.
func (sc *StreamContext) Set(key, val any) {
	if sc.values == nil {
		sc.values = make(map[any]any, 4)
	}
	sc.values[key] = val
}

// Get retrieves a value by key.
func (sc *StreamContext) Get(key any) (any, bool) {
	if sc.values == nil {
		return nil, false
	}
	v, ok := sc.values[key]
	return v, ok
}
