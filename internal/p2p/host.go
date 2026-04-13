package p2p

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/routing"
	autorelay "github.com/libp2p/go-libp2p/p2p/host/autorelay"
	connmgr "github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	libp2ptls "github.com/libp2p/go-libp2p/p2p/security/tls"
	"github.com/multiformats/go-multiaddr"
)

// Host wraps the libp2p host and its listen addresses.
type Host struct {
	Host        host.Host
	Routing     routing.Routing
	ListenAddrs []multiaddr.Multiaddr
}

// PeerSource supplies relay candidates to libp2p AutoRelay.
type PeerSource = autorelay.PeerSource

const (
	defaultConnMgrLowWater     = 50
	defaultConnMgrHighWater    = 400
	serverModeConnMgrLowWater  = 30
	serverModeConnMgrHighWater = 50
)

// NewHost creates and starts a libp2p host with the given identity and listen addresses.
func NewHost(ctx context.Context, priv crypto.PrivKey, listenAddrs []string, relayPeerSource PeerSource, publicIP string, serverMode bool) (*Host, error) {
	var hostRouting routing.Routing

	lowWater, highWater := connManagerWatermarks(serverMode)
	connmgr_, _ := connmgr.NewConnManager(
		lowWater,
		highWater,
		connmgr.WithGracePeriod(time.Minute),
	)

	opts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.UserAgent("meshproxy"),
		libp2p.ConnectionGater(NewAddrFilterGater()),

		//尝试开启upnp协议
		libp2p.NATPortMap(),
		libp2p.EnableNATService(),

		libp2p.DefaultTransports,
		libp2p.Security(libp2ptls.ID, libp2ptls.New),
		libp2p.Security(noise.ID, noise.New),
		// 中繼功能配置
		libp2p.EnableRelay(),        // 啟用中繼功能
		libp2p.EnableRelayService(), // 啟用中繼服務

		libp2p.DefaultPeerstore,

		libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			r, err := dht.New(ctx, h, dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...))
			if err == nil {
				hostRouting = r
			}
			return r, err
		}),
		libp2p.ConnectionManager(connmgr_),
	}

	if relayPeerSource != nil {
		// AutoRelay reads candidates from relays.json via the caller-provided peer source.
		// This keeps relay discovery aligned with the actual relay cache rather than bootstrap peers.
		opts = append(opts, libp2p.EnableAutoRelayWithPeerSource(relayPeerSource))
	}

	if publicIP != "" {
		// When the public IP is known upfront, advertise only rewritten public addresses
		// and skip identify-based address discovery to avoid leaking private addrs.
		opts = append(opts,
			libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
				return rewriteAdvertisedAddrs(addrs, publicIP)
			}),
			libp2p.DisableIdentifyAddressDiscovery(),
		)
	} else {
		// 过滤对外宣告地址
		opts = append(opts,
			libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
				out := make([]multiaddr.Multiaddr, 0, len(addrs))
				for _, a := range addrs {
					s := a.String()
					// 按你的场景过滤 Docker/私网/链路本地地址
					if strings.Contains(s, "/ip4/127.") ||
						strings.Contains(s, "/ip4/10.") ||
						strings.Contains(s, "/ip4/172.16.") ||
						strings.Contains(s, "/ip4/172.17.") ||
						strings.Contains(s, "/ip4/172.18.") ||
						strings.Contains(s, "/ip4/172.19.") ||
						strings.Contains(s, "/ip4/192.168.") {
						continue
					}
					out = append(out, a)
				}

				// 也可以直接只返回你手工指定的公网/域名地址
				// return []ma.Multiaddr{mustMA("/dns4/example.com/tcp/4001")}
				return out
			}),
		)
	}

	for _, addrStr := range listenAddrs {
		// 先打印配置中的监听地址，便于区分容器内绑定地址和对外广播地址。
		log.Printf("[p2p] listen configured: %s", addrStr)
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

	advertisedAddrs := h.Addrs()
	for _, addr := range advertisedAddrs {
		log.Printf("[p2p] advertised addr: %s", addr.String())
	}

	return &Host{
		Host:        h,
		Routing:     hostRouting,
		ListenAddrs: advertisedAddrs,
	}, nil
}

func connManagerWatermarks(serverMode bool) (lowWater, highWater int) {
	if serverMode {
		return serverModeConnMgrLowWater, serverModeConnMgrHighWater
	}
	return defaultConnMgrLowWater, defaultConnMgrHighWater
}

// Close shuts down the underlying libp2p host.
func (h *Host) Close() error {
	return h.Host.Close()
}

// rewriteAdvertisedAddrs rewrites the advertised multiaddrs to use the configured public IP.
// Private listen addresses such as 0.0.0.0/:: are mapped to the public IP while preserving the
// rest of the transport suffix. Addrs with a different IP family are dropped.
func rewriteAdvertisedAddrs(addrs []multiaddr.Multiaddr, publicIP string) []multiaddr.Multiaddr {
	ip := net.ParseIP(strings.TrimSpace(publicIP))
	if ip == nil {
		return addrs
	}

	out := make([]multiaddr.Multiaddr, 0, len(addrs))
	seen := make(map[string]struct{}, len(addrs))
	for _, addr := range addrs {
		rewritten, ok := rewriteAdvertisedAddr(addr, ip)
		if !ok {
			continue
		}
		key := rewritten.String()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, rewritten)
	}
	return out
}

// rewriteAdvertisedAddr rewrites one listen addr into its public counterpart.
// If the addr has an IP component with a different family than publicIP, it is dropped.
func rewriteAdvertisedAddr(addr multiaddr.Multiaddr, publicIP net.IP) (multiaddr.Multiaddr, bool) {
	if addr == nil {
		return nil, false
	}

	if ip4, err := addr.ValueForProtocol(multiaddr.P_IP4); err == nil {
		if publicIP.To4() == nil {
			return nil, false
		}
		oldComp := "/ip4/" + ip4
		newComp := "/ip4/" + publicIP.To4().String()
		if oldComp == newComp {
			return addr, true
		}
		rewritten, err := multiaddr.NewMultiaddr(strings.Replace(addr.String(), oldComp, newComp, 1))
		if err != nil {
			return nil, false
		}
		return rewritten, true
	}

	if ip6, err := addr.ValueForProtocol(multiaddr.P_IP6); err == nil {
		if publicIP.To4() != nil {
			return nil, false
		}
		oldComp := "/ip6/" + ip6
		newComp := "/ip6/" + publicIP.String()
		if oldComp == newComp {
			return addr, true
		}
		rewritten, err := multiaddr.NewMultiaddr(strings.Replace(addr.String(), oldComp, newComp, 1))
		if err != nil {
			return nil, false
		}
		return rewritten, true
	}

	// Addresses without an IP component are kept unchanged.
	return addr, true
}
