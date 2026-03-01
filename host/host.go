package host

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/muxer/yamux"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	multiaddr "github.com/multiformats/go-multiaddr"
)

// Config holds host creation configuration.
// This is the P2P-specific subset — application config lives in the application.
type Config struct {
	// Network
	Port              int      `yaml:"port"`
	ListenAddresses   []string `yaml:"listen_addresses"`
	ExternalAddresses []string `yaml:"external_addresses"`
	BootstrapPeers    []string `yaml:"bootstrap_peers"`

	// Yamux tuning
	YamuxKeepAlive    time.Duration `yaml:"yamux_keepalive"`
	YamuxWriteTimeout time.Duration `yaml:"yamux_write_timeout"`

	// Relay
	EnableRelay        bool        `yaml:"enable_relay"`
	EnableRelayService bool        `yaml:"enable_relay_service"`
	EnableAutoRelay    bool        `yaml:"enable_auto_relay"`
	EnableHolePunching bool        `yaml:"enable_hole_punching"`
	EnableAutoNAT      bool        `yaml:"enable_autonat"`
	RelayLimits        RelayLimits `yaml:"relay_limits"`
}

// RelayLimits configures circuit relay v2 service resource limits.
type RelayLimits struct {
	MaxReservations        int           `yaml:"max_reservations"`
	MaxCircuits            int           `yaml:"max_circuits"`
	BufferSize             int           `yaml:"buffer_size"`
	MaxReservationsPerPeer int           `yaml:"max_reservations_per_peer"`
	MaxReservationsPerIP   int           `yaml:"max_reservations_per_ip"`
	MaxReservationsPerASN  int           `yaml:"max_reservations_per_asn"`
	ReservationTTL         time.Duration `yaml:"reservation_ttl"`
	ConnectionDuration     time.Duration `yaml:"connection_duration"`
	ConnectionData         int64         `yaml:"connection_data"`
}

// DefaultConfig returns sensible defaults for a libp2p server host.
func DefaultConfig() *Config {
	return &Config{
		Port:              0, // random port
		YamuxKeepAlive:    60 * time.Second,
		YamuxWriteTimeout: 30 * time.Second,
		EnableRelay:       true,
		EnableAutoNAT:     true,
	}
}

