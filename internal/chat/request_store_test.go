package chat

import (
	"path/filepath"
	"testing"
)

func TestUpsertIncomingRequest_PreservesTerminalState(t *testing.T) {
	t.Parallel()
	local := "12D3KooLOCALREQ"
	tmp := t.TempDir()
	path := filepath.Join(tmp, "requests.db")
	st, err := NewStore(path, local)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	rid := "req-dup-terminal"
	accepted := Request{
		RequestID:         rid,
		FromPeerID:        "12D3KooREMOTE",
		ToPeerID:          local,
		State:             RequestStateAccepted,
		IntroText:         "first",
		ConversationID:    "conv-1",
		LastTransportMode: TransportModeDirect,
	}
	if err := st.UpsertIncomingRequest(accepted); err != nil {
		t.Fatal(err)
	}
	pendingReplay := Request{
		RequestID:         rid,
		FromPeerID:        "12D3KooREMOTE",
		ToPeerID:          local,
		State:             RequestStatePending,
		IntroText:         "replay",
		LastTransportMode: TransportModeDirect,
	}
	if err := st.UpsertIncomingRequest(pendingReplay); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRequest(rid)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != RequestStateAccepted {
		t.Fatalf("state: want %s got %s (replay must not resurrect pending)", RequestStateAccepted, got.State)
	}
	if got.ConversationID != "conv-1" {
		t.Fatalf("conversation_id: want conv-1 got %q", got.ConversationID)
	}
}
