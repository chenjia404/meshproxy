package client

import (
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/discovery"
	"github.com/chenjia404/meshproxy/internal/protocol"
)

// PathSelector chooses appropriate relay/exit paths based on discovery information and exit selection config.
type PathSelector struct {
	mu           sync.RWMutex
	store        *discovery.Store
	localPeer    string
	routePolicy  RoutePolicy
	exitSelection *config.ExitSelectionConfig
}

// NewPathSelector creates a new PathSelector.
func NewPathSelector(store *discovery.Store, localPeerID string, policy RoutePolicy) *PathSelector {
	return &PathSelector{
		store:       store,
		localPeer:   localPeerID,
		routePolicy:  policy,
		exitSelection: nil,
	}
}

// SetExitSelection sets the exit selection config (nil = auto with defaults). Stores a copy so caller can reuse their struct.
func (p *PathSelector) SetExitSelection(cfg *config.ExitSelectionConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cfg == nil {
		p.exitSelection = nil
		return
	}
	cpy := *cfg
	p.exitSelection = &cpy
}

// GetExitSelection returns a copy of the current exit selection config for API.
func (p *PathSelector) GetExitSelection() config.ExitSelectionConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.exitSelection == nil {
		return config.ExitSelectionConfig{
			Mode:              config.ExitSelectionAuto,
			RequireTCPSupport: true,
			FallbackToAny:     true,
			AllowDirectExit:   true,
		}
	}
	return *p.exitSelection
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
// Exit selection config (country_only, country_preferred, fixed_peer, fallback_to_any, allow_direct_exit) is applied when choosing exits.
func (p *PathSelector) SelectPathForPoolExcluding(kind PoolKind, excludePeerIDs []string) (protocol.PathPlan, error) {
	exclude := make(map[string]bool)
	for _, id := range excludePeerIDs {
		exclude[id] = true
	}
	exits, err := p.filterExitsWithSelection(exclude)
	if err != nil {
		return protocol.PathPlan{}, err
	}
	if len(exits) == 0 {
		return protocol.PathPlan{}, errors.New("no exit nodes available")
	}
	relays := p.filterRelaysExcluding(exclude)

	p.mu.RLock()
	cfg := p.exitSelection
	allowDirect := true
	if cfg != nil {
		allowDirect = cfg.AllowDirectExit
	}
	p.mu.RUnlock()

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

	// If direct exit not allowed, we must have at least one relay.
	if !allowDirect && len(relays) == 0 {
		return protocol.PathPlan{}, errors.New("no relay nodes available (allow_direct_exit is false)")
	}

	switch kind {
	case PoolLowLatency:
		if allowDirect {
			return directExit(exits[0]), nil
		}
		for _, relay := range relays {
			for _, exit := range exits {
				if relay.PeerID == exit.PeerID {
					continue
				}
				return relayExit(relay, exit), nil
			}
		}
		return protocol.PathPlan{}, errors.New("no relay+exit path available")
	case PoolAnonymous, PoolCountry:
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
		if allowDirect {
			return directExit(exits[0]), nil
		}
		return protocol.PathPlan{}, errors.New("no relay+exit path available (allow_direct_exit is false)")
	default:
		plan, err := p.selectRelayExitOrDirect(relays, exits, directExit, relayExit)
		if err != nil {
			return protocol.PathPlan{}, err
		}
		if !allowDirect && len(plan.Hops) == 1 {
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
			return protocol.PathPlan{}, errors.New("no relay+exit path available (allow_direct_exit is false)")
		}
		return plan, nil
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

// filterExitsWithSelection returns exits filtered by exit selection config, with fallback_to_any applied if needed.
func (p *PathSelector) filterExitsWithSelection(exclude map[string]bool) ([]*discovery.NodeDescriptor, error) {
	base := p.filterExitsExcluding(exclude)
	p.mu.RLock()
	cfg := p.exitSelection
	p.mu.RUnlock()

	if cfg == nil {
		return base, nil
	}
	mode := cfg.Mode
	fallback := cfg.FallbackToAny
	candidates := applyExitSelection(base, cfg, p.localPeer, p.routePolicy.AllowSelfExit, exclude)
	if len(candidates) > 0 {
		log.Printf("[path_selector] exit_selected mode=%s count=%d first=%s", mode, len(candidates), candidates[0].PeerID[:12])
		return candidates, nil
	}
	if fallback && mode != config.ExitSelectionAuto {
		// Retry with auto (only base filters)
		fallbackCfg := &config.ExitSelectionConfig{
			Mode:               config.ExitSelectionAuto,
			ExcludeCountries:   cfg.ExcludeCountries,
			ExcludePeerIDs:     cfg.ExcludePeerIDs,
			RequireRemoteDNS:   cfg.RequireRemoteDNS,
			RequireTCPSupport:  cfg.RequireTCPSupport,
			FallbackToAny:      false,
			AllowDirectExit:    cfg.AllowDirectExit,
		}
		candidates = applyExitSelection(base, fallbackCfg, p.localPeer, p.routePolicy.AllowSelfExit, exclude)
		if len(candidates) > 0 {
			log.Printf("[path_selector] exit_selection fallback_to_any used (no match for mode=%s)", mode)
			return candidates, nil
		}
	}
	return nil, fmt.Errorf("no exit nodes match selection (mode=%s, fallback_to_any=%v)", mode, fallback)
}

// ListExitCandidates returns the list of exit nodes that pass the current exit selection config (for API/debug).
func (p *PathSelector) ListExitCandidates() ([]*discovery.NodeDescriptor, error) {
	return p.filterExitsWithSelection(nil)
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

