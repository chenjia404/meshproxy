package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chenjia404/meshproxy/internal/publicchannel"
)

func TestCreatePublicChannelReturnsTopLevelChannelID(t *testing.T) {
	t.Parallel()

	summary := publicchannel.ChannelSummary{
		Profile: publicchannel.ChannelProfile{
			ChannelID:   "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
			OwnerPeerID: "12D3KooWTestOwner",
			Name:        "demo",
		},
		Head: publicchannel.ChannelHead{
			ChannelID: "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
		},
	}
	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{
		PublicChannels: stubPublicChannelProvider{createChannelResult: summary},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/public-channels", bytes.NewBufferString(`{"name":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["channel_id"] != summary.Profile.ChannelID {
		t.Fatalf("want top-level channel_id %q, got %#v", summary.Profile.ChannelID, body["channel_id"])
	}
	profile, ok := body["profile"].(map[string]any)
	if !ok {
		t.Fatalf("want profile object, got %#v", body["profile"])
	}
	if profile["channel_id"] != summary.Profile.ChannelID {
		t.Fatalf("want profile.channel_id %q, got %#v", summary.Profile.ChannelID, profile["channel_id"])
	}
}

func TestUpdatePublicChannelReturnsTopLevelChannelID(t *testing.T) {
	t.Parallel()

	summary := publicchannel.ChannelSummary{
		Profile: publicchannel.ChannelProfile{
			ChannelID:   "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
			OwnerPeerID: "12D3KooWTestOwner",
			Name:        "updated",
		},
		Head: publicchannel.ChannelHead{
			ChannelID: "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
		},
	}
	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{
		PublicChannels: stubPublicChannelProvider{updateChannelResult: summary},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/public-channels/"+summary.Profile.ChannelID, bytes.NewBufferString(`{"name":"updated"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["channel_id"] != summary.Profile.ChannelID {
		t.Fatalf("want top-level channel_id %q, got %#v", summary.Profile.ChannelID, body["channel_id"])
	}
	profile, ok := body["profile"].(map[string]any)
	if !ok {
		t.Fatalf("want profile object, got %#v", body["profile"])
	}
	if profile["channel_id"] != summary.Profile.ChannelID {
		t.Fatalf("want profile.channel_id %q, got %#v", summary.Profile.ChannelID, profile["channel_id"])
	}
}

type stubPublicChannelProvider struct {
	createChannelResult publicchannel.ChannelSummary
	updateChannelResult publicchannel.ChannelSummary
}

func (s stubPublicChannelProvider) CreateChannel(input publicchannel.CreateChannelInput) (publicchannel.ChannelSummary, error) {
	return s.createChannelResult, nil
}

func (s stubPublicChannelProvider) CreateChannelWithAvatar(name, bio, fileName, mimeType string, data []byte) (publicchannel.ChannelSummary, error) {
	return publicchannel.ChannelSummary{}, nil
}

func (s stubPublicChannelProvider) UpdateChannelProfile(channelID string, input publicchannel.UpdateChannelProfileInput) (publicchannel.ChannelSummary, error) {
	return s.updateChannelResult, nil
}

func (s stubPublicChannelProvider) UpdateChannelProfileWithAvatar(channelID, name, bio, fileName, mimeType string, data []byte) (publicchannel.ChannelSummary, error) {
	return s.updateChannelResult, nil
}

func (s stubPublicChannelProvider) CreateChannelMessage(channelID string, input publicchannel.UpsertMessageInput) (publicchannel.ChannelMessage, error) {
	return publicchannel.ChannelMessage{}, nil
}

func (s stubPublicChannelProvider) CreateChannelFileMessage(channelID, text, fileName, mimeType string, data []byte) (publicchannel.ChannelMessage, error) {
	return publicchannel.ChannelMessage{}, nil
}

func (s stubPublicChannelProvider) UpdateChannelMessage(ctx context.Context, channelID string, messageID int64, input publicchannel.UpsertMessageInput) (publicchannel.ChannelMessage, error) {
	return publicchannel.ChannelMessage{}, nil
}

func (s stubPublicChannelProvider) DeleteChannelMessage(ctx context.Context, channelID string, messageID int64) (publicchannel.ChannelMessage, error) {
	return publicchannel.ChannelMessage{}, nil
}

func (s stubPublicChannelProvider) GetChannelSummary(channelID string) (publicchannel.ChannelSummary, error) {
	return publicchannel.ChannelSummary{}, nil
}

func (s stubPublicChannelProvider) GetChannelHead(channelID string) (publicchannel.ChannelHead, error) {
	return publicchannel.ChannelHead{}, nil
}

func (s stubPublicChannelProvider) GetChannelMessages(channelID string, beforeMessageID int64, limit int) ([]publicchannel.ChannelMessage, error) {
	return nil, nil
}

func (s stubPublicChannelProvider) GetChannelMessage(channelID string, messageID int64) (publicchannel.ChannelMessage, error) {
	return publicchannel.ChannelMessage{}, nil
}

func (s stubPublicChannelProvider) GetChannelChanges(channelID string, afterSeq int64, limit int) (publicchannel.GetChangesResponse, error) {
	return publicchannel.GetChangesResponse{}, nil
}

func (s stubPublicChannelProvider) ListChannelsByOwner(ownerPeerID string) ([]publicchannel.ChannelSummary, error) {
	return nil, nil
}

func (s stubPublicChannelProvider) ListProviders(channelID string) ([]publicchannel.ChannelProvider, error) {
	return nil, nil
}

func (s stubPublicChannelProvider) SubscribeChannel(ctx context.Context, channelID string, seedPeerIDs []string, lastSeenSeq int64) (publicchannel.SubscribeResult, error) {
	return publicchannel.SubscribeResult{}, nil
}

func (s stubPublicChannelProvider) UnsubscribeChannel(channelID string) error {
	return nil
}

func (s stubPublicChannelProvider) SyncChannel(ctx context.Context, channelID string) error {
	return nil
}

func (s stubPublicChannelProvider) LoadMessagesFromProviders(ctx context.Context, channelID string, beforeMessageID int64, limit int) ([]publicchannel.ChannelMessage, error) {
	return nil, nil
}
