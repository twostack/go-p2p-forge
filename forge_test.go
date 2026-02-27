package forge_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"

	forge "github.com/twostack/go-p2p-forge"
	"github.com/twostack/go-p2p-forge/codec"
	"github.com/twostack/go-p2p-forge/forgetest"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testIdentity(t *testing.T) crypto.PrivKey {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func TestServer_OpenStream_NotStarted(t *testing.T) {
	s := forge.NewServer()

	pid := forgetest.GenerateTestPeerID()
	_, err := s.OpenStream(nil, pid, "/test/1.0.0")
	if err == nil {
		t.Fatal("expected error when server not started")
	}
	if !errors.Is(err, forge.ErrServerNotStarted) {
		t.Errorf("expected ErrServerNotStarted, got: %v", err)
	}
}

func TestServer_OpenStream_Integration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dataDir := t.TempDir()

	// Start server A.
	cfgA := serverConfig(dataDir + "/a")
	cfgA.Host.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	serverA := forge.NewServer(
		forge.WithIdentity(testIdentity(t)),
		forge.WithLogger(silentLogger()),
		forge.WithTransport(libp2p.Transport(tcp.NewTCPTransport)),
		forge.WithConfig(cfgA),
	)
	if err := serverA.Start(ctx); err != nil {
		t.Fatal("start server A:", err)
	}
	defer serverA.Stop()

	// Start server B with a stream handler for the test protocol.
	receivedCh := make(chan []byte, 1)
	const protoID = "/test/openstream/1.0.0"

	cfgB := serverConfig(dataDir + "/b")
	cfgB.Host.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	serverB := forge.NewServer(
		forge.WithIdentity(testIdentity(t)),
		forge.WithLogger(silentLogger()),
		forge.WithTransport(libp2p.Transport(tcp.NewTCPTransport)),
		forge.WithConfig(cfgB),
	)
	if err := serverB.Start(ctx); err != nil {
		t.Fatal("start server B:", err)
	}
	defer serverB.Stop()

	// Register a handler on B that reads one frame.
	serverB.Host().SetStreamHandler(protoID, func(s network.Stream) {
		defer s.Close()
		data, err := codec.ReadFrame(s)
		if err != nil {
			return
		}
		receivedCh <- data
	})

	// Connect A to B.
	bInfo := peer.AddrInfo{ID: serverB.PeerID(), Addrs: serverB.Host().Addrs()}
	if err := serverA.Host().Connect(ctx, bInfo); err != nil {
		t.Fatal("connect A→B:", err)
	}

	// A opens an outbound stream to B.
	sc, err := serverA.OpenStream(ctx, serverB.PeerID(), protoID)
	if err != nil {
		t.Fatal("OpenStream:", err)
	}
	defer sc.Stream.Close()

	// Write a frame through the stream.
	payload := []byte(`{"notify":"new_message"}`)
	if err := codec.WriteFrame(sc.Stream, payload); err != nil {
		t.Fatal("WriteFrame:", err)
	}

	// Verify B received it.
	select {
	case received := <-receivedCh:
		if string(received) != string(payload) {
			t.Errorf("expected %q, got %q", payload, received)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for frame on server B")
	}
}

func TestOnPeerConnected_ServerCreation(t *testing.T) {
	var called bool
	s := forge.NewServer(
		forge.OnPeerConnected(func(p peer.ID) { called = true }),
		forge.OnPeerDisconnected(func(p peer.ID) {}),
	)
	if s == nil {
		t.Fatal("expected non-nil server")
	}
	if called {
		t.Error("callback should not be invoked before connections")
	}
}

// serverConfig returns a minimal Config suitable for in-process testing.
func serverConfig(dataDir string) *forge.Config {
	os.MkdirAll(dataDir, 0o755)
	cfg := forge.DefaultConfig()
	cfg.DataDirectory = dataDir
	cfg.Host.EnableRelay = false
	cfg.Node.EnablePubSub = false
	cfg.Node.BootstrapPeers = nil
	return cfg
}
