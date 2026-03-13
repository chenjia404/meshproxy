package p2p

import (
	"context"
	"fmt"
	"log"

	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/multiformats/go-multiaddr"
)

// Host wraps the libp2p host and its listen addresses.
type Host struct {
	Host        host.Host
	ListenAddrs []multiaddr.Multiaddr
}

// NewHost creates and starts a libp2p host with the given identity and listen addresses.
func NewHost(ctx context.Context, priv crypto.PrivKey, listenAddrs []string) (*Host, error) {
	opts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.UserAgent("meshproxy"),
		libp2p.DefaultTransports,
		libp2p.Security(noise.ID, noise.New),
		// 中繼功能配置
		libp2p.EnableRelay(),             // 啟用中繼功能
		libp2p.EnableNATService(),        // 啟用 NAT 服務
		libp2p.EnableRelayService(),      // 啟用中繼服務
		libp2p.ForceReachabilityPublic(), // 強制設為公網可達

		libp2p.DefaultPeerstore,

		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			var err error
			d, err = dht.New(ctx, h, dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...))
			return d, err
		}),
	}

	for _, addrStr := range listenAddrs {
		maddr, err := multiaddr.NewMultiaddr(addrStr)
		if err != nil {
			return nil, fmt.Errorf("invalid listen multiaddr %q: %w", addrStr, err)
		}
		opts = append(opts, libp2p.ListenAddrs(maddr))
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("create libp2p host: %w", err)
	}

	for _, addr := range h.Addrs() {
		info := peer.AddrInfo{
			ID:    h.ID(),
			Addrs: []multiaddr.Multiaddr{addr},
		}
		for _, a := range info.Addrs {
			log.Printf("[p2p] listening on %s", a.String())
		}
	}

	return &Host{
		Host:        h,
		ListenAddrs: h.Addrs(),
	}, nil
}

// Close shuts down the underlying libp2p host.
func (h *Host) Close() error {
	return h.Host.Close()
}