// Create creates a libp2p host with the given configuration and identity.
// The transports parameter allows callers to provide custom transport constructors
// (e.g., UDX, TCP, QUIC). If empty, the default libp2p transports are used.
func Create(cfg *Config, priv crypto.PrivKey, logger *slog.Logger, transports ...libp2p.Option) (host.Host, error) {
	listenAddrs := cfg.ListenAddresses
	if len(listenAddrs) == 0 && cfg.Port > 0 {
		listenAddrs = []string{
			fmt.Sprintf("/ip4/0.0.0.0/udp/%d/udx", cfg.Port),
		}
	}

	yamuxTransport := yamux.DefaultTransport
	yamuxCfg := yamuxTransport.Config()
	if cfg.YamuxKeepAlive > 0 {
		yamuxCfg.KeepAliveInterval = cfg.YamuxKeepAlive
	}
	if cfg.YamuxWriteTimeout > 0 {
		yamuxCfg.ConnectionWriteTimeout = cfg.YamuxWriteTimeout
	}

	opts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.Security(noise.ID, noise.New),
		libp2p.Muxer("/yamux/1.0.0", yamuxTransport),
		libp2p.ResourceManager(&network.NullResourceManager{}),
	}

	// Add transport options. If custom transports are provided, disable defaults.
	if len(transports) > 0 {
		opts = append(opts, libp2p.NoTransports)
		opts = append(opts, transports...)
	}

	if len(listenAddrs) > 0 {
		opts = append(opts, libp2p.ListenAddrStrings(listenAddrs...))
	}

	// Advertise external addresses for NAT traversal.
	if len(cfg.ExternalAddresses) > 0 {
		extMAs := make([]multiaddr.Multiaddr, 0, len(cfg.ExternalAddresses))
		for _, addr := range cfg.ExternalAddresses {
			ma, err := multiaddr.NewMultiaddr(addr)
			if err != nil {
				logger.Warn("invalid external address, skipping", "addr", addr, "error", err)
				continue
			}
			extMAs = append(extMAs, ma)
		}
		if len(extMAs) > 0 {
			opts = append(opts, libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
				return append(addrs, extMAs...)
			}))
			logger.Info("advertising external addresses", "count", len(extMAs))
		}
	}

	if cfg.EnableAutoNAT {
		opts = append(opts, libp2p.EnableAutoNATv2())
	}

	// Log relay configuration for diagnostics.
	logger.Info("relay configuration",
		"enable_relay", cfg.EnableRelay,
		"enable_relay_service", cfg.EnableRelayService,
		"enable_auto_relay", cfg.EnableAutoRelay,
		"enable_hole_punching", cfg.EnableHolePunching,
	)

	// Relay configuration.
	var relayResources *relayv2.Resources
	if cfg.EnableRelay {
		opts = append(opts, libp2p.EnableRelay())
		if cfg.EnableRelayService {
			rc := relayv2.DefaultResources()
			applyRelayLimits(&rc, &cfg.RelayLimits)
			relayResources = &rc
			// NOTE: We intentionally do NOT use libp2p.EnableRelayService() here.
			// It creates a RelayManager that defers hop handler registration until
			// a reachability event fires. With ForceReachabilityPublic, there is a
			// race: the event can fire before the manager's goroutine subscribes,
			// so the /libp2p/circuit/relay/0.2.0/hop handler never gets registered.
			// Instead we create the relay service manually after host creation.
			opts = append(opts, libp2p.ForceReachabilityPublic())
		}
		if cfg.EnableHolePunching {
			opts = append(opts, libp2p.EnableHolePunching())
		}
		if cfg.EnableAutoRelay && len(cfg.BootstrapPeers) > 0 {
			relayPeers := parseBootstrapPeers(cfg.BootstrapPeers, logger)
			if len(relayPeers) > 0 {
				opts = append(opts, libp2p.EnableAutoRelayWithStaticRelays(relayPeers))
			}
		}
	} else {
		opts = append(opts, libp2p.DisableRelay())
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create host: %w", err)
	}

	// Create relay v2 service manually to register the hop handler immediately.
	// This avoids the RelayManager race condition described above.
	if relayResources != nil {
		if _, err := relayv2.New(h, relayv2.WithResources(*relayResources)); err != nil {
			h.Close()
			return nil, fmt.Errorf("create relay service: %w", err)
		}
		logger.Info("relay service created",
			"max_reservations", relayResources.MaxReservations,
			"max_circuits", relayResources.MaxCircuits,
		)
	}

	logger.Info("created libp2p host", "peer_id", h.ID().String())
	logger.Info("registered protocols", "protocols", h.Mux().Protocols())
	return h, nil
}

func applyRelayLimits(rc *relayv2.Resources, rl *RelayLimits) {
	if rl.MaxReservations > 0 {
		rc.MaxReservations = rl.MaxReservations
	}
	if rl.MaxCircuits > 0 {
		rc.MaxCircuits = rl.MaxCircuits
	}
	if rl.BufferSize > 0 {
		rc.BufferSize = rl.BufferSize
	}
	if rl.MaxReservationsPerPeer > 0 {
		rc.MaxReservationsPerPeer = rl.MaxReservationsPerPeer
	}
	if rl.MaxReservationsPerIP > 0 {
		rc.MaxReservationsPerIP = rl.MaxReservationsPerIP
	}
	if rl.MaxReservationsPerASN > 0 {
		rc.MaxReservationsPerASN = rl.MaxReservationsPerASN
	}
	if rl.ReservationTTL > 0 {
		rc.ReservationTTL = rl.ReservationTTL
	}
	if rl.ConnectionDuration > 0 {
		rc.Limit.Duration = rl.ConnectionDuration
	}
	if rl.ConnectionData > 0 {
		rc.Limit.Data = rl.ConnectionData
	}
}

func parseBootstrapPeers(addrs []string, logger *slog.Logger) []peer.AddrInfo {
	peers := make([]peer.AddrInfo, 0, len(addrs))
	for _, addr := range addrs {
		ma, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			logger.Warn("invalid bootstrap peer", "addr", addr, "error", err)
			continue
		}
		ai, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			logger.Warn("cannot parse bootstrap peer info", "addr", addr, "error", err)
			continue
		}
		peers = append(peers, *ai)
	}
	return peers
}
