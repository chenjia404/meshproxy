package p2p

import (
	"context"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	routing "github.com/libp2p/go-libp2p/core/routing"
	routing2 "github.com/libp2p/go-libp2p/p2p/discovery/routing"
)

// StartDiscovery 預留 DHT 節點發現的擴展點。
// 目前版本僅依賴顯式 bootstrap_peers + gossip descriptor，
// 沒有引入額外 discovery 模組，以避免 go-libp2p 版本衝突。
func StartDiscovery(ctx context.Context, h host.Host, _ routing.Routing, rendezvous string) {
	// log.Printf("[discovery] rendezvous discovery not enabled (tag=%s)", rendezvous)
	// _ = time.Second
	nodeDiscovery(ctx, h, rendezvous)

}

var d *dht.IpfsDHT

func nodeDiscovery(ctx context.Context, h host.Host, Protocol string) (error, host.Host, error) {
	err := d.Bootstrap(ctx)
	if err != nil {
		return nil, nil, err
	}
	d1 := routing2.NewRoutingDiscovery(d)

	go func() {
		_, err = d1.Advertise(ctx, Protocol)
		if err != nil {
			// log.Println(err)
		}
	}()

	go func() {

		for i := 0; i < 10; {
			// log.Println("开始寻找节点")
			_, err = d1.Advertise(ctx, Protocol)

			if err != nil {
				//log.Println(err)
			}

			peerChan, err := d1.FindPeers(ctx, Protocol)
			if err != nil {
				//log.Println(err)
			}

			for peer := range peerChan {
				if peer.ID == h.ID() {
					//log.Println("过滤自己")
					continue
				}

				if h.Network().Connectedness(peer.ID) != network.Connected {
					//log.Println("尝试连接:", peer)
					err = h.Connect(ctx, peer)
					if err == nil {
						// log.Println("连接成功", peer.ID)
						// fmt.Printf("当前连接节点数%d\n", len(h.Network().Peers()))
						i++
					} else {
						//log.Println(err)
					}
				}

			}
		}

	}()
	return err, h, nil
}
