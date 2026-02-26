package forge

import (
	"context"
	"fmt"
	"log/slog"

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
	handlers   map[protocol.ID]network.StreamHandler
	transports []libp2p.Option
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

// Handle registers a protocol handler.
func (s *Server) Handle(id protocol.ID, handler network.StreamHandler) {
	s.handlers[id] = handler
}

// AddService registers a background service that participates in the lifecycle.
func (s *Server) AddService(svc service.Service) {
	s.lifecycle.Register(svc)
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

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	s.logger.Info("stopping server")

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
