package offlinestore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	multiaddr "github.com/multiformats/go-multiaddr"
)

// DefaultStoreAddrTTL 寫入 peerstore 的地址記錄 TTL（與 chat ensurePeerConnected 慣例一致）
const DefaultStoreAddrTTL = time.Hour

// ConnectStorePeer 將用戶資料中的 OfflineStoreNode 轉為可達連線：
//   - 若配置了 addrs：peerstore.AddAddrs 後 host.Connect；
//   - 若未配置 addrs：要求 rt 實現 routing.PeerRouting，使用 FindPeer 在網絡上解析地址後再 Connect。
//
// ctx 應帶 timeout（例如 30s）；addrTTL<=0 時使用 DefaultStoreAddrTTL。
func ConnectStorePeer(ctx context.Context, h host.Host, rt routing.Routing, node OfflineStoreNode, addrTTL time.Duration) error {
	if h == nil {
		return errors.New("offlinestore: nil host")
	}
	pid, err := peer.Decode(strings.TrimSpace(node.PeerID))
	if err != nil {
		return fmt.Errorf("offlinestore: peer_id: %w", err)
	}
	if addrTTL <= 0 {
		addrTTL = DefaultStoreAddrTTL
	}

	var maddrs []multiaddr.Multiaddr
	for _, s := range node.Addrs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ma, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			return fmt.Errorf("offlinestore: invalid multiaddr %q: %w", s, err)
		}
		maddrs = append(maddrs, ma)
	}

	if len(maddrs) > 0 {
		h.Peerstore().AddAddrs(pid, maddrs, addrTTL)
		return h.Connect(ctx, peer.AddrInfo{ID: pid, Addrs: maddrs})
	}

	if rt == nil {
		return fmt.Errorf("offlinestore: store %s has no addrs; routing is required for DHT lookup", node.PeerID)
	}
	pr, ok := rt.(routing.PeerRouting)
	if !ok {
		return fmt.Errorf("offlinestore: store %s has no addrs and routing does not implement PeerRouting", node.PeerID)
	}
	info, err := pr.FindPeer(ctx, pid)
	if err != nil {
		return fmt.Errorf("offlinestore: FindPeer %s: %w", node.PeerID, err)
	}
	if len(info.Addrs) == 0 {
		return fmt.Errorf("offlinestore: FindPeer returned no addresses for %s", node.PeerID)
	}
	h.Peerstore().AddAddrs(info.ID, info.Addrs, addrTTL)
	return h.Connect(ctx, info)
}
