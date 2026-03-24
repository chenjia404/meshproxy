package binding

// BindingSync 經 ProtocolChatRequest 通道同步綁定（不修改 ProfileSync）。
type BindingSync struct {
	Type       string          `json:"type"`
	FromPeerID string          `json:"from_peer_id"`
	ToPeerID   string          `json:"to_peer_id"`
	Binding    *BindingRecord  `json:"binding,omitempty"`
	SentAtUnix int64           `json:"sent_at_unix"`
	Signature  []byte          `json:"signature,omitempty"`
}
