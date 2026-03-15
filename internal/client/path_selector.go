package client

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/discovery"
	"github.com/chenjia404/meshproxy/internal/geoip"
	"github.com/chenjia404/meshproxy/internal/protocol"
)

// PeerAddrsProvider returns known multiaddr strings for a peer (e.g. from peerstore/observed addrs). Used to get real public IP behind NAT.
type PeerAddrsProvider func(peerID string) []string

// PathSelector chooses appropriate relay/exit paths based on discovery information and exit selection config.
type PathSelector struct {
	mu                sync.RWMutex
	store             *discovery.Store
	localPeer         string
	routePolicy       RoutePolicy
	exitSelection     *config.ExitSelectionConfig
	geoIP             geoip.Resolver
	peerAddrsProvider PeerAddrsProvider
	peerHealth        map[string]*peerHealth
}

type peerHealth struct {
	SuccessCount        int
	ConsecutiveFailures int
	LastSuccess         time.Time
	LastFailure         time.Time
	CooldownUntil       time.Time
	SmoothedRTT         time.Duration
}

const (
	peerFailureCooldownBase = 15 * time.Second
	peerFailureCooldownMax  = 4 * time.Minute
)

// NewPathSelector creates a new PathSelector.
func NewPathSelector(store *discovery.Store, localPeerID string, policy RoutePolicy) *PathSelector {
	return &PathSelector{
		store:         store,
		localPeer:     localPeerID,
		routePolicy:   policy,
		exitSelection: nil,
		peerHealth:    make(map[string]*peerHealth),
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

// SetGeoIPResolver sets the resolver used to infer exit country from IP when descriptor has no ExitInfo.Country. Nil = no lookup.
func (p *PathSelector) SetGeoIPResolver(r geoip.Resolver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.geoIP = r
}

// SetPeerAddrsProvider sets the provider for peer addresses (e.g. from peerstore). When set, IP for GeoIP is taken from these addrs first, so NAT nodes get their real public IP.
func (p *PathSelector) SetPeerAddrsProvider(fn PeerAddrsProvider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.peerAddrsProvider = fn
}

// EffectiveCountryForDescriptor returns the country code for the given exit descriptor (from ExitInfo or GeoIP lookup). For API display.
func (p *PathSelector) EffectiveCountryForDescriptor(d *discovery.NodeDescriptor) string {
	p.mu.RLock()
	r := p.geoIP
	provider := p.peerAddrsProvider
	p.mu.RUnlock()
	var peerAddrs []string
	if provider != nil {
		peerAddrs = provider(d.PeerID)
	}
	return EffectiveCountry(d, r, peerAddrs)
}

// CountryForExit implements api.ExitCountryResolver: returns effective country for the descriptor.
func (p *PathSelector) CountryForExit(d *discovery.NodeDescriptor) string {
	return p.EffectiveCountryForDescriptor(d)
}

// ReportPathSuccess increases the preference of hops that participated in a healthy circuit.
func (p *PathSelector) ReportPathSuccess(plan protocol.PathPlan) {
	p.reportPath(plan, true)
}

// ReportPathFailure puts the hops in a short cooldown to avoid immediate re-selection.
func (p *PathSelector) ReportPathFailure(plan protocol.PathPlan) {
	p.reportPath(plan, false)
}

// ReportPathRTT updates smoothed RTT observations for all hops in the path.
func (p *PathSelector) ReportPathRTT(plan protocol.PathPlan, rtt time.Duration) {
	if rtt <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, hop := range plan.Hops {
		if hop.PeerID == "" {
			continue
		}
		st := p.peerHealth[hop.PeerID]
		if st == nil {
			st = &peerHealth{}
			p.peerHealth[hop.PeerID] = st
		}
		if st.SmoothedRTT > 0 {
			st.SmoothedRTT = time.Duration(float64(st.SmoothedRTT)*0.8 + float64(rtt)*0.2)
		} else {
			st.SmoothedRTT = rtt
		}
	}
}

func (p *PathSelector) reportPath(plan protocol.PathPlan, success bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for _, hop := range plan.Hops {
		if hop.PeerID == "" {
			continue
		}
		st := p.peerHealth[hop.PeerID]
		if st == nil {
			st = &peerHealth{}
			p.peerHealth[hop.PeerID] = st
		}
		if success {
			st.SuccessCount++
			st.ConsecutiveFailures = 0
			st.LastSuccess = now
			st.CooldownUntil = time.Time{}
			continue
		}
		st.ConsecutiveFailures++
		st.LastFailure = now
		cooldown := peerFailureCooldownBase
		for i := 1; i < st.ConsecutiveFailures; i++ {
			cooldown *= 2
			if cooldown >= peerFailureCooldownMax {
				cooldown = peerFailureCooldownMax
				break
			}
		}
		st.CooldownUntil = now.Add(cooldown)
	}
}

// SelectBestPath selects a path plan according to policy (direct exit preferred when allowed).
func (p *PathSelector) SelectBestPath() (protocol.PathPlan, error) {
	return p.SelectPathForPool(PoolAnonymous)
}

// SelectPathForPool selects a path plan for the given pool kind.
func (p *PathSelector) SelectPathForPool(kind PoolKind) (protocol.PathPlan, error) {
	return p.SelectPathForPoolExcluding(kind, nil)
}

// SelectRawTunnelPathExcluding selects a path for the raw tunnel mode. It
// prefers relay+exit when available, and only falls back to direct exit when no
// suitable relay exists.
func (p *PathSelector) SelectRawTunnelPathExcluding(excludePeerIDs []string) (protocol.PathPlan, error) {
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

	for _, relay := range relays {
		for _, exit := range exits {
			if relay.PeerID == exit.PeerID {
				continue
			}
			return protocol.PathPlan{
				Hops: []protocol.PathHop{
					{PeerID: relay.PeerID, IsRelay: true, IsExit: false},
					{PeerID: exit.PeerID, IsRelay: false, IsExit: true},
				},
				ExitHopIndex: 1,
			}, nil
		}
	}
	if allowDirect {
		return protocol.PathPlan{
			Hops:         []protocol.PathHop{{PeerID: exits[0].PeerID, IsRelay: false, IsExit: true}},
			ExitHopIndex: 0,
		}, nil
	}
	return protocol.PathPlan{}, errors.New("no relay+exit path available")
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
		if allowDirect {
			return directExit(exits[0]), nil
		}
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
	if len(exits) > 0 {
		return directExit(exits[0]), nil
	}
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
	return protocol.PathPlan{}, errors.New("no exit nodes available")
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
	var countryFunc countryGetter
	if p.geoIP != nil || p.peerAddrsProvider != nil {
		resolver := p.geoIP
		provider := p.peerAddrsProvider
		countryFunc = func(d *discovery.NodeDescriptor) string {
			var peerAddrs []string
			if provider != nil {
				peerAddrs = provider(d.PeerID)
			}
			return EffectiveCountry(d, resolver, peerAddrs)
		}
	}
	candidates := applyExitSelection(base, cfg, p.localPeer, p.routePolicy.AllowSelfExit, exclude, countryFunc)
	if len(candidates) > 0 {
		log.Printf("[path_selector] exit_selected mode=%s count=%d first=%s", mode, len(candidates), candidates[0].PeerID[:12])
		return candidates, nil
	}
	if fallback && mode != config.ExitSelectionAuto {
		// Retry with auto (only base filters)
		fallbackCfg := &config.ExitSelectionConfig{
			Mode:              config.ExitSelectionAuto,
			ExcludeCountries:  cfg.ExcludeCountries,
			ExcludePeerIDs:    cfg.ExcludePeerIDs,
			RequireRemoteDNS:  cfg.RequireRemoteDNS,
			RequireTCPSupport: cfg.RequireTCPSupport,
			FallbackToAny:     false,
			AllowDirectExit:   cfg.AllowDirectExit,
		}
		candidates = applyExitSelection(base, fallbackCfg, p.localPeer, p.routePolicy.AllowSelfExit, exclude, countryFunc)
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
	return p.rankNodes(out)
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
	return p.rankNodes(out)
}

func (p *PathSelector) rankNodes(nodes []*discovery.NodeDescriptor) []*discovery.NodeDescriptor {
	if len(nodes) <= 1 {
		return nodes
	}
	p.mu.RLock()
	now := time.Now()
	health := make(map[string]peerHealth, len(nodes))
	available := make([]*discovery.NodeDescriptor, 0, len(nodes))
	cooled := make([]*discovery.NodeDescriptor, 0, len(nodes))
	for _, n := range nodes {
		if st := p.peerHealth[n.PeerID]; st != nil {
			health[n.PeerID] = *st
			if st.CooldownUntil.After(now) {
				cooled = append(cooled, n)
				continue
			}
		}
		available = append(available, n)
	}
	p.mu.RUnlock()
	sort.SliceStable(available, func(i, j int) bool {
		return betterNode(available[i], available[j], health)
	})
	sort.SliceStable(cooled, func(i, j int) bool {
		return betterNode(cooled[i], cooled[j], health)
	})
	if len(available) > 0 {
		return append(available, cooled...)
	}
	return cooled
}

func betterNode(a, b *discovery.NodeDescriptor, health map[string]peerHealth) bool {
	if a == nil || b == nil {
		return a != nil
	}
	ha, oka := health[a.PeerID]
	hb, okb := health[b.PeerID]
	if oka && okb {
		if ha.SmoothedRTT > 0 && hb.SmoothedRTT > 0 && ha.SmoothedRTT != hb.SmoothedRTT {
			return ha.SmoothedRTT < hb.SmoothedRTT
		}
		if ha.SmoothedRTT > 0 && hb.SmoothedRTT == 0 {
			return true
		}
		if ha.SmoothedRTT == 0 && hb.SmoothedRTT > 0 {
			return false
		}
	}
	if oka && !okb {
		return true
	}
	if !oka && okb {
		return false
	}
	if ha.SuccessCount != hb.SuccessCount {
		return ha.SuccessCount > hb.SuccessCount
	}
	if ha.ConsecutiveFailures != hb.ConsecutiveFailures {
		return ha.ConsecutiveFailures < hb.ConsecutiveFailures
	}
	if !ha.LastSuccess.Equal(hb.LastSuccess) {
		return ha.LastSuccess.After(hb.LastSuccess)
	}
	return a.PeerID < b.PeerID
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
