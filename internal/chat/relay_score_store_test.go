package chat

import (
	"path/filepath"
	"testing"
	"time"
)

func TestApplyRelayNodeConnectScore_FirstConnect(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "relay_score.db")
	st, err := NewStore(path, "12D3KooLOCAL")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	add, err := st.ApplyRelayNodeConnectScore("12D3KooRelay", "203.0.113.1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if add != 0 {
		t.Fatalf("added: want 0 got %d", add)
	}
	m, err := st.RelayNodeScoresMap()
	if err != nil {
		t.Fatal(err)
	}
	if m["12D3KooRelay"] != 0 {
		t.Fatalf("score: want 0 got %d", m["12D3KooRelay"])
	}
}

func TestApplyRelayNodeConnectScore_SameIPByMinutes(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "relay_score2.db")
	st, err := NewStore(path, "12D3KooLOCAL")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := st.ApplyRelayNodeConnectScore("12D3KooRelay", "203.0.113.1", t0); err != nil {
		t.Fatal(err)
	}
	t1 := t0.Add(5 * time.Minute)
	add, err := st.ApplyRelayNodeConnectScore("12D3KooRelay", "203.0.113.1", t1)
	if err != nil {
		t.Fatal(err)
	}
	if add != 5 {
		t.Fatalf("added: want 5 got %d", add)
	}
	m, err := st.RelayNodeScoresMap()
	if err != nil {
		t.Fatal(err)
	}
	if m["12D3KooRelay"] != 5 {
		t.Fatalf("score: want 5 got %d", m["12D3KooRelay"])
	}
}

func TestApplyRelayNodeConnectScore_CapPerConnect(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "relay_score3.db")
	st, err := NewStore(path, "12D3KooLOCAL")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := st.ApplyRelayNodeConnectScore("p", "198.51.100.1", t0); err != nil {
		t.Fatal(err)
	}
	t1 := t0.Add(100 * time.Minute)
	add, err := st.ApplyRelayNodeConnectScore("p", "198.51.100.1", t1)
	if err != nil {
		t.Fatal(err)
	}
	if add != relayNodeMaxScorePerConnect {
		t.Fatalf("added: want %d got %d", relayNodeMaxScorePerConnect, add)
	}
}

func TestApplyRelayNodeConnectScore_IPChangeClearsScore(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "relay_score4.db")
	st, err := NewStore(path, "12D3KooLOCAL")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := st.ApplyRelayNodeConnectScore("p", "198.51.100.1", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRelayNodeConnectScore("p", "198.51.100.1", t0.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	add, err := st.ApplyRelayNodeConnectScore("p", "198.51.100.2", t0.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if add != 0 {
		t.Fatalf("added on ip change: want 0 got %d", add)
	}
	m, err := st.RelayNodeScoresMap()
	if err != nil {
		t.Fatal(err)
	}
	if m["p"] != 0 {
		t.Fatalf("score: want 0 after ip change got %d", m["p"])
	}
}
