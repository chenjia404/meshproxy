package offlinestore

import (
	"encoding/json"

	"github.com/libp2p/go-libp2p/core/protocol"
)

// 協議 ID（與《私聊離線消息（節點側）改造文檔 V1》一致，不得改動）
const (
	ProtocolStore protocol.ID = "/chat/offline/store/1.0.0"
	ProtocolFetch protocol.ID = "/chat/offline/fetch/1.0.0"
	ProtocolAck   protocol.ID = "/chat/offline/ack/1.0.0"
)

// OfflineStoreNode 用戶資料或節點配置（chat.offline_store_peers 解析結果）中的單個 store。
// Addrs 可省略：僅填 peer_id 時由 ConnectStorePeer 經 DHT FindPeer 查址後連線。
type OfflineStoreNode struct {
	PeerID string   `json:"peer_id" yaml:"peer_id"`
	Addrs  []string `json:"addrs,omitempty" yaml:"addrs,omitempty"`
}

// StoreMessageRequest 對應文檔 7.3
type StoreMessageRequest struct {
	Version int             `json:"version"`
	Message json.RawMessage `json:"message"`
}

// StoreMessageResponse 對應文檔 7.4
type StoreMessageResponse struct {
	OK         bool   `json:"ok"`
	Duplicate  bool   `json:"duplicate"`
	StoreSeq   int64  `json:"store_seq"`
	ExpireAt   int64  `json:"expire_at"`
	ErrorCode  string `json:"error_code"`
}

// FetchMessagesRequest 對應文檔 8.3
type FetchMessagesRequest struct {
	Version      int    `json:"version"`
	RecipientID  string `json:"recipient_id"`
	AfterSeq     int64  `json:"after_seq"`
	Limit        int    `json:"limit"`
}

// FetchMessagesResponse 對應文檔 8.4
type FetchMessagesResponse struct {
	OK        bool              `json:"ok"`
	Items     []json.RawMessage `json:"items"`
	HasMore   bool              `json:"has_more"`
	ErrorCode string            `json:"error_code"`
}

// AckMessagesRequest 對應文檔 9.2
type AckMessagesRequest struct {
	Version     int             `json:"version"`
	RecipientID string          `json:"recipient_id"`
	DeviceID    string          `json:"device_id"`
	AckSeq      int64           `json:"ack_seq"`
	AckedAt     int64           `json:"acked_at"`
	Signature   json.RawMessage `json:"signature"`
}

// AckMessagesResponse 對應文檔 9.3
type AckMessagesResponse struct {
	OK               bool  `json:"ok"`
	DeletedUntilSeq  int64 `json:"deleted_until_seq"`
}
