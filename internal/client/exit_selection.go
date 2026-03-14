package client

import (
	"strings"

	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/discovery"
	"github.com/chenjia404/meshproxy/internal/geoip"
)

// exitCountry returns the exit node's country code (from ExitInfo); empty if unknown.
func exitCountry(d *discovery.NodeDescriptor) string {
	if d == nil || d.ExitInfo == nil {
		return ""
	}
	return strings.TrimSpace(strings.ToUpper(d.ExitInfo.Country))
}

// EffectiveCountry returns the country code for exit selection: if the descriptor
// already has ExitInfo.Country, use it; otherwise resolve IP and lookup via the given Resolver.
// peerAddrs: optional multiaddr strings from our peerstore (observed/connected addrs); prefer over descriptor ListenAddrs to get real public IP behind NAT.
// resolver may be nil (no lookup).
func EffectiveCountry(d *discovery.NodeDescriptor, resolver geoip.Resolver, peerAddrs []string) string {
	if d == nil {
		return ""
	}
	if d.ExitInfo != nil && strings.TrimSpace(d.ExitInfo.Country) != "" {
		return strings.TrimSpace(strings.ToUpper(d.ExitInfo.Country))
	}
	if resolver == nil {
		return ""
	}
	// Prefer peerstore/observed addrs (real IP we connect to) over descriptor ListenAddrs (often 0.0.0.0 or LAN).
	ip := ""
	if len(peerAddrs) > 0 {
		ip = geoip.FirstPublicIP(peerAddrs)
	}
	if ip == "" {
		ip = geoip.FirstPublicIP(d.ListenAddrs)
	}
	if ip == "" {
		return ""
	}
	country, err := resolver.Country(ip)
	if err != nil || country == "" {
		return ""
	}
	return strings.TrimSpace(strings.ToUpper(country))
}

// exitAllowsTCP returns true if the exit supports TCP (default true when ExitInfo is nil).
func exitAllowsTCP(d *discovery.NodeDescriptor) bool {
	if d == nil || d.ExitInfo == nil {
		return true
	}
	return d.ExitInfo.AllowTCP
}

// exitRemoteDNS returns true if the exit supports remote DNS.
func exitRemoteDNS(d *discovery.NodeDescriptor) bool {
	if d == nil || d.ExitInfo == nil {
		return false
	}
	return d.ExitInfo.RemoteDNS
}

// countryGetter returns the effective country code for an exit descriptor (e.g. from ExitInfo or GeoIP). Nil means use exitCountry only.
type countryGetter func(*discovery.NodeDescriptor) string

// applyExitSelection filters and sorts exits according to cfg. exclude is merged with cfg.ExcludePeerIDs and cfg.ExcludeCountries.
// If countryFunc is non-nil, it is used for country; otherwise exitCountry (descriptor-only) is used.
// Returns filtered list (may be empty). Caller can retry with fallback (e.g. auto) when fallback_to_any is true.
func applyExitSelection(
	exits []*discovery.NodeDescriptor,
	cfg *config.ExitSelectionConfig,
	localPeerID string,
	allowSelfExit bool,
	excludePeerIDs map[string]bool,
	countryFunc countryGetter,
) []*discovery.NodeDescriptor {
	if cfg == nil {
		return exits
	}
	getCountry := exitCountry
	if countryFunc != nil {
		getCountry = countryFunc
	}
	out := make([]*discovery.NodeDescriptor, 0, len(exits))
	excludeCountries := make(map[string]bool)
	for _, c := range cfg.ExcludeCountries {
		excludeCountries[strings.TrimSpace(strings.ToUpper(c))] = true
	}
	excludePeers := make(map[string]bool)
	for _, id := range cfg.ExcludePeerIDs {
		excludePeers[id] = true
	}
	for k := range excludePeerIDs {
		excludePeers[k] = true
	}
	allowedCountries := make(map[string]bool)
	for _, c := range cfg.AllowedCountries {
		allowedCountries[strings.TrimSpace(strings.ToUpper(c))] = true
	}
	preferredOrder := make([]string, 0, len(cfg.PreferredCountries))
	for _, c := range cfg.PreferredCountries {
		preferredOrder = append(preferredOrder, strings.TrimSpace(strings.ToUpper(c)))
	}

	for _, n := range exits {
		if !allowSelfExit && n.PeerID == localPeerID {
			continue
		}
		if excludePeers[n.PeerID] {
			continue
		}
		country := getCountry(n)
		if excludeCountries[country] {
			continue
		}
		if cfg.RequireTCPSupport && !exitAllowsTCP(n) {
			continue
		}
		if cfg.RequireRemoteDNS && !exitRemoteDNS(n) {
			continue
		}
		switch cfg.Mode {
		case config.ExitSelectionFixedPeer:
			if n.PeerID != cfg.FixedExitPeerID {
				continue
			}
		case config.ExitSelectionCountryOnly:
			if len(allowedCountries) > 0 && !allowedCountries[country] {
				continue
			}
		case config.ExitSelectionCountryPreferred:
			// include all that passed base filters; sort by preferred order below
		case config.ExitSelectionAuto:
			// no extra filter
		default:
			continue
		}
		out = append(out, n)
	}
	if cfg.Mode == config.ExitSelectionCountryPreferred && len(preferredOrder) > 0 {
		// Sort: preferred countries first (by order), then others.
		byCountry := make(map[string][]*discovery.NodeDescriptor)
		for _, n := range out {
			c := getCountry(n)
			if c == "" {
				c = "__"
			}
			byCountry[c] = append(byCountry[c], n)
		}
		out = out[:0]
		for _, c := range preferredOrder {
			out = append(out, byCountry[c]...)
		}
		for _, c := range preferredOrder {
			delete(byCountry, c)
		}
		for _, list := range byCountry {
			out = append(out, list...)
		}
	}
	return out
}
