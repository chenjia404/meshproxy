package client

import (
	"strings"

	"meshproxy/internal/config"
	"meshproxy/internal/discovery"
)

// exitCountry returns the exit node's country code (from ExitInfo); empty if unknown.
func exitCountry(d *discovery.NodeDescriptor) string {
	if d == nil || d.ExitInfo == nil {
		return ""
	}
	return strings.TrimSpace(strings.ToUpper(d.ExitInfo.Country))
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

// applyExitSelection filters and sorts exits according to cfg. exclude is merged with cfg.ExcludePeerIDs and cfg.ExcludeCountries.
// Returns filtered list (may be empty). Caller can retry with fallback (e.g. auto) when fallback_to_any is true.
func applyExitSelection(
	exits []*discovery.NodeDescriptor,
	cfg *config.ExitSelectionConfig,
	localPeerID string,
	allowSelfExit bool,
	excludePeerIDs map[string]bool,
) []*discovery.NodeDescriptor {
	if cfg == nil {
		return exits
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
		country := exitCountry(n)
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
			c := exitCountry(n)
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
