package chatrelay

import "github.com/libp2p/go-libp2p/core/protocol"

// 《聊天中继.md》V1 協議 ID（length-prefixed JSON）。
const (
	ProtocolRelayConnect   protocol.ID = "/chat/relay/connect/1.0.0"
	ProtocolRelayHandshake protocol.ID = "/chat/relay/handshake/1.0.0"
	ProtocolRelayData      protocol.ID = "/chat/relay/data/1.0.0"
	ProtocolRelayHeartbeat protocol.ID = "/chat/relay/heartbeat/1.0.0"
)
