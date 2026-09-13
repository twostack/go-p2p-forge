package forgetest

import (
	"net"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// PipeStream is a network.Stream over one end of a net.Pipe. Unlike
// MockStream it blocks on reads until the other end writes, and it honours
// deadlines, which is what a test of timeouts needs.
type PipeStream struct {
	pipe  net.Conn
	conn  *MockConn
	proto protocol.ID
}

// NewPipeStream returns a server-side stream attributed to peerID and the
// client end the test drives. Close either end to unblock the other.
func NewPipeStream(peerID peer.ID) (*PipeStream, net.Conn) {
	server, client := net.Pipe()
	return &PipeStream{pipe: server, conn: &MockConn{remotePeer: peerID}}, client
}

func (ps *PipeStream) Read(p []byte) (int, error)  { return ps.pipe.Read(p) }
func (ps *PipeStream) Write(p []byte) (int, error) { return ps.pipe.Write(p) }
func (ps *PipeStream) Close() error                { return ps.pipe.Close() }

func (ps *PipeStream) CloseRead() error                                  { return nil }
func (ps *PipeStream) CloseWrite() error                                 { return nil }
func (ps *PipeStream) Reset() error                                      { return ps.pipe.Close() }
func (ps *PipeStream) ResetWithError(code network.StreamErrorCode) error { return ps.pipe.Close() }
func (ps *PipeStream) ID() string                                        { return "pipe-stream-1" }
func (ps *PipeStream) Protocol() protocol.ID                             { return ps.proto }
func (ps *PipeStream) SetProtocol(id protocol.ID) error                  { ps.proto = id; return nil }
func (ps *PipeStream) Stat() network.Stats                               { return network.Stats{} }
func (ps *PipeStream) Conn() network.Conn                                { return ps.conn }
func (ps *PipeStream) Scope() network.StreamScope                        { return &network.NullScope{} }
func (ps *PipeStream) IsClosed() bool                                    { return false }
func (ps *PipeStream) As(target any) bool                                { return false }
func (ps *PipeStream) SetDeadline(t time.Time) error                     { return ps.pipe.SetDeadline(t) }
func (ps *PipeStream) SetReadDeadline(t time.Time) error                 { return ps.pipe.SetReadDeadline(t) }
func (ps *PipeStream) SetWriteDeadline(t time.Time) error                { return ps.pipe.SetWriteDeadline(t) }
