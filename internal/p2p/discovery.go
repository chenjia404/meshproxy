package p2p

import (
	"context"

	"github.com/chenjia404/meshproxy/internal/safe"
	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	routing "github.com/libp2p/go-libp2p/core/routing"
	routing2 "github.com/libp2p/go-libp2p/p2p/discovery/routing"
)

// StartDiscovery 預留 DHT 節點發現的擴展點。
// 目前版本僅依賴顯式 bootstrap_peers + gossip descriptor，
// 沒有引入額外 discovery 模組，以避免 go-libp2p 版本衝突。
func StartDiscovery(ctx context.Context, h host.Host, router routing.Routing, rendezvous string, onPeerFound func(peer.AddrInfo)) {
	nodeDiscovery(ctx, h, router, rendezvous, onPeerFound)
}

func nodeDiscovery(ctx context.Context, h host.Host, router routing.Routing, protocolTag string, onPeerFound func(peer.AddrInfo)) (error, host.Host, error) {
	if router == nil {
		return nil, h, nil
	}
	discovery := routing2.NewRoutingDiscovery(router)
	var err error

	safe.Go("p2p.discovery.advertiseOnce", func() {
		_, err = discovery.Advertise(ctx, protocolTag)
		if err != nil {
			// log.Println(err)
		}
	})

	safe.Go("p2p.discovery.findPeersLoop", func() {
		for i := 0; i < 10; {
			_, err = discovery.Advertise(ctx, protocolTag)
			if err != nil {
				// log.Println(err)
			}

			peerChan, err := discovery.FindPeers(ctx, protocolTag)
			if err != nil {
				// log.Println(err)
			}

			for peer := range peerChan {
				if peer.ID == h.ID() {
					continue
				}
				if onPeerFound != nil {
					onPeerFound(peer)
				}

				if h.Network().Connectedness(peer.ID) != network.Connected {
					err = h.Connect(ctx, peer)
					if err == nil {
						i++
					}
				}
			}
		}
	})
	return err, h, nil
}
