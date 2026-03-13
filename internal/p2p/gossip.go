package p2p

import (
	"context"
	"fmt"

	host "github.com/libp2p/go-libp2p/core/host"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// Gossip wraps a minimal pubsub instance used for control and discovery messages.
type Gossip struct {
	ps *pubsub.PubSub
}

// NewGossip creates a new gossip/pubsub instance attached to the host.
func NewGossip(ctx context.Context, h host.Host) (*Gossip, error) {
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("create gossip: %w", err)
	}
	return &Gossip{ps: ps}, nil
}

// PubSub exposes the underlying pubsub instance.
func (g *Gossip) PubSub() *pubsub.PubSub {
	return g.ps
}

