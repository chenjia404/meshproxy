package protocol

// Protocol IDs for circuit, relay and exit services.
const (
	// CircuitProtocolID is used between local node and relay/exit nodes
	// to manage circuits and streams.
	CircuitProtocolID = "/meshproxy/circuit/1.0.0"
)

// MessageType represents the high-level message type for the circuit protocol.
type MessageType uint16

const (
	MsgTypeCreate           MessageType = 1
	MsgTypeCreated          MessageType = 2
	MsgTypeExtend           MessageType = 3
	MsgTypeExtended         MessageType = 4
	MsgTypeBeginTCP         MessageType = 5
	MsgTypeConnected        MessageType = 6
	MsgTypeData             MessageType = 7
	MsgTypeEnd              MessageType = 8
	MsgTypeKeyExchangeInit  MessageType = 9
	MsgTypeKeyExchangeResp  MessageType = 10
	MsgTypeOnionData        MessageType = 11
)

