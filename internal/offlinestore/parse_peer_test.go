package offlinestore

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestParseOfflineStorePeerEntry_peerIDOnly(t *testing.T) {
	_, pub, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	n, err := ParseOfflineStorePeerEntry(pid.String())
	if err != nil {
		t.Fatal(err)
	}
	if n.PeerID != pid.String() || len(n.Addrs) != 0 {
		t.Fatalf("got %+v", n)
	}
}

func TestParseOfflineStorePeerEntry_multiaddr(t *testing.T) {
	_, pub, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := peer.IDFromPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	s := "/ip4/127.0.0.1/tcp/4001/p2p/" + pid.String()
	n, err := ParseOfflineStorePeerEntry(s)
	if err != nil {
		t.Fatal(err)
	}
	if n.PeerID != pid.String() || len(n.Addrs) == 0 {
		t.Fatalf("got %+v", n)
	}
}

func TestParseOfflineStorePeerEntry_multiaddrWithoutP2P(t *testing.T) {
	_, err := ParseOfflineStorePeerEntry("/ip4/127.0.0.1/tcp/4001")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseOfflineStorePeerEntry_invalidPeerID(t *testing.T) {
	_, err := ParseOfflineStorePeerEntry("not-a-peer-id")
	if err == nil {
		t.Fatal("expected error")
	}
}
