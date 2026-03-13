package client

import (
	"errors"
	"fmt"

	"meshproxy/internal/discovery"
	"meshproxy/internal/protocol"
)

// PathSelector chooses appropriate relay/exit paths based on discovery information.
type PathSelector struct {
	store      *discovery.Store
	localPeer  string
	routePolicy RoutePolicy
}

// NewPathSelector creates a new PathSelector.
func NewPathSelector(store *discovery.Store, localPeerID string, policy RoutePolicy) *PathSelector {
	return &PathSelector{
		store:      store,
		localPeer:  localPeerID,
		routePolicy: policy,
	}
}

// SelectBestPath selects a path plan according to policy (relay+exit preferred).
func (p *PathSelector) SelectBestPath() (protocol.PathPlan, error) {
	return p.SelectPathForPool(PoolAnonymous)
}

// SelectPathForPool selects a path plan for the given pool kind.
func (p *PathSelector) SelectPathForPool(kind PoolKind) (protocol.PathPlan, error) {
	return p.SelectPathForPoolExcluding(kind, nil)
}

// SelectPathForPoolExcluding selects a path plan excluding the given peer IDs (for relay/exit failover).
func (p *PathSelector) SelectPathForPoolExcluding(kind PoolKind, excludePeerIDs []string) (protocol.PathPlan, error) {
	exclude := make(map[string]bool)
	for _, id := range excludePeerIDs {
		exclude[id] = true
	}
	exits := p.filterExitsExcluding(exclude)
	if len(exits) == 0 {
		return protocol.PathPlan{}, errors.New("no exit nodes available")
	}
	relays := p.filterRelaysExcluding(exclude)

	directExit := func(exit *discovery.NodeDescriptor) protocol.PathPlan {
		hops := []protocol.PathHop{
			{PeerID: exit.PeerID, IsRelay: false, IsExit: true},
		}
		return protocol.PathPlan{Hops: hops, ExitHopIndex: 0}
	}

	relayExit := func(relay, exit *discovery.NodeDescriptor) protocol.PathPlan {
		hops := []protocol.PathHop{
			{PeerID: relay.PeerID, IsRelay: true, IsExit: false},
			{PeerID: exit.PeerID, IsRelay: false, IsExit: true},
		}
		return protocol.PathPlan{Hops: hops, ExitHopIndex: 1}
	}

	switch kind {
	case PoolLowLatency:
		// Prefer direct exit for lowest latency.
		return directExit(exits[0]), nil
	case PoolAnonymous, PoolCountry:
		// Prefer relay+exit for anonymity (country uses same logic until we have geo metadata).
		if len(relays) > 0 {
			for _, relay := range relays {
				for _, exit := range exits {
					if relay.PeerID == exit.PeerID {
						continue
					}
					return relayExit(relay, exit), nil
				}
			}
		}
		return directExit(exits[0]), nil
	default:
		return p.selectRelayExitOrDirect(relays, exits, directExit, relayExit)
	}
}

func (p *PathSelector) selectRelayExitOrDirect(
	relays, exits []*discovery.NodeDescriptor,
	directExit func(*discovery.NodeDescriptor) protocol.PathPlan,
	relayExit func(*discovery.NodeDescriptor, *discovery.NodeDescriptor) protocol.PathPlan,
) (protocol.PathPlan, error) {
	if len(relays) > 0 {
		for _, relay := range relays {
			for _, exit := range exits {
				if relay.PeerID == exit.PeerID {
					continue
				}
				return relayExit(relay, exit), nil
			}
		}
	}
	return directExit(exits[0]), nil
}

func (p *PathSelector) filterRelays() []*discovery.NodeDescriptor {
	return p.filterRelaysExcluding(nil)
}

func (p *PathSelector) filterRelaysExcluding(exclude map[string]bool) []*discovery.NodeDescriptor {
	all := p.store.ListRelays()
	out := make([]*discovery.NodeDescriptor, 0, len(all))
	for _, n := range all {
		if n.PeerID == p.localPeer || exclude != nil && exclude[n.PeerID] {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (p *PathSelector) filterExits() []*discovery.NodeDescriptor {
	return p.filterExitsExcluding(nil)
}

func (p *PathSelector) filterExitsExcluding(exclude map[string]bool) []*discovery.NodeDescriptor {
	all := p.store.ListExits()
	out := make([]*discovery.NodeDescriptor, 0, len(all))
	for _, n := range all {
		if !p.routePolicy.AllowSelfExit && n.PeerID == p.localPeer {
			continue
		}
		if exclude != nil && exclude[n.PeerID] {
			continue
		}
		out = append(out, n)
	}
	return out
}

// DebugString returns a human-readable description of a path plan.
func DebugString(plan protocol.PathPlan) string {
	result := ""
	for i, hop := range plan.Hops {
		role := "relay"
		if hop.IsExit {
			role = "exit"
		}
		if i > 0 {
			result += " -> "
		}
		result += fmt.Sprintf("%s(%s)", hop.PeerID, role)
	}
	return result
}

