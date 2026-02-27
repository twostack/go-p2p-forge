package forge

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	forgehost "github.com/twostack/go-p2p-forge/host"
	"github.com/twostack/go-p2p-forge/node"
	"github.com/twostack/go-p2p-forge/service"
)

// Server is the main go-p2p-forge server, analogous to Netty's ServerBootstrap.
// It manages a libp2p host, P2P node (DHT + GossipSub), protocol handlers,
// and background services with structured lifecycle management.
type Server struct {
	config     *Config
	logger     *slog.Logger
	identity   crypto.PrivKey
	host       host.Host
	node       *node.Node
	lifecycle  *service.Lifecycle
	handlers       map[protocol.ID]network.StreamHandler
	transports     []libp2p.Option
	registry       *Registry
	onConnected    []func(peer.ID)
	onDisconnected []func(peer.ID)
	activeStreams   sync.WaitGroup
	metrics        MetricsCollector
}

// Option configures a Server.
type Option func(*Server)

// NewServer creates a new server with the given options.
// Call ListenAndServe to start it.
func NewServer(opts ...Option) *Server {
	s := &Server{
		config:   DefaultConfig(),
		logger:   slog.Default(),
		handlers: make(map[protocol.ID]network.StreamHandler),
		registry: NewRegistry(),
		metrics:  NoopMetrics{},
	}
	for _, opt := range opts {
		opt(s)
	}
	s.lifecycle = service.NewLifecycle(s.logger)
	return s
}

// WithConfig sets the server configuration.
func WithConfig(cfg *Config) Option {
	return func(s *Server) { s.config = cfg }
}

// WithLogger sets the structured logger.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Server) { s.logger = logger }
}

// WithIdentity sets the server's private key.
func WithIdentity(priv crypto.PrivKey) Option {
	return func(s *Server) { s.identity = priv }
}

// WithPort sets the listen port.
func WithPort(port int) Option {
	return func(s *Server) { s.config.Host.Port = port }
}

// WithListenAddrs sets the multiaddrs to listen on.
func WithListenAddrs(addrs ...string) Option {
	return func(s *Server) { s.config.Host.ListenAddresses = addrs }
}

// WithBootstrapPeers sets the DHT bootstrap peers.
func WithBootstrapPeers(peers ...string) Option {
	return func(s *Server) { s.config.Node.BootstrapPeers = peers }
}

// WithTransport adds a libp2p transport option.
func WithTransport(t libp2p.Option) Option {
	return func(s *Server) { s.transports = append(s.transports, t) }
}

// WithPreset applies a configuration preset.
func WithPreset(preset Preset) Option {
	return func(s *Server) { preset(s.config) }
}

// WithMetrics sets a MetricsCollector for observability. If not set, a no-op
// collector is used and no metrics overhead is incurred.
func WithMetrics(m MetricsCollector) Option {
	return func(s *Server) { s.metrics = m }
}

// Metrics returns the server's MetricsCollector (NoopMetrics if none was configured).
func (s *Server) Metrics() MetricsCollector { return s.metrics }

// WithShutdownTimeout sets the maximum time to wait for active streams to drain
// during graceful shutdown. Zero disables draining (immediate close).
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) { s.config.ShutdownTimeout = d }
}

// ActiveStreams returns the WaitGroup tracking in-flight streams. Pipelines should
// call Add(1) on stream entry and Done() on stream exit to enable graceful draining.
func (s *Server) ActiveStreams() *sync.WaitGroup { return &s.activeStreams }

// OnPeerConnected registers a callback invoked when a new peer connection is established.
// Multiple callbacks can be registered; they execute in registration order.
func OnPeerConnected(fn func(peer.ID)) Option {
	return func(s *Server) { s.onConnected = append(s.onConnected, fn) }
}

// OnPeerDisconnected registers a callback invoked when a peer disconnects.
// Multiple callbacks can be registered; they execute in registration order.
func OnPeerDisconnected(fn func(peer.ID)) Option {
	return func(s *Server) { s.onDisconnected = append(s.onDisconnected, fn) }
}

// Handle registers a protocol handler.
func (s *Server) Handle(id protocol.ID, handler network.StreamHandler) {
	s.handlers[id] = handler
}

// AddService registers a background service that participates in the lifecycle.
func (s *Server) AddService(svc service.Service) {
	s.lifecycle.Register(svc)
}

// Provide registers a server-level singleton accessible to all pipeline handlers
// via ServiceFrom[T]. Must be called before Start(). Panics on duplicate keys.
func (s *Server) Provide(key string, value any) {
	s.registry.Provide(key, value)
}

// Registry returns the server's service registry for use with Pipeline.WithRegistry().
func (s *Server) Registry() *Registry {
	return s.registry
}

// Host returns the underlying libp2p host (available after Start).
func (s *Server) Host() host.Host { return s.host }

// Node returns the underlying P2P node (available after Start).
func (s *Server) Node() *node.Node { return s.node }

