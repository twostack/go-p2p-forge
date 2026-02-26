// Package node wraps a libp2p host with DHT and GossipSub.
package node

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// DHTMode specifies how the DHT operates.
type DHTMode int

const (
	// DHTModeServer runs the DHT in server mode (serves queries).
	DHTModeServer DHTMode = iota
	// DHTModeClient runs the DHT in client mode (queries only).
	DHTModeClient
	// DHTModeAuto lets the DHT choose based on reachability.
	DHTModeAuto
)

// Config holds node configuration.
type Config struct {
	DHTMode        DHTMode  `yaml:"dht_mode"`
	BootstrapPeers []string `yaml:"bootstrap_peers"`
	EnablePubSub   bool     `yaml:"enable_pubsub"`
}

// DefaultConfig returns default node configuration.
func DefaultConfig() *Config {
	return &Config{
		DHTMode:      DHTModeServer,
		EnablePubSub: true,
	}
}

// Node wraps a libp2p host with DHT and GossipSub.
type Node struct {
	host   host.Host
	dht    *dht.IpfsDHT
	pubsub *pubsub.PubSub
	topics map[string]*pubsub.Topic
	subs   map[string]*pubsub.Subscription
	logger *slog.Logger
	mu     sync.RWMutex
	cancel context.CancelFunc
}

// New creates and starts a new P2P node with DHT and optional GossipSub.
func New(ctx context.Context, cfg *Config, h host.Host, logger *slog.Logger) (*Node, error) {
	ctx, cancel := context.WithCancel(ctx)

	// Map DHTMode to dht.ModeOpt.
	var mode dht.ModeOpt
	switch cfg.DHTMode {
	case DHTModeServer:
		mode = dht.ModeServer
	case DHTModeClient:
		mode = dht.ModeClient
	case DHTModeAuto:
		mode = dht.ModeAuto
	default:
		mode = dht.ModeServer
	}

	kadDHT, err := dht.New(ctx, h,
		dht.Mode(mode),
		dht.AddressFilter(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
			return addrs
		}),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create dht: %w", err)
	}
	logger.Info("DHT initialized", "mode", cfg.DHTMode)

	if err := kadDHT.Bootstrap(ctx); err != nil {
		kadDHT.Close()
		cancel()
		return nil, fmt.Errorf("bootstrap dht: %w", err)
	}

	// Connect to bootstrap peers.
	for _, peerAddr := range cfg.BootstrapPeers {
		maddr, err := multiaddr.NewMultiaddr(peerAddr)
		if err != nil {
			logger.Warn("invalid bootstrap peer", "addr", peerAddr, "error", err)
			continue
		}
		info, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			logger.Warn("invalid bootstrap peer info", "addr", peerAddr, "error", err)
			continue
		}
		if err := h.Connect(ctx, *info); err != nil {
			logger.Warn("failed to connect to bootstrap peer", "peer", info.ID, "error", err)
		} else {
			logger.Info("connected to bootstrap peer", "peer", info.ID)
		}
	}

	node := &Node{
		host:   h,
		dht:    kadDHT,
		topics: make(map[string]*pubsub.Topic),
		subs:   make(map[string]*pubsub.Subscription),
		logger: logger,
		cancel: cancel,
	}

	// Initialize GossipSub if enabled.
	if cfg.EnablePubSub {
		ps, err := pubsub.NewGossipSub(ctx, h,
			pubsub.WithMessageSignaturePolicy(pubsub.StrictSign),
		)
		if err != nil {
			kadDHT.Close()
			cancel()
			return nil, fmt.Errorf("create gossipsub: %w", err)
		}
		node.pubsub = ps
		logger.Info("GossipSub initialized")
	}

	return node, nil
}

// PeerID returns this node's peer ID.
func (n *Node) PeerID() peer.ID { return n.host.ID() }

// Addrs returns this node's listen addresses.
func (n *Node) Addrs() []multiaddr.Multiaddr { return n.host.Addrs() }

// Host returns the underlying libp2p host.
func (n *Node) Host() host.Host { return n.host }

// DHT returns the underlying Kademlia DHT.
func (n *Node) DHT() *dht.IpfsDHT { return n.dht }

// PubSub returns the underlying GossipSub instance.
func (n *Node) PubSub() *pubsub.PubSub { return n.pubsub }

// JoinTopic joins a GossipSub topic.
func (n *Node) JoinTopic(name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.pubsub == nil {
		return fmt.Errorf("pubsub not enabled")
	}

	if _, exists := n.topics[name]; exists {
		return nil
	}

	topic, err := n.pubsub.Join(name)
	if err != nil {
		return fmt.Errorf("join topic %s: %w", name, err)
	}

	sub, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("subscribe topic %s: %w", name, err)
	}

	n.topics[name] = topic
	n.subs[name] = sub
	n.logger.Info("joined topic", "topic", name)

	return nil
}

// Publish publishes data to a topic.
func (n *Node) Publish(ctx context.Context, topic string, data []byte) error {
	n.mu.RLock()
	t, exists := n.topics[topic]
	n.mu.RUnlock()

	if !exists {
		return fmt.Errorf("topic %s not joined", topic)
	}

	return t.Publish(ctx, data)
}

// Subscribe returns a subscription for a topic.
func (n *Node) Subscribe(topic string) *pubsub.Subscription {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.subs[topic]
}

// LogDHTStatus logs the current DHT routing table and connection state.
func (n *Node) LogDHTStatus() {
	rt := n.dht.RoutingTable()
	rtPeers := rt.ListPeers()
	peerIDs := make([]string, len(rtPeers))
	for i, p := range rtPeers {
		peerIDs[i] = p.String()
	}

	connectedPeers := n.host.Network().Peers()
	n.logger.Info("DHT status",
		"routing_table_size", rt.Size(),
		"routing_table_peers", peerIDs,
		"connected_peers", len(connectedPeers),
		"peerstore_peers", len(n.host.Peerstore().Peers()),
	)
}

// Close shuts down the P2P node.
func (n *Node) Close() error {
	n.cancel()

	n.mu.Lock()
	for _, sub := range n.subs {
		sub.Cancel()
	}
	for _, topic := range n.topics {
		topic.Close()
	}
	n.mu.Unlock()

	if err := n.dht.Close(); err != nil {
		n.logger.Warn("error closing dht", "error", err)
	}

	return n.host.Close()
}
