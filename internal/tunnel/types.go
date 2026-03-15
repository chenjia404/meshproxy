package tunnel

const (
	FrameTypeClientHello = "client_hello"
	FrameTypeServerHello = "server_hello"
	FrameTypeData        = "data"
	FrameTypeClose       = "close"
	FrameTypePing        = "ping"
	FrameTypePong        = "pong"
)

// RouteHeader is the only routing metadata visible to relays.
type RouteHeader struct {
	Version    int      `json:"version"`
	TunnelID   string   `json:"tunnel_id"`
	Path       []string `json:"path"`
	HopIndex   int      `json:"hop_index"`
	TargetExit string   `json:"target_exit"`
}

type ClientHello struct {
	Type      string `json:"type"`
	TunnelID  string `json:"tunnel_id"`
	PublicKey []byte `json:"public_key"`
	Timestamp int64  `json:"timestamp"`
}

type ServerHello struct {
	Type      string `json:"type"`
	TunnelID  string `json:"tunnel_id"`
	PublicKey []byte `json:"public_key"`
	Timestamp int64  `json:"timestamp"`
}

type EncryptedFrame struct {
	Type       string `json:"type"`
	Seq        uint64 `json:"seq"`
	Ciphertext []byte `json:"ciphertext,omitempty"`
	SentAtUnix int64  `json:"sent_at_unix,omitempty"`
}
