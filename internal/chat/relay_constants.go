package chat

// 与私聊主模型共存的 relay / offline store 元数据（非第二套私聊）。
const (
	TransportKindNative      = "native"
	TransportKindRelayStore  = "relay_store"
	RelayStatusNone          = "none"
	RelayStatusQueued        = "queued"
	RelayStatusRelayed       = "relayed"
	RelayStatusAcked         = "acked"
	RelayStatusFailed        = "failed"
)
