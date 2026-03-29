package app

import (
	"context"
	"sort"
	"strings"

	peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/chenjia404/meshproxy/internal/p2p"
	"github.com/chenjia404/meshproxy/internal/relaycache"
)

// relayPeerSourceFromCache exposes relay cache entries as AutoRelay candidates.
// The source is evaluated on demand so updated relays.json contents can be picked up
// without restarting the process.
func relayPeerSourceFromCache(cache *relaycache.Cache) p2p.PeerSource {
	if cache == nil {
		return nil
	}
	return func(ctx context.Context, num int) <-chan peer.AddrInfo {
		records := cache.Records()
		if len(records) == 0 {
			return nil
		}
		// Prefer the most recently seen relay first so AutoRelay retries the freshest
		// candidates before older, potentially stale entries.
		sort.Slice(records, func(i, j int) bool {
			if records[i].SeenAt == records[j].SeenAt {
				return records[i].PeerID < records[j].PeerID
			}
			return records[i].SeenAt > records[j].SeenAt
		})

		candidates := make([]peer.AddrInfo, 0, len(records))
		for _, rec := range records {
			if rec == nil || rec.PeerID == "" {
				continue
			}
			pid, err := peer.Decode(rec.PeerID)
			if err != nil {
				continue
			}
			addrs := make([]multiaddr.Multiaddr, 0, len(rec.Addrs))
			for _, raw := range rec.Addrs {
				raw = strings.TrimSpace(raw)
				if raw == "" {
					continue
				}
				maddr, err := multiaddr.NewMultiaddr(raw)
				if err != nil {
					continue
				}
				addrs = append(addrs, maddr)
			}
			if len(addrs) == 0 {
				continue
			}
			candidates = append(candidates, peer.AddrInfo{ID: pid, Addrs: addrs})
		}
		if len(candidates) == 0 {
			return nil
		}
		if num <= 0 || num > len(candidates) {
			num = len(candidates)
		}

		ch := make(chan peer.AddrInfo, num)
		go func() {
			defer close(ch)
			for i := 0; i < num; i++ {
				select {
				case <-ctx.Done():
					return
				case ch <- candidates[i]:
				}
			}
		}()
		return ch
	}
}
