package offlinestore

import (
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// ParseOfflineStorePeerEntry 解析單條配置（與 p2p.bootstrap_peers 條目相同語法）：
//   - 以 '/' 開頭 → 按 multiaddr 解析，且須含 /p2p/<peer_id>；得到 peer_id 與可撥號地址。
//   - 否則 → 按 peer_id 字串解析（無地址，由 ConnectStorePeer 走 DHT FindPeer）。
func ParseOfflineStorePeerEntry(s string) (OfflineStoreNode, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return OfflineStoreNode{}, fmt.Errorf("empty offline store peer entry")
	}
	if strings.HasPrefix(s, "/") {
		ma, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			return OfflineStoreNode{}, fmt.Errorf("offline store multiaddr: %w", err)
		}
		if _, err := ma.ValueForProtocol(multiaddr.P_P2P); err != nil {
			return OfflineStoreNode{}, fmt.Errorf("offline store multiaddr must include /p2p/<peer_id>")
		}
		info, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			return OfflineStoreNode{}, fmt.Errorf("offline store multiaddr: %w", err)
		}
		addrs := make([]string, len(info.Addrs))
		for i, a := range info.Addrs {
			addrs[i] = a.String()
		}
		return OfflineStoreNode{PeerID: info.ID.String(), Addrs: addrs}, nil
	}
	pid, err := peer.Decode(s)
	if err != nil {
		return OfflineStoreNode{}, fmt.Errorf("offline store peer_id: %w", err)
	}
	return OfflineStoreNode{PeerID: pid.String()}, nil
}
