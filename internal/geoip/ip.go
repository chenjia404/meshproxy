package geoip

import (
	"net"
	"strings"

	"github.com/multiformats/go-multiaddr"
)

// FirstPublicIP parses listenAddrs (multiaddr strings) and returns the first
// public IPv4 address suitable for GeoIP lookup. Skips loopback, unspecified,
// and link-local. Returns empty string if none found.
func FirstPublicIP(listenAddrs []string) string {
	var firstIPv4, firstIPv6 string
	for _, s := range listenAddrs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		maddr, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			continue
		}
		ip4Str, err := maddr.ValueForProtocol(multiaddr.P_IP4)
		if err == nil && ip4Str != "" {
			ip := net.ParseIP(ip4Str)
			if ip != nil {
				if isPublicIPv4(ip) {
					return ip.String()
				}
				if firstIPv4 == "" && !isUnspecifiedOrLoopback(ip) {
					firstIPv4 = ip.String()
				}
			}
			continue
		}
		ip6Str, err := maddr.ValueForProtocol(multiaddr.P_IP6)
		if err == nil && ip6Str != "" {
			if firstIPv6 == "" {
				ip := net.ParseIP(ip6Str)
				if ip != nil && isRoutableIPv6(ip) {
					firstIPv6 = ip.String()
				}
			}
		}
	}
	if firstIPv4 != "" {
		return firstIPv4
	}
	return firstIPv6
}

func isUnspecifiedOrLoopback(ip net.IP) bool {
	return ip.IsUnspecified() || ip.IsLoopback()
}

func isPublicIPv4(ip net.IP) bool {
	if ip == nil || ip.To4() == nil {
		return false
	}
	if isUnspecifiedOrLoopback(ip) {
		return false
	}
	// link-local
	if ip[0] == 169 && ip[1] == 254 {
		return false
	}
	return true
}

func isRoutableIPv6(ip net.IP) bool {
	if ip == nil || ip.To4() != nil {
		return false
	}
	if ip.IsUnspecified() || ip.IsLoopback() {
		return false
	}
	// link-local
	if len(ip) >= 2 && ip[0] == 0xfe && (ip[1]&0xc0) == 0x80 {
		return false
	}
	return true
}
