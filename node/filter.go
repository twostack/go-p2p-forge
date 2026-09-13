package node

import (
	"log/slog"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"

	dht "github.com/libp2p/go-libp2p-kad-dht"
)

// dialableFilter admits a peer to the DHT routing table only while this
// host holds a direct connection to it that this host opened.
//
// A routing table entry is a promise to query that peer on its own: bucket
// refreshes open a stream to it and expect a FIND_NODE answer, and when
// the connection drops the DHT dials it again. An inbound connection
// proves nothing about that. The peer that caused this filter was a mobile
// client behind a carrier NAT: it dialled the server, advertised the DHT
// protocol, was admitted, and the next bucket refresh queried it. The
// client tore the connection down, every dial-back (IPv6, circuit relay)
// failed, and the routing table drained to zero. A connection this host
// opened is the one piece of evidence that the peer can be reached when
// the DHT needs it, so that is what admission requires.
//
// The cost: a peer that only ever dialled us stays out until we dial it.
// The DHT dials the peers it learns from lookups, and bootstrap peers are
// dialled at start, so the peers that belong in the table arrive over
// outbound connections anyway.
//
// A relayed connection is refused as well, whichever side opened it. The
// DHT cannot query over one (streams on limited connections are refused
// unless a caller opts in, and the DHT does not), so kad-dht never admits
// such a peer today; the check here is so that this stays true if that
// changes.
func dialableFilter(h host.Host, logger *slog.Logger) dht.RouteTableFilterFunc {
	return func(_ any, p peer.ID) bool {
		conns := h.Network().ConnsToPeer(p)
		for _, c := range conns {
			if c.Stat().Direction == network.DirOutbound && isDirect(c) {
				return true
			}
		}
		logger.Debug("routing table: refusing peer without a direct outbound connection",
			"peer", p, "connections", len(conns))
		return false
	}
}

// isDirect reports whether a connection reaches the peer without a relay.
// A relayed connection is marked limited by the circuit transport and
// carries /p2p-circuit in its remote address; either is enough to refuse it.
func isDirect(c network.Conn) bool {
	if c.Stat().Limited {
		return false
	}
	relayed := false
	ma.ForEach(c.RemoteMultiaddr(), func(comp ma.Component) bool {
		relayed = comp.Protocol().Code == ma.P_CIRCUIT
		return !relayed
	})
	return !relayed
}
