package host

import (
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/x/rate"
)

// DefaultYamuxMaxIncomingStreams caps the streams one connection may have
// open towards this host at once. go-libp2p's own yamux default is
// effectively unlimited because it expects the resource manager to bound
// streams per peer; this is the per-connection backstop under that.
const DefaultYamuxMaxIncomingStreams = 512

// streamsPerConn is the average number of concurrent inbound streams the
// system-wide stream limit allows per connection. Request/response protocols
// hold one stream per in-flight request, so a handful per connection is
// generous; the memory limit bounds the rest.
const streamsPerConn = 8

// connectionLimitOptions returns the resource manager and connection manager
// that bound this host to cfg.MaxConnections and cfg.MaxConnectionsPerIP.
//
// The resource manager is the hard limit: a connection or stream beyond it is
// refused at the transport. The connection manager works underneath it,
// trimming the least valuable connections once the count passes 90% of the
// cap so there is always room for a new peer, rather than letting the host
// run flat against the hard limit and refuse everyone.
//
// With MaxConnections zero, go-libp2p's memory-scaled limits and its default
// 160/192 connection manager apply. The resource manager is still built
// here rather than left to go-libp2p, so that the per-address policy is the
// one in cfg and never go-libp2p's own.
func connectionLimitOptions(cfg *Config, logger *slog.Logger) ([]libp2p.Option, error) {
	mgr, err := newResourceManager(cfg)
	if err != nil {
		return nil, err
	}
	opts := []libp2p.Option{libp2p.ResourceManager(mgr)}

	maxConns := cfg.MaxConnections
	if maxConns <= 0 {
		logger.Info("connection limits: libp2p defaults (max_connections not set)",
			"max_connections_per_ip", cfg.MaxConnectionsPerIP)
		return opts, nil
	}

	high := maxConns - maxConns/10
	low := maxConns - maxConns/5
	if high <= 0 {
		high = maxConns
	}
	if low <= 0 {
		low = high
	}
	grace := cfg.ConnManagerGracePeriod
	if grace <= 0 {
		grace = DefaultConnManagerGracePeriod
	}
	cm, err := connmgr.NewConnManager(low, high, connmgr.WithGracePeriod(grace))
	if err != nil {
		mgr.Close()
		return nil, fmt.Errorf("create connection manager: %w", err)
	}

	logger.Info("connection limits",
		"max_connections", maxConns,
		"max_connections_per_ip", cfg.MaxConnectionsPerIP,
		"transient_connections", max(maxConns/4, 64),
		"system_streams_inbound", maxConns*streamsPerConn,
		"connmgr_high_water", high,
		"connmgr_low_water", low,
		"connmgr_grace", grace,
	)

	return append(opts, libp2p.ConnectionManager(cm)), nil
}

// newResourceManager builds the resource manager for cfg: system and
// transient limits from MaxConnections when it is set, go-libp2p's scaled
// defaults otherwise, and in both cases the per-address policy from
// MaxConnectionsPerIP with no per-address rate limit.
func newResourceManager(cfg *Config) (network.ResourceManager, error) {
	scaled := rcmgr.DefaultLimits
	libp2p.SetDefaultServiceLimits(&scaled)
	limits := scaled.AutoScale()

	if maxConns := cfg.MaxConnections; maxConns > 0 {
		system := rcmgr.ResourceLimits{
			Conns:           rcmgr.LimitVal(maxConns),
			ConnsInbound:    rcmgr.LimitVal(maxConns),
			ConnsOutbound:   rcmgr.LimitVal(maxConns),
			Streams:         rcmgr.LimitVal(maxConns * streamsPerConn * 2),
			StreamsInbound:  rcmgr.LimitVal(maxConns * streamsPerConn),
			StreamsOutbound: rcmgr.LimitVal(maxConns * streamsPerConn),
			FD:              rcmgr.LimitVal(maxConns),
		}
		// Transient covers connections still in the handshake, before they
		// are attributed to a peer. A quarter of the cap lets a reconnect
		// storm after a restart get through instead of being refused at the
		// door.
		transientConns := max(maxConns/4, 64)
		transient := rcmgr.ResourceLimits{
			Conns:           rcmgr.LimitVal(transientConns),
			ConnsInbound:    rcmgr.LimitVal(transientConns),
			ConnsOutbound:   rcmgr.LimitVal(transientConns),
			Streams:         rcmgr.LimitVal(transientConns * streamsPerConn * 2),
			StreamsInbound:  rcmgr.LimitVal(transientConns * streamsPerConn),
			StreamsOutbound: rcmgr.LimitVal(transientConns * streamsPerConn),
			FD:              rcmgr.LimitVal(transientConns),
		}
		limits = rcmgr.PartialLimitConfig{System: system, Transient: transient}.Build(limits)
	}

	mgr, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(limits), perAddressOptions(cfg.MaxConnectionsPerIP)...)
	if err != nil {
		return nil, fmt.Errorf("create resource manager: %w", err)
	}
	return mgr, nil
}

// perAddressOptions is the per-source-address policy: a concurrency cap of
// perIP connections (none when perIP is zero) and no rate limit on new
// connections. Loopback keeps go-libp2p's exemption from the cap.
func perAddressOptions(perIP int) []rcmgr.Option {
	capacity := math.MaxInt
	if perIP > 0 {
		capacity = perIP
	}
	return []rcmgr.Option{
		rcmgr.WithLimitPerSubnet(
			[]rcmgr.ConnLimitPerSubnet{{PrefixLength: 32, ConnCount: capacity}},
			[]rcmgr.ConnLimitPerSubnet{
				{PrefixLength: 56, ConnCount: capacity},
				{PrefixLength: 48, ConnCount: saturatingMul(capacity, 8)},
			},
		),
		rcmgr.WithConnRateLimiters(&rate.Limiter{}),
	}
}

func saturatingMul(a, b int) int {
	if a > math.MaxInt/b {
		return math.MaxInt
	}
	return a * b
}

// DefaultConnManagerGracePeriod is how long a new connection is protected
// from trimming. Long enough for a client to finish its handshake and first
// request; short enough that a reconnect storm does not become untrimmable.
const DefaultConnManagerGracePeriod = 30 * time.Second
