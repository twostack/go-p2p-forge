// Package forgetest provides test helpers for the forge framework.
// MockStream and MockConn allow testing middleware and pipelines without
// a real libp2p network.
package forgetest

import (
	"bytes"
	"context"
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	multiaddr "github.com/multiformats/go-multiaddr"

	"github.com/twostack/go-p2p-forge/codec"
)

// MockStream implements network.Stream for testing without libp2p.
type MockStream struct {
	ReadBuf  *bytes.Buffer
	WriteBuf *bytes.Buffer
	conn     *MockConn
	proto    protocol.ID
	closed   bool
}

// NewMockStream creates a mock stream with the given peer ID and pre-loaded request data.
func NewMockStream(peerID peer.ID, requestData []byte) *MockStream {
	return &MockStream{
		ReadBuf:  bytes.NewBuffer(requestData),
		WriteBuf: &bytes.Buffer{},
		conn:     &MockConn{remotePeer: peerID},
	}
}

// NewMockStreamWithFrame creates a mock stream with request data wrapped in a length-prefixed frame.
func NewMockStreamWithFrame(peerID peer.ID, payload []byte) *MockStream {
	var buf bytes.Buffer
	codec.WriteFrame(&buf, payload)
	return NewMockStream(peerID, buf.Bytes())
}

// ReadResponseFrame reads a length-prefixed frame from the stream's write buffer.
func (ms *MockStream) ReadResponseFrame() ([]byte, error) {
	return codec.ReadFrame(ms.WriteBuf)
}

// network.Stream interface implementation

func (ms *MockStream) Read(p []byte) (int, error)            { return ms.ReadBuf.Read(p) }
func (ms *MockStream) Write(p []byte) (int, error)           { return ms.WriteBuf.Write(p) }
func (ms *MockStream) Close() error                          { ms.closed = true; return nil }
func (ms *MockStream) CloseRead() error                      { return nil }
func (ms *MockStream) CloseWrite() error                     { return nil }
func (ms *MockStream) Reset() error                          { return nil }
func (ms *MockStream) ResetWithError(errCode network.StreamErrorCode) error { return nil }
func (ms *MockStream) SetDeadline(t time.Time) error         { return nil }
func (ms *MockStream) SetReadDeadline(t time.Time) error     { return nil }
func (ms *MockStream) SetWriteDeadline(t time.Time) error    { return nil }
func (ms *MockStream) ID() string                            { return "mock-stream-1" }
func (ms *MockStream) Protocol() protocol.ID                 { return ms.proto }
func (ms *MockStream) SetProtocol(id protocol.ID) error      { ms.proto = id; return nil }
func (ms *MockStream) Stat() network.Stats                   { return network.Stats{} }
func (ms *MockStream) Conn() network.Conn                    { return ms.conn }
func (ms *MockStream) Scope() network.StreamScope            { return &network.NullScope{} }
func (ms *MockStream) IsClosed() bool                        { return ms.closed }
func (ms *MockStream) As(target any) bool                    { return false }

// MockConn implements the subset of network.Conn needed by forge middleware.
type MockConn struct {
	remotePeer peer.ID
}

func (mc *MockConn) RemotePeer() peer.ID                      { return mc.remotePeer }
func (mc *MockConn) ID() string                               { return "mock-conn-1" }
func (mc *MockConn) Close() error                             { return nil }
func (mc *MockConn) IsClosed() bool                           { return false }
func (mc *MockConn) NewStream(ctx context.Context) (network.Stream, error) {
	return nil, io.ErrClosedPipe
}
func (mc *MockConn) GetStreams() []network.Stream              { return nil }
func (mc *MockConn) Stat() network.ConnStats                  { return network.ConnStats{} }
func (mc *MockConn) Scope() network.ConnScope                 { return &network.NullScope{} }
func (mc *MockConn) ConnState() network.ConnectionState       { return network.ConnectionState{} }
func (mc *MockConn) LocalPeer() peer.ID                       { return "" }
func (mc *MockConn) RemotePublicKey() crypto.PubKey            { return nil }
func (mc *MockConn) LocalMultiaddr() multiaddr.Multiaddr      { return nil }
func (mc *MockConn) RemoteMultiaddr() multiaddr.Multiaddr     { return nil }
func (mc *MockConn) As(target any) bool                        { return false }
func (mc *MockConn) CloseWithError(code network.ConnErrorCode) error { return nil }

// GenerateTestPeerID generates a random peer ID for testing.
func GenerateTestPeerID() peer.ID {
	priv, _, _ := crypto.GenerateEd25519Key(nil)
	id, _ := peer.IDFromPrivateKey(priv)
	return id
}
