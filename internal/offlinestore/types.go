package offlinestore

import (
	"encoding/json"
	"errors"

	"github.com/libp2p/go-libp2p/core/protocol"
)

// ErrRPCLayer 表示服務端在 RPC 包絡層返回的錯誤（未知 method、非法請求等），body 為 RPCErrorBody 形狀。
var ErrRPCLayer = errors.New("offlinestore: rpc layer error")

// ProtocolRPC 與 meshchat-store 節點統一協議（單流多方法 RPC）。
const ProtocolRPC protocol.ID = "/meshchat/offline-store/rpc/1.0.0"

// 與 store-node internal/protocol 一致的方法名。
const (
	MethodOfflineStore = "offline.store"
	MethodOfflineFetch = "offline.fetch"
	MethodOfflineAck   = "offline.ack"
)

// RPCRequest 單一流上的請求：body 為 StoreRequest / FetchRequest / AckRequest JSON。
type RPCRequest struct {
	RequestID string          `json:"request_id"`
	Method    string          `json:"method"`
	Body      json.RawMessage `json:"body"`
}

// RPCResponse 單一流上的響應：body 為 StoreResponse / FetchResponse / AckResponse，或 RPC 層錯誤形狀。
type RPCResponse struct {
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error"`
	Body      json.RawMessage `json:"body"`
}

// RPCErrorBody RPC 層錯誤（非法請求、未知 method 等）時 body 的固定形狀。
type RPCErrorBody struct {
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	Method       string `json:"method,omitempty"`
}

// OfflineStoreNode 用戶資料或節點配置（chat.offline_store_peers 解析結果）中的單個 store。
// Addrs 可省略：僅填 peer_id 時由 ConnectStorePeer 經 DHT FindPeer 查址後連線。
type OfflineStoreNode struct {
	PeerID string   `json:"peer_id" yaml:"peer_id"`
	Addrs  []string `json:"addrs,omitempty" yaml:"addrs,omitempty"`
}

// StoreMessageRequest 對應業務 StoreRequest（文檔 7.3）
type StoreMessageRequest struct {
	Version int             `json:"version"`
	Message json.RawMessage `json:"message"`
}

// StoreMessageResponse 對應業務 StoreResponse（文檔 7.4）
type StoreMessageResponse struct {
	OK           bool   `json:"ok"`
	Duplicate    bool   `json:"duplicate"`
	StoreSeq     int64  `json:"store_seq"`
	ExpireAt     int64  `json:"expire_at"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// FetchMessagesRequest 對應業務 FetchRequest（文檔 8.3）
type FetchMessagesRequest struct {
	Version     int    `json:"version"`
	RecipientID string `json:"recipient_id"`
	AfterSeq    int64  `json:"after_seq"`
	Limit       int    `json:"limit"`
}

// FetchMessagesResponse 對應業務 FetchResponse（文檔 8.4）
type FetchMessagesResponse struct {
	OK           bool              `json:"ok"`
	Items        []json.RawMessage `json:"items"`
	HasMore      bool              `json:"has_more"`
	ErrorCode    string            `json:"error_code"`
	ErrorMessage string            `json:"error_message,omitempty"`
}

// AckMessagesRequest 對應業務 AckRequest（文檔 9.2）
type AckMessagesRequest struct {
	Version     int             `json:"version"`
	RecipientID string          `json:"recipient_id"`
	DeviceID    string          `json:"device_id"`
	AckSeq      int64           `json:"ack_seq"`
	AckedAt     int64           `json:"acked_at"`
	Signature   json.RawMessage `json:"signature"`
}

// AckMessagesResponse 對應業務 AckResponse（文檔 9.3）
type AckMessagesResponse struct {
	OK               bool   `json:"ok"`
	DeletedUntilSeq  int64  `json:"deleted_until_seq"`
	ErrorCode        string `json:"error_code"`
	ErrorMessage     string `json:"error_message,omitempty"`
}
