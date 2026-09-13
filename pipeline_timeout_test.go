package forge_test

import (
	"encoding/binary"
	"io"
	"testing"
	"time"

	forge "github.com/twostack/go-p2p-forge"
	"github.com/twostack/go-p2p-forge/codec"
	"github.com/twostack/go-p2p-forge/forgetest"
)

// A stream that carries only a length prefix used to occupy a goroutine
// forever. Now the idle timeout ends it and the stream is closed.
func TestPipeline_HalfSentFrameIsReapedByIdleTimeout(t *testing.T) {
	handlerRan := false
	pipeline := forge.NewPipeline(testLogger(),
		forge.FrameDecodeMiddleware(codec.NewBufferPool()),
		func(sc *forge.StreamContext, next func()) { handlerRan = true },
	).WithIdleTimeout(100 * time.Millisecond)

	stream, client := forgetest.NewPipeStream(forgetest.GenerateTestPeerID())
	defer client.Close()

	var header [codec.LengthPrefixSize]byte
	binary.BigEndian.PutUint32(header[:], codec.MaxFrameSize)
	go client.Write(header[:])

	done := make(chan struct{})
	go func() {
		pipeline.HandleStream(stream)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("pipeline still waiting on a peer that sent only a length prefix")
	}
	if handlerRan {
		t.Fatal("handler ran on a frame that never arrived")
	}
	// The server closed its end, so the client's read returns.
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("client read after reap = %v; want EOF", err)
	}
}

// sc.Ctx carries the request deadline, so a handler blocked on it returns,
// and the stream is reset out from under a peer that is still connected.
func TestPipeline_RequestTimeoutBoundsTheHandler(t *testing.T) {
	var ctxErr error
	pipeline := forge.NewPipeline(testLogger(),
		func(sc *forge.StreamContext, next func()) {
			<-sc.Ctx.Done()
			ctxErr = sc.Ctx.Err()
		},
	).WithRequestTimeout(100 * time.Millisecond)

	stream, client := forgetest.NewPipeStream(forgetest.GenerateTestPeerID())
	defer client.Close()

	done := make(chan struct{})
	go func() {
		pipeline.HandleStream(stream)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler was not released by the request timeout")
	}
	if ctxErr == nil {
		t.Fatal("sc.Ctx was not cancelled")
	}
}

// Without a timeout firing, sc.Ctx is still cancelled once the stream is
// handled, so nothing derived from it outlives the request.
func TestPipeline_ContextIsCancelledAfterTheStream(t *testing.T) {
	var sc *forge.StreamContext
	pipeline := forge.NewPipeline(testLogger(),
		func(c *forge.StreamContext, next func()) { sc = c },
	)
	pipeline.HandleStream(forgetest.NewMockStream(forgetest.GenerateTestPeerID(), nil))
	if sc.Ctx.Err() == nil {
		t.Fatal("sc.Ctx still live after HandleStream returned")
	}
}
