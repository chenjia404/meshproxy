package chatrelay

// RouteState 《聊天中继.md》五：連接狀態機。
type RouteState string

const (
	RouteDirectConnected   RouteState = "DirectConnected"
	RouteDirectDialFailed  RouteState = "DirectDialFailed"
	RouteRelayNegotiating  RouteState = "RelayNegotiating"
	RouteRelayHandshake    RouteState = "RelayHandshake"
	RouteRelayConnected    RouteState = "RelayConnected"
	RouteRelayDegraded     RouteState = "RelayDegraded"
	RouteDisconnected      RouteState = "Disconnected"
)

// RouteEntry 本地路由表項（文檔八）。
type RouteEntry struct {
	PeerID            string     `json:"peer_id"`
	RouteType         string     `json:"route_type"` // "direct" | "relay"
	RelayID           string     `json:"relay_id,omitempty"`
	SessionID         string     `json:"session_id,omitempty"`
	State             RouteState `json:"state"`
	RTTMs             int64      `json:"rtt_ms,omitempty"`
	LastHeartbeatUnix int64      `json:"last_heartbeat_at,omitempty"`
	FailCount         int        `json:"fail_count"`
	ExpireAtUnix      int64      `json:"expire_at,omitempty"`
}

// SignatureBlock 文檔 ed25519 簽名。
type SignatureBlock struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"` // base64
}

// RelayConnectRequest 文檔六。
type RelayConnectRequest struct {
	Version   int            `json:"version"`
	SessionID string         `json:"session_id"`
	SrcID     string         `json:"src_id"`
	DstID     string         `json:"dst_id"`
	Timestamp int64          `json:"timestamp"`
	TTLSec    int            `json:"ttl_sec"`
	Signature SignatureBlock `json:"signature"`
}

// RelayConnectResponse 文檔六。
type RelayConnectResponse struct {
	Version   int            `json:"version"`
	SessionID string         `json:"session_id"`
	SrcID     string         `json:"src_id"`
	DstID     string         `json:"dst_id"`
	RelayID   string         `json:"relay_id"`
	Accepted  bool           `json:"accepted"`
	ExpireAt  int64          `json:"expire_at"`
	Signature SignatureBlock `json:"signature"`
}

// RelayHandshakeRequest 文檔六。
type RelayHandshakeRequest struct {
	Version     int            `json:"version"`
	SessionID   string         `json:"session_id"`
	SrcID       string         `json:"src_id"`
	DstID       string         `json:"dst_id"`
	EphPub      string         `json:"eph_pub"` // base64 raw 32B
	CipherSuite string         `json:"cipher_suite"`
	Timestamp   int64          `json:"timestamp"`
	Signature   SignatureBlock `json:"signature"`
}

// RelayHandshakeResponse 文檔六。
type RelayHandshakeResponse struct {
	Version     int            `json:"version"`
	SessionID   string         `json:"session_id"`
	SrcID       string         `json:"src_id"`
	DstID       string         `json:"dst_id"`
	EphPub      string         `json:"eph_pub"`
	CipherSuite string         `json:"cipher_suite"`
	Timestamp   int64          `json:"timestamp"`
	Signature   SignatureBlock `json:"signature"`
}

// RelayDataCipher 文檔六。
type RelayDataCipher struct {
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`      // base64
	Ciphertext string `json:"ciphertext"` // base64
}

// RelayDataFrame 文檔六。
type RelayDataFrame struct {
	Version   int             `json:"version"`
	SessionID string          `json:"session_id"`
	SrcID     string          `json:"src_id"`
	DstID     string          `json:"dst_id"`
	PacketSeq uint64          `json:"packet_seq"`
	Cipher    RelayDataCipher `json:"cipher"`
}

// RelayHeartbeat 文檔六。
type RelayHeartbeat struct {
	Version   int            `json:"version"`
	SessionID string         `json:"session_id"`
	SrcID     string         `json:"src_id"`
	DstID     string         `json:"dst_id"`
	RelayID   string         `json:"relay_id"`
	PingID    string         `json:"ping_id"`
	SentAt    int64          `json:"sent_at"`
	Signature SignatureBlock `json:"signature"`
}

// RelayErrorFrame 中繼或對端返回的錯誤（V1 擴展，JSON 單幀）。
type RelayErrorFrame struct {
	Version int    `json:"version"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}
