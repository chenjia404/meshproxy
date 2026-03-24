package binding

import "testing"

func TestCanonicalMessage_OrderAndNewlines(t *testing.T) {
	p := BindingPayload{
		Version:    1,
		Action:     ActionBindEthAddr,
		PeerID:     "QmTestPeer",
		EthAddress: "0xAbCdef0000000000000000000000000000AbCdEf",
		ChainID:    1,
		Domain:     DomainMeshChat,
		Nonce:      "aabbccdd",
		Seq:        2,
		IssuedAt:   1000,
		ExpireAt:   2000,
	}
	got := CanonicalMessage(p)
	want := "MeshChat Binding v1\n" +
		"action: bind_eth_address\n" +
		"peer_id: QmTestPeer\n" +
		"eth_address: 0xabcdef0000000000000000000000000000abcdef\n" +
		"chain_id: 1\n" +
		"domain: meshchat\n" +
		"nonce: aabbccdd\n" +
		"seq: 2\n" +
		"issued_at: 1000\n" +
		"expire_at: 2000\n"
	if got != want {
		t.Fatalf("canonical mismatch:\n%s\nwant:\n%s", got, want)
	}
}
