package publicchannel

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestParseChannelIDBoundOwner(t *testing.T) {
	t.Parallel()

	channelUUID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new uuidv7: %v", err)
	}
	channelID := BuildChannelID("12D3KooWBoundOwnerPeer", channelUUID)
	ownerPeerID, parsedUUID, err := ParseChannelID(channelID)
	if err != nil {
		t.Fatalf("parse channel id: %v", err)
	}
	if ownerPeerID != "12D3KooWBoundOwnerPeer" {
		t.Fatalf("unexpected owner peer id: %s", ownerPeerID)
	}
	if parsedUUID != channelUUID {
		t.Fatalf("unexpected uuid: %s", parsedUUID.String())
	}
}

func TestIsMeshChatChannelNotFoundError(t *testing.T) {
	t.Parallel()
	if !isMeshChatChannelNotFoundError(errors.New("channel not found")) {
		t.Fatal("want channel not found")
	}
	if !isMeshChatChannelNotFoundError(fmt.Errorf("lookup: %w", sql.ErrNoRows)) {
		t.Fatal("want sql.ErrNoRows")
	}
	if isMeshChatChannelNotFoundError(errors.New("channel is not owned by local peer")) {
		t.Fatal("ownership error is not not-found")
	}
}

func TestIsLocallyOwnedChannelFromID(t *testing.T) {
	t.Parallel()
	s := &Service{localPeer: "12D3KooWLocal"}
	channelUUID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new uuidv7: %v", err)
	}
	if !s.isLocallyOwnedChannel(BuildChannelID("12D3KooWLocal", channelUUID)) {
		t.Fatal("want local owned")
	}
	if s.isLocallyOwnedChannel(BuildChannelID("12D3KooWOther", channelUUID)) {
		t.Fatal("remote channel must not be treated as locally owned")
	}
}
