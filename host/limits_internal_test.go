package host

import (
	"fmt"
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	ma "github.com/multiformats/go-multiaddr"
)

func openFrom(t *testing.T, mgr network.ResourceManager, addr string) (network.ConnManagementScope, error) {
	t.Helper()
	endpoint, err := ma.NewMultiaddr(addr)
	if err != nil {
		t.Fatal(err)
	}
	return mgr.OpenConnection(network.DirInbound, false, endpoint)
}

// max_connections_per_ip caps what one source address may hold open, and
// zero means no cap: go-libp2p's own default of eight per address, and its
// 0.2 connections per second per address, must not survive.
func TestResourceManagerPerAddressPolicy(t *testing.T) {
	t.Run("cap applies per address", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.MaxConnectionsPerIP = 2
		mgr, err := newResourceManager(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer mgr.Close()

		for i := 0; i < 2; i++ {
			if _, err := openFrom(t, mgr, "/ip4/10.1.2.3/tcp/4001"); err != nil {
				t.Fatalf("connection %d from one address refused under a cap of 2: %v", i+1, err)
			}
		}
		if _, err := openFrom(t, mgr, "/ip4/10.1.2.3/tcp/4001"); err == nil {
			t.Fatal("third connection from one address admitted under a cap of 2")
		}
		if _, err := openFrom(t, mgr, "/ip4/10.1.2.4/tcp/4001"); err != nil {
			t.Fatalf("another address refused: %v", err)
		}
	})

	t.Run("zero is no cap and no rate limit", func(t *testing.T) {
		mgr, err := newResourceManager(DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		defer mgr.Close()

		// Well past go-libp2p's default cap of 8 and its default burst of
		// 16 new connections per address, opened as fast as the loop runs.
		for i := 0; i < 100; i++ {
			if _, err := openFrom(t, mgr, fmt.Sprintf("/ip4/10.9.9.9/tcp/%d", 1000+i)); err != nil {
				t.Fatalf("connection %d from one address refused with no cap configured: %v", i+1, err)
			}
		}
	})
}
