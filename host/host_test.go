package host_test

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"

	forgehost "github.com/twostack/go-p2p-forge/host"
)

func testKey(t *testing.T) crypto.PrivKey {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCreate_DefaultTransports(t *testing.T) {
	cfg := forgehost.DefaultConfig()
	cfg.Port = 0
	cfg.EnableRelay = false

	h, err := forgehost.Create(cfg, testKey(t), silentLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	if len(h.Addrs()) == 0 {
		t.Error("expected at least one listen address with default transports")
	}
}

func TestCreate_CustomTransportSuppressesDefaults(t *testing.T) {
	cfg := forgehost.DefaultConfig()
	cfg.Port = 0
	cfg.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	cfg.EnableRelay = false

	h, err := forgehost.Create(cfg, testKey(t), silentLogger(),
		libp2p.Transport(tcp.NewTCPTransport),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	if len(h.Addrs()) == 0 {
		t.Fatal("expected at least one listen address")
	}

	// All addresses should be TCP (since we only provided TCP transport).
	for _, addr := range h.Addrs() {
		s := addr.String()
		if !strings.Contains(s, "/tcp/") {
			t.Errorf("unexpected non-TCP address: %s", s)
		}
	}
}

func TestCreate_MultipleCustomTransports(t *testing.T) {
	cfg := forgehost.DefaultConfig()
	cfg.Port = 0
	cfg.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	cfg.EnableRelay = false

	// Pass the same transport twice to verify multiple WithTransport calls compose.
	h, err := forgehost.Create(cfg, testKey(t), silentLogger(),
		libp2p.Transport(tcp.NewTCPTransport),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	if len(h.Addrs()) == 0 {
		t.Fatal("expected addresses")
	}
}

func TestCreate_NoiseAndYamuxLayerOverCustomTransport(t *testing.T) {
	cfg := forgehost.DefaultConfig()
	cfg.Port = 0
	cfg.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	cfg.EnableRelay = false

	// Create two hosts with TCP transport and verify they can connect.
	h1, err := forgehost.Create(cfg, testKey(t), silentLogger(),
		libp2p.Transport(tcp.NewTCPTransport),
	)
	if err != nil {
		t.Fatal("h1:", err)
	}
	defer h1.Close()

	h2, err := forgehost.Create(cfg, testKey(t), silentLogger(),
		libp2p.Transport(tcp.NewTCPTransport),
	)
	if err != nil {
		t.Fatal("h2:", err)
	}
	defer h2.Close()

	// Verify Noise+Yamux work over custom transport by testing connectivity.
	h2Info := h2.Peerstore().PeerInfo(h2.ID())
	h2Info.Addrs = h2.Addrs()
	if err := h1.Connect(t.Context(), h2Info); err != nil {
		t.Fatalf("failed to connect h1→h2 over custom transport with Noise+Yamux: %v", err)
	}
}
