package relay

import (
	"testing"
	"time"

	"github.com/chenjia404/meshproxy/internal/chatrelay"
	peer "github.com/libp2p/go-libp2p/core/peer"
)

// TestChatRelayV1Table_RateLimitUsesInboundPeer：同一入站 peer 在最小间隔内 connect/heartbeat 转发应被限流（键为 RemotePeer，非 payload src_id）。
func TestChatRelayV1Table_RateLimitUsesInboundPeer(t *testing.T) {
	tbl := NewChatRelayV1Table()
	pid, err := peer.Decode("12D3KooWKEMmQz7wzjVQxhDxFE4xYx8jFS1QKBJMZ4HDV5aK5b3i")
	if err != nil {
		t.Fatal(err)
	}
	inbound := pid.String()

	if !tbl.AllowConnectForward(inbound) {
		t.Fatal("first connect forward should be allowed")
	}
	if tbl.AllowConnectForward(inbound) {
		t.Fatal("second connect within gap should be rate limited")
	}
	time.Sleep(chatRelayV1MinConnectGap + 15*time.Millisecond)
	if !tbl.AllowConnectForward(inbound) {
		t.Fatal("after gap connect should be allowed again")
	}

	if !tbl.AllowHeartbeatForward(inbound) {
		t.Fatal("first heartbeat forward should be allowed")
	}
	if tbl.AllowHeartbeatForward(inbound) {
		t.Fatal("second heartbeat within gap should be rate limited")
	}
	time.Sleep(chatRelayV1MinHeartbeatGap + 15*time.Millisecond)
	if !tbl.AllowHeartbeatForward(inbound) {
		t.Fatal("after gap heartbeat should be allowed again")
	}
}

func TestRelayV1InboundSrcMatchesRemotePeer(t *testing.T) {
	pid, err := peer.Decode("12D3KooWKEMmQz7wzjVQxhDxFE4xYx8jFS1QKBJMZ4HDV5aK5b3i")
	if err != nil {
		t.Fatal(err)
	}
	if !relayV1InboundSrcMatchesRemotePeer(pid, pid.String()) {
		t.Fatal("claimed src_id matching RemotePeer should pass")
	}
	if relayV1InboundSrcMatchesRemotePeer(pid, "QmWrongPeerId") {
		t.Fatal("wrong src_id should fail")
	}
	if relayV1InboundSrcMatchesRemotePeer(pid, "") {
		t.Fatal("empty src_id should fail")
	}
}

func TestRelayV1SigBlockPresent(t *testing.T) {
	if !relayV1SigBlockPresent(chatrelay.SignatureBlock{Algorithm: "ed25519", Value: "YWJj"}) {
		t.Fatal("non-empty algorithm and value should pass")
	}
	if relayV1SigBlockPresent(chatrelay.SignatureBlock{Algorithm: "", Value: "YWJj"}) {
		t.Fatal("empty algorithm should fail")
	}
	if relayV1SigBlockPresent(chatrelay.SignatureBlock{Algorithm: "ed25519", Value: ""}) {
		t.Fatal("empty value should fail")
	}
}
