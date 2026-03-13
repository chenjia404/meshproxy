package p2p

import (
	"context"
	"fmt"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	host "github.com/libp2p/go-libp2p/core/host"
	routing "github.com/libp2p/go-libp2p/core/routing"
)

// DHT wraps a minimal Kademlia DHT instance.
type DHT struct {
	dht *dht.IpfsDHT
}

// NewDHT creates a new DHT attached to the given host.
func NewDHT(ctx context.Context, h host.Host) (*DHT, error) {
	instance, err := dht.New(ctx, h, dht.Mode(dht.ModeAuto))
	if err != nil {
		return nil, fmt.Errorf("create DHT: %w", err)
	}
	// Bootstrap the DHT so it starts querying and discovering more peers.
	if err := instance.Bootstrap(ctx); err != nil {
		return nil, fmt.Errorf("bootstrap DHT: %w", err)
	}
	return &DHT{dht: instance}, nil
}

// Routing returns the routing interface for integration with libp2p.
func (d *DHT) Routing() routing.Routing {
	return d.dht
}

// Close gracefully shuts down the DHT.
func (d *DHT) Close() error {
	return d.dht.Close()
}