// PeerID returns the server's peer ID (available after Start).
func (s *Server) PeerID() peer.ID {
	if s.host == nil {
		return ""
	}
	return s.host.ID()
}

// OpenStream opens a new stream to the given peer for the specified protocol
// and returns a StreamContext ready for writing. The caller is responsible for
// closing the stream when done.
//
// This enables server-initiated communication (e.g., push notifications).
// Use codec.WriteFrame() and codec.ReadFrame() on sc.Stream for I/O.
func (s *Server) OpenStream(ctx context.Context, peerID peer.ID, proto protocol.ID) (*StreamContext, error) {
	if s.host == nil {
		return nil, ErrServerNotStarted
	}

	stream, err := s.host.NewStream(ctx, peerID, proto)
	if err != nil {
		return nil, fmt.Errorf("open stream to %s: %w", peerID, err)
	}

	sc := &StreamContext{
		Ctx:    ctx,
		Stream: stream,
		PeerID: peerID,
		Logger: s.logger.With("peer", peerID.String(), "protocol", string(proto), "direction", "outbound"),
	}

	return sc, nil
}

// Start initializes the P2P stack, registers handlers, and starts services.
func (s *Server) Start(ctx context.Context) error {
	if err := s.config.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Load or create identity.
	if s.identity == nil {
		identityPath := s.config.DataDirectory + "/peer_identity.key"
		if s.config.IdentityFile != "" {
			identityPath = s.config.IdentityFile
		}
		priv, err := forgehost.LoadOrCreateIdentity(identityPath)
		if err != nil {
			return fmt.Errorf("load identity: %w", err)
		}
		s.identity = priv
	}

	pid, _ := peer.IDFromPrivateKey(s.identity)
	s.logger.Info("loaded identity", "peer_id", pid.String())

	// Create host.
	h, err := forgehost.Create(&s.config.Host, s.identity, s.logger, s.transports...)
	if err != nil {
		return fmt.Errorf("create host: %w", err)
	}
	s.host = h

	// Register connection event hooks.
	if len(s.onConnected) > 0 || len(s.onDisconnected) > 0 {
		s.host.Network().Notify(&network.NotifyBundle{
			ConnectedF: func(_ network.Network, c network.Conn) {
				pid := c.RemotePeer()
				for _, fn := range s.onConnected {
					fn := fn // capture loop variable
					go s.safeCallback(fn, pid, "OnPeerConnected")
				}
			},
			DisconnectedF: func(_ network.Network, c network.Conn) {
				pid := c.RemotePeer()
				for _, fn := range s.onDisconnected {
					fn := fn // capture loop variable
					go s.safeCallback(fn, pid, "OnPeerDisconnected")
				}
			},
		})
	}

	// Create node (DHT + PubSub).
	n, err := node.New(ctx, &s.config.Node, h, s.logger)
	if err != nil {
		h.Close()
		return fmt.Errorf("create node: %w", err)
	}
	s.node = n

	// Register protocol handlers.
	for id, handler := range s.handlers {
		s.host.SetStreamHandler(id, handler)
		s.logger.Info("registered protocol handler", "protocol", id)
	}

	// Start background services.
	if err := s.lifecycle.StartAll(ctx); err != nil {
		n.Close()
		return fmt.Errorf("start services: %w", err)
	}

	s.logger.Info("server started",
		"peer_id", s.host.ID().String(),
		"addrs", fmt.Sprintf("%v", s.host.Addrs()),
	)

	return nil
}

// safeCallback invokes a connection event callback with panic recovery.
func (s *Server) safeCallback(fn func(peer.ID), pid peer.ID, hook string) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in connection callback",
				"hook", hook, "peer", pid, "panic", r)
		}
	}()
	fn(pid)
}

// Stop gracefully shuts down the server. If ShutdownTimeout is configured,
// it waits for active streams to drain before closing the node.
func (s *Server) Stop() error {
	s.logger.Info("stopping server")

	// Remove stream handlers to stop accepting new streams.
	for id := range s.handlers {
		s.host.RemoveStreamHandler(id)
	}

	// Wait for active streams to drain.
	if s.config.ShutdownTimeout > 0 {
		done := make(chan struct{})
		go func() { s.activeStreams.Wait(); close(done) }()
		select {
		case <-done:
			s.logger.Info("all active streams drained")
		case <-time.After(s.config.ShutdownTimeout):
			s.logger.Warn("shutdown timeout reached, forcing close")
		}
	}

	if err := s.lifecycle.StopAll(); err != nil {
		s.logger.Warn("error stopping services", "error", err)
	}

	if s.node != nil {
		if err := s.node.Close(); err != nil {
			s.logger.Warn("error closing node", "error", err)
		}
	}

	s.logger.Info("server stopped")
	return nil
}

// ListenAndServe starts the server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := s.Start(ctx); err != nil {
		return err
	}

	<-ctx.Done()
	return s.Stop()
}
