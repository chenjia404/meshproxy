package p2p

import (
	"context"
	"log"
	"sync"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

// Bootstrap connects to the configured bootstrap peers in the background.
func Bootstrap(ctx context.Context, h host.Host, peers []string) {
	if len(peers) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, addrStr := range peers {
		addrStr := addrStr
		wg.Add(1)
		go func() {
			defer wg.Done()

			maddr, err := multiaddr.NewMultiaddr(addrStr)
			if err != nil {
				log.Printf("[bootstrap] invalid multiaddr %q: %v", addrStr, err)
				return
			}

			info, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				log.Printf("[bootstrap] parse peer info %q: %v", addrStr, err)
				return
			}

			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			if err := h.Connect(ctx, *info); err != nil {
				log.Printf("[bootstrap] connect to %s failed: %v", addrStr, err)
				return
			}
			log.Printf("[bootstrap] connected to %s", addrStr)
		}()
	}

	go func() {
		wg.Wait()
	}()
}

