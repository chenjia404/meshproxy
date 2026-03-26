// Package offlinestore 實現與 meshchat-store 節點一致的離線消息 RPC：
// length-prefixed JSON 幀（StreamCodec）、統一協議 /meshchat/offline-store/rpc/1.0.0
//（方法 offline.store / offline.fetch / offline.ack），以及 OfflineStoreNode 與 ConnectStorePeer（僅 peer_id 時經 DHT FindPeer 連線）。
// 不使用 HTTP，僅使用 libp2p stream；每個 RPC 帶默認超時並關閉 stream。
package offlinestore
