package host_test

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"

	forgehost "github.com/twostack/go-p2p-forge/host"
)

func tcpHost(t *testing.T, cfg *forgehost.Config) host.Host {
	t.Helper()
	cfg.Port = 0
	cfg.ListenAddresses = []string{"/ip4/127.0.0.1/tcp/0"}
	cfg.EnableRelay = false
	cfg.EnableAutoNAT = false
	h, err := forgehost.Create(cfg, testKey(t), silentLogger(), libp2p.Transport(tcp.NewTCPTransport))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

// max_connections used to be advisory: the host installed a null resource
// manager and accepted everything. Now the cap is enforced at the transport.
func TestCreate_MaxConnectionsIsEnforced(t *testing.T) {
	const cap = 2
	cfg := forgehost.DefaultConfig()
	cfg.MaxConnections = cap
	server := tcpHost(t, cfg)
	target := peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	connected := 0
	for i := 0; i < cap+2; i++ {
		c := tcpHost(t, forgehost.DefaultConfig())
		if err := c.Connect(ctx, target); err == nil {
			connected++
		}
	}
	if connected != cap {
		t.Fatalf("%d peers connected against max_connections=%d", connected, cap)
	}
}

// Zero keeps go-libp2p's defaults rather than refusing everything.
func TestCreate_ZeroMaxConnectionsAccepts(t *testing.T) {
	server := tcpHost(t, forgehost.DefaultConfig())
	c := tcpHost(t, forgehost.DefaultConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Connect(ctx, peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()}); err != nil {
		t.Fatal(err)
	}
}
