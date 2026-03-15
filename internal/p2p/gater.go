package p2p

import (
	"net"

	connmgr "github.com/libp2p/go-libp2p/core/connmgr"
	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// AddrFilterGater blocks connections to/from addresses that should never be
// dialed by a public-facing libp2p node.
type AddrFilterGater struct{}

var _ connmgr.ConnectionGater = (*AddrFilterGater)(nil)

func NewAddrFilterGater() *AddrFilterGater {
	return &AddrFilterGater{}
}

func (g *AddrFilterGater) InterceptPeerDial(peer.ID) bool {
	return true
}

func (g *AddrFilterGater) InterceptAddrDial(_ peer.ID, addr ma.Multiaddr) bool {
	return allowMultiaddr(addr)
}

func (g *AddrFilterGater) InterceptAccept(cma network.ConnMultiaddrs) bool {
	if cma == nil {
		return true
	}
	return allowMultiaddr(cma.RemoteMultiaddr())
}

func (g *AddrFilterGater) InterceptSecured(_ network.Direction, _ peer.ID, cma network.ConnMultiaddrs) bool {
	if cma == nil {
		return true
	}
	return allowMultiaddr(cma.RemoteMultiaddr())
}

func (g *AddrFilterGater) InterceptUpgraded(network.Conn) (bool, control.DisconnectReason) {
	return true, 0
}

func allowMultiaddr(addr ma.Multiaddr) bool {
	if addr == nil {
		return true
	}
	naddr, err := manet.ToNetAddr(addr)
	if err != nil {
		// If the address is not an IP transport (e.g. memory), don't block it here.
		return true
	}
	switch v := naddr.(type) {
	case *net.TCPAddr:
		return allowIP(v.IP)
	case *net.UDPAddr:
		return allowIP(v.IP)
	case interface{ IP() net.IP }:
		return allowIP(v.IP())
	default:
		return true
	}
}

func allowIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if isCGNAT(ip) || isUniqueLocalIPv6(ip) {
		return false
	}
	return true
}

func isCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	_, subnet, _ := net.ParseCIDR("100.64.0.0/10")
	return subnet.Contains(ip4)
}

func isUniqueLocalIPv6(ip net.IP) bool {
	ip16 := ip.To16()
	if ip16 == nil || ip.To4() != nil {
		return false
	}
	return ip16[0]&0xfe == 0xfc
}
