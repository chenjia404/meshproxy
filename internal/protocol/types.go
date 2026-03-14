package protocol

// Frame is the unified on-wire structure for all circuit protocol messages.
// Payload is encoded as JSON of one of the message structs below.
type Frame struct {
	Type      MessageType `json:"type"`
	CircuitID string      `json:"circuit_id"`
	StreamID  string      `json:"stream_id"`
	// PayloadJSON is a raw JSON-encoded message specific to the frame type.
	PayloadJSON []byte `json:"payload_json"`
}

// CreateCircuit carries parameters to open a new circuit.
type CreateCircuit struct {
	PathPeerIDs []string `json:"path_peer_ids"`
}

// CreateCircuitResult is returned when a circuit is created.
type CreateCircuitResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Extend carries information to extend an existing circuit to the next peer.
type Extend struct {
	NextPeerID string `json:"next_peer_id"`
}

// ExtendResult is returned when a circuit has been extended.
type ExtendResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// BeginTCP represents a BEGIN_TCP request from client to exit.
type BeginTCP struct {
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
}

// BeginUDP represents a BEGIN_UDP request from client to exit (UDP session to target host:port).
type BeginUDP struct {
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
}

// Connected is sent by the exit when a TCP connection has been established (or failed).
type Connected struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// DataCell carries application data over an existing stream.
type DataCell struct {
	Payload []byte `json:"payload"`
}

// EndCell signals the end of a stream.
type EndCell struct {
	Reason string `json:"reason,omitempty"`
}

// KeyExchangeInit is the JSON representation of a per-hop key exchange
// initiation payload. The concrete cryptographic scheme is intentionally
// left opaque at this layer.
type KeyExchangeInit struct {
	Payload []byte `json:"payload"`
}

// KeyExchangeResp is the JSON representation of a per-hop key exchange
// response payload.
type KeyExchangeResp struct {
	Payload []byte `json:"payload"`
}

// OnionCell wraps an inner relay cell with one or more layers of
// encryption. Each hop removes exactly one layer and forwards the
// remaining ciphertext towards the exit.
type OnionCell struct {
	HopIndex   uint32 `json:"hop_index"`
	Ciphertext []byte `json:"ciphertext"`
}

// OnionInner is a helper structure used for the current plaintext
// onion implementation. Ciphertext carries the JSON-encoded OnionInner,
// which then wraps either a BeginTCP, BeginUDP or DataCell.
type OnionInner struct {
	Kind     string     `json:"kind"`              // "begin" | "begin_udp" | "data"
	Begin    *BeginTCP  `json:"begin,omitempty"`   // present when Kind == "begin"
	BeginUDP *BeginUDP  `json:"begin_udp,omitempty"`
	Data     *DataCell  `json:"data,omitempty"`     // present when Kind == "data"
}

// HopKeys describes the forward and backward keys for a single hop.
type HopKeys struct {
	PeerID      string `json:"peer_id"`
	ForwardKey  []byte `json:"forward_key,omitempty"`
	BackwardKey []byte `json:"backward_key,omitempty"`
}

// CircuitKeys groups all hop keys for a circuit.
type CircuitKeys struct {
	Hops []HopKeys `json:"hops"`
}

// --- 多跳分層加密：HopSession、RelayLayerHeader、OnionPayload、OnionEnvelope ---

// HopRole 表示單跳角色。
type HopRole string

const (
	HopRoleRelay HopRole = "relay"
	HopRoleExit  HopRole = "exit"
)

// HopSession 表示與單一 hop 的會話：X25519 臨時密鑰、共享密鑰、派生出的 forward/backward 密鑰及計數器。
// 密鑰材料不通過 API 輸出。
type HopSession struct {
	PeerID          string    `json:"peer_id"`
	Role            HopRole   `json:"role"`
	EphemeralPub    []byte    `json:"ephemeral_pub,omitempty"`    // 本端臨時公鑰（可對外）
	SharedSecret    []byte    `json:"-"`                         // 不序列化
	ForwardKey      []byte    `json:"-"`
	BackwardKey     []byte    `json:"-"`
	ForwardCounter  uint64    `json:"-"`
	BackwardCounter uint64    `json:"-"`
	CreatedAt       int64     `json:"created_at,omitempty"`
}

// RelayLayerHeader 是 relay 解開一層後可見的局部控制信息（下一跳、內層類型等）。
type RelayLayerHeader struct {
	NextPeerID     string `json:"next_peer_id"`
	InnerPayloadType string `json:"inner_payload_type"` // "onion" | "payload"
	StreamID       string `json:"stream_id,omitempty"`
	Flags          uint8  `json:"flags,omitempty"`
}

// InnerPayloadType 內層業務負載類型。
const (
	InnerPayloadTypeOnion       = "onion"        // 仍是洋蔥密文
	InnerPayloadTypePayload     = "payload"      // 最內層 BEGIN_TCP/DATA/END/CONNECTED 等
	InnerPayloadTypeKeyExchange = "key_exchange" // extend 時轉發給下一跳的 32 字節公鑰
)

// OnionPayload 表示最內層業務命令（exit 解密後得到）。
type OnionPayload struct {
	Kind      string     `json:"kind"` // "begin" | "begin_udp" | "data" | "end" | "connected" | "window_update"
	Begin     *BeginTCP  `json:"begin,omitempty"`
	BeginUDP  *BeginUDP  `json:"begin_udp,omitempty"`
	Data      *DataCell  `json:"data,omitempty"`
	End       *EndCell   `json:"end,omitempty"`
	Connected *Connected `json:"connected,omitempty"`
}

// OnionEnvelope 是單層洋蔥：Relay 解開後得到 Header + InnerCiphertext（或轉發給下一跳）。
type OnionEnvelope struct {
	Header         RelayLayerHeader `json:"header"`
	InnerCiphertext []byte          `json:"inner_ciphertext"`
}

