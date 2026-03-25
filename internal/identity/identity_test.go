package identity

import (
	"path/filepath"
	"testing"

	peer "github.com/libp2p/go-libp2p/core/peer"
)

func TestExportImportPrivateKeyBase58RoundTrip(t *testing.T) {
	t.Parallel()

	path1 := filepath.Join(t.TempDir(), "id-1.pem")
	m1, err := NewManager(path1)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := m1.ExportPrivateKeyBase58()
	if err != nil {
		t.Fatal(err)
	}
	originalPeerID, err := peer.IDFromPrivateKey(m1.PrivateKey())
	if err != nil {
		t.Fatal(err)
	}

	path2 := filepath.Join(t.TempDir(), "id-2.pem")
	m2, err := NewManager(path2)
	if err != nil {
		t.Fatal(err)
	}
	importedPeerID, err := m2.ImportPrivateKeyBase58(exported)
	if err != nil {
		t.Fatal(err)
	}
	if importedPeerID != originalPeerID.String() {
		t.Fatalf("want imported peer id %q, got %q", originalPeerID.String(), importedPeerID)
	}

	reloaded, err := NewManager(path2)
	if err != nil {
		t.Fatal(err)
	}
	reloadedPeerID, err := peer.IDFromPrivateKey(reloaded.PrivateKey())
	if err != nil {
		t.Fatal(err)
	}
	if reloadedPeerID.String() != originalPeerID.String() {
		t.Fatalf("want reloaded peer id %q, got %q", originalPeerID.String(), reloadedPeerID.String())
	}
}
