// Package exit 的 policy 子邏輯：出口策略檢查（端口、域名、peer、私網/回環、drain 模式）。
package exit

import (
	"net"
	"strings"
	"sync"

	"github.com/chenjia404/meshproxy/internal/config"
)

// ExitRejectReason 標準化拒絕原因，供日誌與 API 使用。
type ExitRejectReason string

const (
	ExitRejectDisabled            ExitRejectReason = "exit_disabled"
	ExitRejectDraining            ExitRejectReason = "exit_draining"
	ExitRejectPeerBlacklisted     ExitRejectReason = "peer_blacklisted"
	ExitRejectPeerNotWhitelisted  ExitRejectReason = "peer_not_whitelisted"
	ExitRejectProtocolNotAllowed  ExitRejectReason = "protocol_not_allowed"
	ExitRejectPortDenied          ExitRejectReason = "port_denied"
	ExitRejectPortNotAllowed      ExitRejectReason = "port_not_allowed"
	ExitRejectPrivateTargetDenied ExitRejectReason = "private_target_denied"
	ExitRejectLoopbackDenied      ExitRejectReason = "loopback_target_denied"
	ExitRejectLinkLocalDenied     ExitRejectReason = "link_local_target_denied"
	ExitRejectDomainDenied        ExitRejectReason = "domain_denied"
	ExitRejectDomainNotAllowed    ExitRejectReason = "domain_not_allowed"
)

// PolicyChecker 出口策略檢查器：按文檔順序檢查 peer、協議、端口、IP、域名。
type PolicyChecker struct {
	mu      sync.RWMutex
	policy  config.ExitPolicyConfig
	runtime config.ExitRuntimeConfig
	enabled bool
	// 便於檢查的集合（從 slice 構建）
	deniedPortsSet   map[int]bool
	allowedPortsSet  map[int]bool
	deniedDomainsSet map[string]bool
	allowedDomainsSet map[string]bool
	peerBlacklistSet map[string]bool
	peerWhitelistSet map[string]bool
}

// NewPolicyChecker 從配置創建檢查器。policy 可為 nil 表示不限制（僅用於非 exit 節點）。
func NewPolicyChecker(exitCfg *config.ExitConfig) *PolicyChecker {
	p := &PolicyChecker{
		enabled: true,
		runtime: config.ExitRuntimeConfig{AcceptNewStreams: true},
	}
	if exitCfg != nil {
		p.enabled = exitCfg.Enabled
		p.policy = exitCfg.Policy
		p.runtime = exitCfg.Runtime
	} else {
		p.policy = defaultExitPolicyInConfig()
		p.runtime = config.ExitRuntimeConfig{AcceptNewStreams: true}
	}
	p.rebuildSets()
	return p
}

func defaultExitPolicyInConfig() config.ExitPolicyConfig {
	return config.ExitPolicyConfig{
		AllowTCP: true, AllowUDP: false, RemoteDNS: true,
		AllowedPorts: []int{80, 443}, DeniedPorts: []int{25, 465, 587, 22, 3389},
		AllowPrivateIPTargets: false, AllowLoopbackTargets: false, AllowLinkLocalTargets: false,
	}
}

func (p *PolicyChecker) rebuildSets() {
	p.deniedPortsSet = sliceToSetInt(p.policy.DeniedPorts)
	p.allowedPortsSet = sliceToSetInt(p.policy.AllowedPorts)
	p.deniedDomainsSet = sliceToSetLower(p.policy.DeniedDomains)
	p.allowedDomainsSet = sliceToSetLower(p.policy.AllowedDomains)
	p.peerBlacklistSet = sliceToSet(p.policy.PeerBlacklist)
	p.peerWhitelistSet = sliceToSet(p.policy.PeerWhitelist)
}

func sliceToSetInt(s []int) map[int]bool {
	m := make(map[int]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

func sliceToSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}

func sliceToSetLower(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[strings.ToLower(strings.TrimSpace(v))] = true
	}
	return m
}

