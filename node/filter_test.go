package node

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"

	dht "github.com/libp2p/go-libp2p-kad-dht"
)

func tcpHost(t *testing.T, opts ...libp2p.Option) host.Host {
	t.Helper()
	h, err := libp2p.New(append([]libp2p.Option{libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0")}, opts...)...)
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// dhtServer makes h answer DHT queries so it qualifies for a routing table.
func dhtServer(t *testing.T, h host.Host) {
	t.Helper()
	d, err := dht.New(h, dht.Mode(dht.ModeServer))
	if err != nil {
		t.Fatalf("create dht: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
}

func connectDirect(t *testing.T, a, b host.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Connect(ctx, peer.AddrInfo{ID: b.ID(), Addrs: b.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(msg)
}

// Only a peer this host has itself reached over a direct connection enters
// the routing table. The peer that dials in is the mobile client behind a
// NAT: admitting it on the strength of its inbound connection is what used
// to make bucket refreshes query it, tear the connection down, and drain
// the table. A relay-only peer stays out as well.
func TestOnlyPeersWeHaveDialledEnterTheRoutingTable(t *testing.T) {
	ctx := context.Background()

	relayHost := tcpHost(t)
	r, err := relay.New(relayHost)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// The relay-only peer listens nowhere; it can only be reached through
	// the relay it holds a reservation on.
	behindNAT, err := libp2p.New(libp2p.NoListenAddrs, libp2p.EnableRelay())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = behindNAT.Close() })
	dhtServer(t, behindNAT)
	connectDirect(t, behindNAT, relayHost)
	if _, err := client.Reserve(ctx, behindNAT, relayHost.Peerstore().PeerInfo(relayHost.ID())); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Two DHT peers on direct connections: one we dial, one that dials us.
	dialled := tcpHost(t)
	dhtServer(t, dialled)
	dialsIn := tcpHost(t)
	dhtServer(t, dialsIn)

	serverHost := tcpHost(t, libp2p.EnableRelay())
	server, err := New(ctx, &Config{DHTMode: DHTModeServer}, serverHost, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	serverHost.Peerstore().AddAddrs(relayHost.ID(), relayHost.Addrs(), peerstore.PermanentAddrTTL)
	circuit := ma.StringCast(fmt.Sprintf("/p2p/%s/p2p-circuit", relayHost.ID()))
	if err := serverHost.Connect(ctx, peer.AddrInfo{ID: behindNAT.ID(), Addrs: []ma.Multiaddr{circuit}}); err != nil {
		t.Fatalf("connect over relay: %v", err)
	}
	connectDirect(t, dialsIn, serverHost)
	connectDirect(t, serverHost, dialled)

	rt := server.DHT().RoutingTable()
	waitFor(t, 10*time.Second, func() bool { return rt.Find(dialled.ID()) != "" },
		"the peer we dialled never entered the routing table")

	// The other two were identified before the dialled peer was, so their
	// admission would have run first; the wait is for the liveliness probe
	// kad-dht runs before adding a peer, which takes a round trip.
	time.Sleep(time.Second)
	if rt.Find(dialsIn.ID()) != "" {
		t.Fatal("a peer known only by its inbound connection entered the routing table")
	}
	if rt.Find(behindNAT.ID()) != "" {
		t.Fatal("relay-only peer entered the routing table")
	}

	filter := dialableFilter(serverHost, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !filter(nil, dialled.ID()) {
		t.Fatal("filter refuses a peer we dialled directly")
	}
	if filter(nil, dialsIn.ID()) {
		t.Fatal("filter admits a peer that only dialled us")
	}
	if filter(nil, behindNAT.ID()) {
		t.Fatal("filter admits a peer with only a relayed connection")
	}
	var nobody peer.ID = "12D3KooWQYV9dGMFoRzNStwpXztXaBUjtPqi6aU76ZgUriHhKust"
	if filter(nil, nobody) {
		t.Fatal("filter admits a peer with no connection")
	}
}
