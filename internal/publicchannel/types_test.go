package publicchannel

import (
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