// CheckPeerAllowed 檢查 peer 是否允許使用本出口。blacklist 優先於 whitelist。
func (p *PolicyChecker) CheckPeerAllowed(peerID string) (ExitRejectReason, bool) {
	if p == nil {
		return "", true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.peerBlacklistSet[peerID] {
		return ExitRejectPeerBlacklisted, false
	}
	if len(p.policy.PeerWhitelist) > 0 && !p.peerWhitelistSet[peerID] {
		return ExitRejectPeerNotWhitelisted, false
	}
	return "", true
}

// CheckProtocolAllowed 檢查是否允許 TCP（目前僅處理 TCP）。
func (p *PolicyChecker) CheckProtocolAllowed(tcp bool) (ExitRejectReason, bool) {
	if p == nil {
		return "", true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if tcp && !p.policy.AllowTCP {
		return ExitRejectProtocolNotAllowed, false
	}
	if !tcp && !p.policy.AllowUDP {
		return ExitRejectProtocolNotAllowed, false
	}
	return "", true
}

// CheckPortAllowed 檢查端口：先 denied，再 allowed（非空時必須在白名單）。
func (p *PolicyChecker) CheckPortAllowed(port int) (ExitRejectReason, bool) {
	if p == nil {
		return "", true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.deniedPortsSet[port] {
		return ExitRejectPortDenied, false
	}
	if len(p.policy.AllowedPorts) > 0 && !p.allowedPortsSet[port] {
		return ExitRejectPortNotAllowed, false
	}
	return "", true
}

// CheckTargetIPAllowed 檢查目標 IP 是否允許（私網、回環、鏈路本地）。
func (p *PolicyChecker) CheckTargetIPAllowed(ip net.IP) (ExitRejectReason, bool) {
	if p == nil {
		return "", true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	ip4 := ip.To4()
	if ip4 != nil {
		if isPrivateIPv4(ip4) && !p.policy.AllowPrivateIPTargets {
			return ExitRejectPrivateTargetDenied, false
		}
		if isLoopbackIPv4(ip4) && !p.policy.AllowLoopbackTargets {
			return ExitRejectLoopbackDenied, false
		}
		if isLinkLocalIPv4(ip4) && !p.policy.AllowLinkLocalTargets {
			return ExitRejectLinkLocalDenied, false
		}
	} else {
		ip6 := ip.To16()
		if ip6 != nil {
			if isPrivateIPv6(ip6) && !p.policy.AllowPrivateIPTargets {
				return ExitRejectPrivateTargetDenied, false
			}
			if isLoopbackIPv6(ip6) && !p.policy.AllowLoopbackTargets {
				return ExitRejectLoopbackDenied, false
			}
			if isLinkLocalIPv6(ip6) && !p.policy.AllowLinkLocalTargets {
				return ExitRejectLinkLocalDenied, false
			}
		}
	}
	return "", true
}

func isPrivateIPv4(ip net.IP) bool {
	if len(ip) != 4 {
		return false
	}
	return ip[0] == 10 ||
		(ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) ||
		(ip[0] == 192 && ip[1] == 168)
}

func isLoopbackIPv4(ip net.IP) bool {
	return len(ip) == 4 && ip[0] == 127
}

func isLinkLocalIPv4(ip net.IP) bool {
	return len(ip) == 4 && ip[0] == 169 && ip[1] == 254
}

func isPrivateIPv6(ip net.IP) bool {
	if len(ip) != 16 {
		return false
	}
	return ip[0] == 0xfd && ip[1] == 0 || (ip[0] == 0xfc && ip[1] == 0)
}

func isLoopbackIPv6(ip net.IP) bool {
	return len(ip) == 16 && ip[0] == 0 && ip[1] == 0 && ip[2] == 0 && ip[3] == 0 &&
		ip[4] == 0 && ip[5] == 0 && ip[6] == 0 && ip[7] == 0 && ip[8] == 0 && ip[9] == 0 &&
		ip[10] == 0 && ip[11] == 0 && ip[12] == 0 && ip[13] == 0 && ip[14] == 0 && ip[15] == 1
}

func isLinkLocalIPv6(ip net.IP) bool {
	return len(ip) == 16 && ip[0] == 0xfe && ip[1] == 0x80
}

// DomainMatchSuffix 後綴匹配：suffix 為 ".google.com" 時，匹配 "google.com"、"mail.google.com"，不匹配 "google.com.evil.com"。
func DomainMatchSuffix(domain, suffix string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	suffix = strings.ToLower(strings.TrimSpace(suffix))
	if suffix == "" {
		return false
	}
	suffix = strings.TrimPrefix(suffix, ".")
	if domain == suffix {
		return true
	}
	return strings.HasSuffix(domain, "."+suffix)
}

// CheckDomainAllowed 檢查域名：先 denied（精確+後綴），再 allowed（非空時必須在白名單或後綴）。
func (p *PolicyChecker) CheckDomainAllowed(host string) (ExitRejectReason, bool) {
	if p == nil {
		return "", true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	host = strings.ToLower(strings.TrimSpace(host))
	if p.deniedDomainsSet[host] {
		return ExitRejectDomainDenied, false
	}
	for _, suf := range p.policy.DeniedDomainSuffixes {
		if DomainMatchSuffix(host, suf) {
			return ExitRejectDomainDenied, false
		}
	}
	if len(p.policy.AllowedDomains) > 0 || len(p.policy.AllowedDomainSuffixes) > 0 {
		if p.allowedDomainsSet[host] {
			return "", true
		}
		for _, suf := range p.policy.AllowedDomainSuffixes {
			if DomainMatchSuffix(host, suf) {
				return "", true
			}
		}
		return ExitRejectDomainNotAllowed, false
	}
	return "", true
}

// IsEnabled 是否啟用出口。
func (p *PolicyChecker) IsEnabled() bool {
	if p == nil {
		return true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.enabled
}

// AcceptNewStreams 是否接受新 stream（drain 時可關閉）。
func (p *PolicyChecker) AcceptNewStreams() bool {
	if p == nil {
		return true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.runtime.AcceptNewStreams && !p.runtime.DrainMode
}

// DrainMode 是否處於維護模式。
func (p *PolicyChecker) DrainMode() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.runtime.DrainMode
}

// SetRuntime 更新運行時配置（drain_mode / accept_new_streams）。
func (p *PolicyChecker) SetRuntime(r config.ExitRuntimeConfig) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runtime = r
}

// SetPolicy 更新策略配置並重建集合。
func (p *PolicyChecker) SetPolicy(c config.ExitPolicyConfig) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.policy = c
	p.rebuildSets()
}

// GetPolicy 返回當前策略副本。
func (p *PolicyChecker) GetPolicy() config.ExitPolicyConfig {
	if p == nil {
		return config.ExitPolicyConfig{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.policy
}

// GetRuntime 返回當前運行時配置副本。
func (p *PolicyChecker) GetRuntime() config.ExitRuntimeConfig {
	if p == nil {
		return config.ExitRuntimeConfig{}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.runtime
}
