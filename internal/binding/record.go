package binding

// Algo 常數。
const (
	AlgoLibP2P = "libp2p"
	AlgoEIP191 = "eip191"
)

// SignatureEnvelope 單一簽名包裝。
type SignatureEnvelope struct {
	Algo  string `json:"algo"`
	Value string `json:"value"`
}

// BindingRecord 完整雙簽記錄（持久化 / 同步）。
type BindingRecord struct {
	Payload    BindingPayload `json:"payload"`
	Signatures struct {
		Peer     SignatureEnvelope `json:"peer"`
		Ethereum SignatureEnvelope `json:"ethereum"`
	} `json:"signatures"`
}

// BindingDraft 建立草稿供外部錢包簽 ETH。
type BindingDraft struct {
	Payload           BindingPayload `json:"payload"`
	CanonicalMessage  string         `json:"canonical_message"`
	PeerSignature     string         `json:"peer_signature"`
}
