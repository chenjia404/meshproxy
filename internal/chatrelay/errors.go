package chatrelay

// 錯誤碼（《聊天中继.md》十三）—— 用於 API/日誌，與 wire 無關。
const (
	ErrDirectDialFailed      = "DIRECT_DIAL_FAILED"
	ErrRelayNotAvailable     = "RELAY_NOT_AVAILABLE"
	ErrRelayConnectRejected  = "RELAY_CONNECT_REJECTED"
	ErrRelayConnectTimeout   = "RELAY_CONNECT_TIMEOUT"
	ErrRelayUnauthorized     = "RELAY_UNAUTHORIZED"
	ErrRelayRateLimited      = "RELAY_RATE_LIMITED"
	ErrSessionNotFound       = "SESSION_NOT_FOUND"
	ErrSessionExpired        = "SESSION_EXPIRED"
	ErrSessionReplayed       = "SESSION_REPLAYED"
	ErrHandshakeInvalid      = "HANDSHAKE_INVALID"
	ErrHandshakeTimeout      = "HANDSHAKE_TIMEOUT"
	ErrHeartbeatTimeout      = "HEARTBEAT_TIMEOUT"
	ErrHeartbeatInvalid      = "HEARTBEAT_INVALID"
	ErrRelayPayloadTooLarge  = "RELAY_PAYLOAD_TOO_LARGE"
	ErrRouteNotAvailable     = "ROUTE_NOT_AVAILABLE"
)
