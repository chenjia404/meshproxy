package profile

// 對端綁定校驗狀態（寫入 peers.binding_status）。
const (
	BindingStatusValid          = "valid"
	BindingStatusExpired        = "expired"
	BindingStatusPeerSigInvalid = "peer_sig_invalid"
	BindingStatusEthSigInvalid  = "eth_sig_invalid"
	BindingStatusParseError     = "parse_error"
	BindingStatusStale          = "stale"
)
