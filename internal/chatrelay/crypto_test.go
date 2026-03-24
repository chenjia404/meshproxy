package chatrelay

import (
	"testing"

	"github.com/chenjia404/meshproxy/internal/protocol"
)

// 协议级回归测试（connect/handshake/heartbeat/限流/重放等）见：
//   - internal/chat/relay_v1_protocol_test.go
//   - internal/relay/chatrelay_v1_test.go

func TestDeriveRelaySessionKeysAndDataRoundTrip(t *testing.T) {
	priv1, pub1, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	priv2, pub2, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	s1, err := protocol.X25519SharedSecret(priv1, pub2)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := protocol.X25519SharedSecret(priv2, pub1)
	if err != nil {
		t.Fatal(err)
	}
	txA, _, err := DeriveRelaySessionKeys(s1, "sess-1", true)
	if err != nil {
		t.Fatal(err)
	}
	_, rxB, err := DeriveRelaySessionKeys(s2, "sess-1", false)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"type":"chat_text"}`)
	nonce, ct, err := SealDataFrame(txA, "sess-1", 0, plain)
	if err != nil {
		t.Fatal(err)
	}
	out, err := OpenDataFrame(rxB, "sess-1", 0, nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(plain) {
		t.Fatalf("roundtrip")
	}
}
