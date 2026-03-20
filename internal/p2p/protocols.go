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
	ProtocolChatRequest  protocol.ID = "/meshproxy/chat/request/1.1.0"
	ProtocolChatMsg      protocol.ID = "/meshproxy/chat/msg/1.0.0"
	ProtocolChatAck      protocol.ID = "/meshproxy/chat/ack/1.0.0"
	ProtocolChatSync     protocol.ID = "/meshproxy/chat/sync/1.0.0"
	ProtocolChatPresence protocol.ID = "/meshproxy/chat/presence/1.0.0"
	ProtocolChatRelayE2E protocol.ID = "/meshproxy/chat/relay-e2e/1.0.0"
	ProtocolChatVoiceSig protocol.ID = "/meshproxy/chat/voice-signal/1.0.0"
	ProtocolGroupControl protocol.ID = "/meshproxy/group/control/1.0.0"
	ProtocolGroupMsg     protocol.ID = "/meshproxy/group/msg/1.0.0"
	ProtocolGroupSync    protocol.ID = "/meshproxy/group/sync/1.0.0"
)
