package p2p

import "github.com/libp2p/go-libp2p/core/protocol"

// Application protocol IDs for meshproxy.
const (
	ProtocolPing         protocol.ID = "/meshproxy/ping/1.0.0"
	ProtocolCtrl         protocol.ID = "/meshproxy/control/1.0.0"
	ProtocolGossip       protocol.ID = "/meshproxy/gossip/1.0.0"
	ProtocolPeerX        protocol.ID = "/meshproxy/peer-exchange/1.0.0"
	ProtocolSocks5       protocol.ID = "/meshproxy/socks5-tunnel/1.0.0"
	ProtocolRawTunnelE2E protocol.ID = "/meshproxy/raw-tunnel-e2e/1.0.0"
)
