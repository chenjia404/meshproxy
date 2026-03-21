package protocol

import "testing"

func TestBuildChatFileChunkNonce(t *testing.T) {
	a := BuildChatFileChunkNonce("msg-1", 0)
	b := BuildChatFileChunkNonce("msg-1", 1)
	c := BuildChatFileChunkNonce("msg-1", 0)

	if len(a) != AEADNonceSize {
		t.Fatalf("nonce length = %d, want %d", len(a), AEADNonceSize)
	}
	if string(a) == string(b) {
		t.Fatalf("nonce should differ for different offsets")
	}
	if string(a) != string(c) {
		t.Fatalf("nonce should be deterministic")
	}
}
