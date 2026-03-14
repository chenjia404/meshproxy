package app

import (
	"context"
	"log"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	peer "github.com/libp2p/go-libp2p/core/peer"
	routing "github.com/libp2p/go-libp2p/core/routing"

	"github.com/chenjia404/meshproxy/internal/discovery"
	"github.com/chenjia404/meshproxy/internal/geoip"
)

const (
	defaultLearnerInterval = 25 * time.Second
	learnConnectTimeout    = 8 * time.Second
	learnAddrTTL           = 10 * time.Minute
)

// runPeerAddrLearner periodically tries to learn public addresses for exit nodes (via DHT FindPeer + Connect)
// so that GeoIP can resolve country soon after discovery. Exits we already have a public IP for are skipped.
func runPeerAddrLearner(ctx context.Context, h host.Host, r routing.Routing, store *discovery.Store, interval time.Duration) {
	if interval <= 0 {
		interval = defaultLearnerInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			exits := store.ListExits()
			for _, d := range exits {
				if d == nil || d.PeerID == "" {
					continue
				}
				pid, err := peer.Decode(d.PeerID)
				if err != nil {
					continue
				}
				if pid == h.ID() {
					continue
				}
				// Skip if we already have a public IP for this peer (peerstore addrs).
				addrs := h.Peerstore().Addrs(pid)
				addrsStr := make([]string, 0, len(addrs))
				for _, m := range addrs {
					addrsStr = append(addrsStr, m.String())
				}
				if geoip.FirstPublicIP(addrsStr) != "" {
					continue
				}
				// Try DHT FindPeer to get routable addrs, then connect so peerstore gets observed addr.
				learnCtx, cancel := context.WithTimeout(ctx, learnConnectTimeout)
				info, err := r.FindPeer(learnCtx, pid)
				cancel()
				if err != nil || len(info.Addrs) == 0 {
					continue
				}
				h.Peerstore().AddAddrs(pid, info.Addrs, learnAddrTTL)
				learnCtx2, cancel2 := context.WithTimeout(ctx, learnConnectTimeout)
				err = h.Connect(learnCtx2, info)
				cancel2()
				if err != nil {
					log.Printf("[addr_learner] connect to exit %s for GeoIP: %v", d.PeerID[:12], err)
					continue
				}
				log.Printf("[addr_learner] learned addr for exit %s (country lookup ready)", d.PeerID[:12])
			}
		}
	}
}
