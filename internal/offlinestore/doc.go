// Package offlinestore 實現《私聊離線消息（節點側）改造文檔 V1》中的
// length-prefixed JSON 幀編解碼（StreamCodec）與 libp2p store/fetch/ack 三協議客戶端；
// 並提供 OfflineStoreNode 與 ConnectStorePeer（僅 peer_id 時經 DHT FindPeer 連線）。
// 不使用 HTTP，僅使用 libp2p stream；每個 RPC 帶默認超時並關閉 stream。
package offlinestore
