package host

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
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
// that bound this host to cfg.MaxConnections.
//
// The resource manager is the hard limit: a connection or stream beyond it is
// refused at the transport. The connection manager works underneath it,
// trimming the least valuable connections once the count passes 90% of the
// cap so there is always room for a new peer, rather than letting the host
// run flat against the hard limit and refuse everyone.
//
// With MaxConnections zero, go-libp2p's defaults apply: limits auto-scaled
// from system memory and a 160/192 connection manager.
func connectionLimitOptions(cfg *Config, logger *slog.Logger) ([]libp2p.Option, error) {
	maxConns := cfg.MaxConnections
	if maxConns <= 0 {
		logger.Info("connection limits: libp2p defaults (max_connections not set)")
		return nil, nil
	}

	scaled := rcmgr.DefaultLimits.AutoScale()
	system := rcmgr.ResourceLimits{
		Conns:           rcmgr.LimitVal(maxConns),
		ConnsInbound:    rcmgr.LimitVal(maxConns),
		ConnsOutbound:   rcmgr.LimitVal(maxConns),
		Streams:         rcmgr.LimitVal(maxConns * streamsPerConn * 2),
		StreamsInbound:  rcmgr.LimitVal(maxConns * streamsPerConn),
		StreamsOutbound: rcmgr.LimitVal(maxConns * streamsPerConn),
		FD:              rcmgr.LimitVal(maxConns),
	}
	// Transient covers connections still in the handshake, before they are
	// attributed to a peer. A quarter of the cap lets a reconnect storm
	// after a restart get through instead of being refused at the door.
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
	limits := rcmgr.PartialLimitConfig{System: system, Transient: transient}.Build(scaled)

	mgr, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(limits))
	if err != nil {
		return nil, fmt.Errorf("create resource manager: %w", err)
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
		"transient_connections", transientConns,
		"system_streams_inbound", maxConns*streamsPerConn,
		"connmgr_high_water", high,
		"connmgr_low_water", low,
		"connmgr_grace", grace,
	)

	return []libp2p.Option{
		libp2p.ResourceManager(mgr),
		libp2p.ConnectionManager(cm),
	}, nil
}

// DefaultConnManagerGracePeriod is how long a new connection is protected
// from trimming. Long enough for a client to finish its handshake and first
// request; short enough that a reconnect storm does not become untrimmable.
const DefaultConnManagerGracePeriod = 30 * time.Second
