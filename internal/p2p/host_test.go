package p2p

import (
	"testing"

	"github.com/multiformats/go-multiaddr"
)

func TestRewriteAdvertisedAddrsWithPublicIPv4(t *testing.T) {
	addrs := []multiaddr.Multiaddr{
		multiaddr.StringCast("/ip4/0.0.0.0/tcp/4001"),
		multiaddr.StringCast("/ip6/::/udp/4001/quic-v1"),
		multiaddr.StringCast("/p2p-circuit"),
	}

	got := rewriteAdvertisedAddrs(addrs, "203.0.113.10")
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].String() != "/ip4/203.0.113.10/tcp/4001" {
		t.Fatalf("got[0] = %q, want %q", got[0].String(), "/ip4/203.0.113.10/tcp/4001")
	}
	if got[1].String() != "/p2p-circuit" {
		t.Fatalf("got[1] = %q, want %q", got[1].String(), "/p2p-circuit")
	}
}

func TestRewriteAdvertisedAddrsWithPublicIPv6(t *testing.T) {
	addrs := []multiaddr.Multiaddr{
		multiaddr.StringCast("/ip4/0.0.0.0/tcp/4001"),
		multiaddr.StringCast("/ip6/::/udp/4001/quic-v1"),
	}

	got := rewriteAdvertisedAddrs(addrs, "2001:db8::10")
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].String() != "/ip6/2001:db8::10/udp/4001/quic-v1" {
		t.Fatalf("got[0] = %q, want %q", got[0].String(), "/ip6/2001:db8::10/udp/4001/quic-v1")
	}
}

func TestConnManagerWatermarks(t *testing.T) {
	low, high := connManagerWatermarks(false)
	if low != defaultConnMgrLowWater || high != defaultConnMgrHighWater {
		t.Fatalf("default watermarks = (%d,%d), want (%d,%d)", low, high, defaultConnMgrLowWater, defaultConnMgrHighWater)
	}

	low, high = connManagerWatermarks(true)
	if low != serverModeConnMgrLowWater || high != serverModeConnMgrHighWater {
		t.Fatalf("server mode watermarks = (%d,%d), want (%d,%d)", low, high, serverModeConnMgrLowWater, serverModeConnMgrHighWater)
	}
}
