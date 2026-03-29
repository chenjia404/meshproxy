package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenjia404/meshproxy/internal/relaycache"
)

func TestRelayPeerSourcePrefersMostRecentSeenAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relays.json")
	data := []byte(`[
  {
    "peer_id": "12D3KooWMxVFQZVQwnPqVF4Leosp1qewEtCzYqnEgizSAi33x3EK",
    "addrs": ["/ip4/1.2.3.4/tcp/4001/p2p/12D3KooWMxVFQZVQwnPqVF4Leosp1qewEtCzYqnEgizSAi33x3EK"],
    "seen_at": 100
  },
  {
    "peer_id": "QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
    "addrs": ["/ip4/5.6.7.8/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ"],
    "seen_at": 200
  }
]`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cache, err := relaycache.New(path)
	if err != nil {
		t.Fatalf("relaycache.New() error = %v", err)
	}

	source := relayPeerSourceFromCache(cache)
	if source == nil {
		t.Fatal("relayPeerSourceFromCache() = nil, want non-nil")
	}

	ch := source(context.Background(), 2)
	first := <-ch
	second := <-ch

	if first.ID.String() != "QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ" {
		t.Fatalf("first relay = %s, want most recent relay", first.ID)
	}
	if second.ID.String() != "12D3KooWMxVFQZVQwnPqVF4Leosp1qewEtCzYqnEgizSAi33x3EK" {
		t.Fatalf("second relay = %s, want older relay", second.ID)
	}
}
