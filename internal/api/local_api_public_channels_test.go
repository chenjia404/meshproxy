package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenjia404/meshproxy/internal/publicchannel"
)

func TestCreatePublicChannelReturnsTopLevelChannelID(t *testing.T) {
	t.Parallel()

	summary := publicchannel.ChannelSummary{
		Profile: publicchannel.ChannelProfile{
			ChannelID:               "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
			OwnerPeerID:             "12D3KooWTestOwner",
			Name:                    "demo",
			MessageRetentionMinutes: 60,
		},
		Head: publicchannel.ChannelHead{
			ChannelID: "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
		},
	}
	provider := &stubPublicChannelProvider{createChannelResult: summary}
	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{PublicChannels: provider})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/public-channels", bytes.NewBufferString(`{"name":"demo","message_retention_minutes":60}`))
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
	if profile["message_retention_minutes"] != float64(60) {
		t.Fatalf("want profile.message_retention_minutes 60, got %#v", profile["message_retention_minutes"])
	}
	if provider.createChannelInput.MessageRetentionMinutes != 60 {
		t.Fatalf("want create input retention 60, got %d", provider.createChannelInput.MessageRetentionMinutes)
	}
}

func TestUpdatePublicChannelReturnsTopLevelChannelID(t *testing.T) {
	t.Parallel()

	summary := publicchannel.ChannelSummary{
		Profile: publicchannel.ChannelProfile{
			ChannelID:               "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
			OwnerPeerID:             "12D3KooWTestOwner",
			Name:                    "updated",
			MessageRetentionMinutes: 120,
		},
		Head: publicchannel.ChannelHead{
			ChannelID: "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
		},
	}
	provider := &stubPublicChannelProvider{updateChannelResult: summary}
	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{PublicChannels: provider})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/public-channels/"+summary.Profile.ChannelID, bytes.NewBufferString(`{"name":"updated","message_retention_minutes":120}`))
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
	if profile["message_retention_minutes"] != float64(120) {
		t.Fatalf("want profile.message_retention_minutes 120, got %#v", profile["message_retention_minutes"])
	}
	if provider.updateChannelInput.MessageRetentionMinutes == nil || *provider.updateChannelInput.MessageRetentionMinutes != 120 {
		t.Fatalf("want update input retention 120, got %#v", provider.updateChannelInput.MessageRetentionMinutes)
	}
}

func TestUpdatePublicChannelWithoutRetentionKeepsExistingSetting(t *testing.T) {
	t.Parallel()

	summary := publicchannel.ChannelSummary{
		Profile: publicchannel.ChannelProfile{
			ChannelID:               "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e125",
			OwnerPeerID:             "12D3KooWTestOwner",
			Name:                    "updated",
			MessageRetentionMinutes: 120,
		},
	}
	provider := &stubPublicChannelProvider{updateChannelResult: summary}
	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{PublicChannels: provider})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/public-channels/"+summary.Profile.ChannelID, bytes.NewBufferString(`{"name":"updated"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if provider.updateChannelInput.MessageRetentionMinutes != nil {
		t.Fatalf("want omitted retention to stay nil, got %#v", provider.updateChannelInput.MessageRetentionMinutes)
	}
}

func TestListSubscribedPublicChannels(t *testing.T) {
	t.Parallel()

	items := []publicchannel.ChannelSummary{
		{
			Profile: publicchannel.ChannelProfile{
				ChannelID: "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
				Name:      "sub-1",
			},
		},
		{
			Profile: publicchannel.ChannelProfile{
				ChannelID: "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e124",
				Name:      "sub-2",
			},
		},
	}
	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{
		PublicChannels: &stubPublicChannelProvider{listSubscribedChannelsResult: items},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/public-channels/subscriptions", nil)
	rec := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 {
		t.Fatalf("want 2 subscribed channels, got %d", len(body))
	}
	profile, ok := body[0]["profile"].(map[string]any)
	if !ok {
		t.Fatalf("want profile object, got %#v", body[0]["profile"])
	}
	if profile["channel_id"] != items[0].Profile.ChannelID {
		t.Fatalf("want first subscribed channel %q, got %#v", items[0].Profile.ChannelID, profile["channel_id"])
	}
}

func TestListSubscribedPublicChannelsReturnsEmptyArray(t *testing.T) {
	t.Parallel()

	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{
		PublicChannels: &stubPublicChannelProvider{},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/public-channels/subscriptions", nil)
	rec := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body []any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("want empty array, got %d items", len(body))
	}
}

func TestGetPublicChannelMessagesReturnsLocalDataAndLoadsAsync(t *testing.T) {
	t.Parallel()

	loadCalled := make(chan struct{}, 1)
	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{
		PublicChannels: &stubPublicChannelProvider{
			getChannelMessagesErr: sql.ErrNoRows,
			loadMessagesErr:       context.DeadlineExceeded,
			loadMessagesCalled:    loadCalled,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/public-channels/test-channel/messages?limit=20", nil)
	rec := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("want items array, got %#v", body["items"])
	}
	if len(items) != 0 {
		t.Fatalf("want empty local items, got %d", len(items))
	}
	select {
	case <-loadCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected async message load to be triggered")
	}
}

func TestSubscribePublicChannelReturnsLocalPlaceholderImmediately(t *testing.T) {
	t.Parallel()

	result := publicchannel.SubscribeResult{
		Profile: publicchannel.ChannelProfile{ChannelID: "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123"},
		Head:    publicchannel.ChannelHead{ChannelID: "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123"},
	}
	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{
		PublicChannels: &stubPublicChannelProvider{subscribeAsyncResult: result},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/public-channels/"+result.Profile.ChannelID+"/subscribe", bytes.NewBufferString(`{"peer_ids":["12D3KooWSeedPeer"],"last_seen_seq":0}`))
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
	profile, ok := body["profile"].(map[string]any)
	if !ok {
		t.Fatalf("want profile object, got %#v", body["profile"])
	}
	if profile["channel_id"] != result.Profile.ChannelID {
		t.Fatalf("want placeholder channel_id %q, got %#v", result.Profile.ChannelID, profile["channel_id"])
	}
}

func TestSyncPublicChannelReturnsImmediatelyAndRunsAsync(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls int32
	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{
		PublicChannels: &stubPublicChannelProvider{
			syncChannelStarted: started,
			syncChannelRelease: release,
			syncChannelCalls:   &calls,
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/public-channels/test-channel/sync", nil)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		api.server.Handler.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected sync endpoint to return immediately")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["scheduled"] != true {
		t.Fatalf("want scheduled=true, got %#v", body["scheduled"])
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected async sync to start")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("want SyncChannel called once, got %d", atomic.LoadInt32(&calls))
	}

	close(release)
}

func TestCreatePublicChannelFilesMessageMultipart(t *testing.T) {
	t.Parallel()

	result := publicchannel.ChannelMessage{
		ChannelID:   "0195f3f0-8d4a-7c12-b2c1-9db1f0a9e123",
		MessageID:   7,
		MessageType: publicchannel.MessageTypeImage,
		Content: publicchannel.MessageContent{
			Text: "trip",
			Files: []publicchannel.File{
				{FileName: "a.png", MIMEType: "image/png", BlobID: "bafy-a"},
				{FileName: "b.png", MIMEType: "image/png", BlobID: "bafy-b"},
			},
		},
	}
	provider := &stubPublicChannelProvider{createChannelFilesResult: result}
	api := NewLocalAPI(":0", nil, nil, nil, &LocalAPIOpts{PublicChannels: provider})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("text", "trip"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("files", "a.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("png-a")); err != nil {
		t.Fatal(err)
	}
	part, err = writer.CreateFormFile("files", "b.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("png-b")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/public-channels/"+result.ChannelID+"/messages/files", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	api.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if provider.createChannelFilesText != "trip" {
		t.Fatalf("want text trip, got %q", provider.createChannelFilesText)
	}
	if len(provider.createChannelFilesUploads) != 2 {
		t.Fatalf("want 2 uploaded files, got %d", len(provider.createChannelFilesUploads))
	}
	if provider.createChannelFilesUploads[0].FileName != "a.png" || provider.createChannelFilesUploads[1].FileName != "b.png" {
		t.Fatalf("unexpected uploaded file names: %#v", provider.createChannelFilesUploads)
	}
	if provider.createChannelFilesUploads[0].MIMEType == "" || provider.createChannelFilesUploads[1].MIMEType == "" {
		t.Fatalf("want detected mime types, got %#v", provider.createChannelFilesUploads)
	}
	var resp publicchannel.ChannelMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.MessageID != result.MessageID || len(resp.Content.Files) != 2 {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

type stubPublicChannelProvider struct {
	createChannelResult          publicchannel.ChannelSummary
	createChannelInput           publicchannel.CreateChannelInput
	updateChannelResult          publicchannel.ChannelSummary
	updateChannelInput           publicchannel.UpdateChannelProfileInput
	createChannelFilesResult     publicchannel.ChannelMessage
	createChannelFilesText       string
	createChannelFilesUploads    []publicchannel.UploadFileInput
	getChannelSummaryResult      publicchannel.ChannelSummary
	listSubscribedChannelsResult []publicchannel.ChannelSummary
	getChannelMessagesResult     []publicchannel.ChannelMessage
	getChannelMessagesErr        error
	loadMessagesErr              error
	loadMessagesCalled           chan struct{}
	subscribeAsyncResult         publicchannel.SubscribeResult
	syncChannelStarted           chan struct{}
	syncChannelRelease           chan struct{}
	syncChannelCalls             *int32
}

func (s *stubPublicChannelProvider) CreateChannel(input publicchannel.CreateChannelInput) (publicchannel.ChannelSummary, error) {
	s.createChannelInput = input
	return s.createChannelResult, nil
}

func (s *stubPublicChannelProvider) CreateChannelWithAvatar(name, bio string, messageRetentionMinutes int, fileName, mimeType string, data []byte) (publicchannel.ChannelSummary, error) {
	return publicchannel.ChannelSummary{}, nil
}

func (s *stubPublicChannelProvider) UpdateChannelProfile(channelID string, input publicchannel.UpdateChannelProfileInput) (publicchannel.ChannelSummary, error) {
	s.updateChannelInput = input
	return s.updateChannelResult, nil
}

func (s *stubPublicChannelProvider) UpdateChannelProfileWithAvatar(channelID, name, bio string, messageRetentionMinutes int, fileName, mimeType string, data []byte) (publicchannel.ChannelSummary, error) {
	return s.updateChannelResult, nil
}

func (s *stubPublicChannelProvider) CreateChannelMessage(channelID string, input publicchannel.UpsertMessageInput) (publicchannel.ChannelMessage, error) {
	return publicchannel.ChannelMessage{}, nil
}

func (s *stubPublicChannelProvider) CreateChannelFileMessage(channelID, text, fileName, mimeType string, data []byte) (publicchannel.ChannelMessage, error) {
	return publicchannel.ChannelMessage{}, nil
}

func (s *stubPublicChannelProvider) CreateChannelFilesMessage(channelID, text string, files []publicchannel.UploadFileInput) (publicchannel.ChannelMessage, error) {
	s.createChannelFilesText = text
	s.createChannelFilesUploads = append([]publicchannel.UploadFileInput(nil), files...)
	return s.createChannelFilesResult, nil
}

func (s *stubPublicChannelProvider) UpdateChannelMessage(ctx context.Context, channelID string, messageID int64, input publicchannel.UpsertMessageInput) (publicchannel.ChannelMessage, error) {
	return publicchannel.ChannelMessage{}, nil
}

func (s *stubPublicChannelProvider) DeleteChannelMessage(ctx context.Context, channelID string, messageID int64) (publicchannel.ChannelMessage, error) {
	return publicchannel.ChannelMessage{}, nil
}

func (s *stubPublicChannelProvider) GetChannelSummary(channelID string) (publicchannel.ChannelSummary, error) {
	return s.getChannelSummaryResult, nil
}

func (s *stubPublicChannelProvider) GetChannelHead(channelID string) (publicchannel.ChannelHead, error) {
	return publicchannel.ChannelHead{}, nil
}

func (s *stubPublicChannelProvider) GetChannelMessages(channelID string, beforeMessageID int64, limit int) ([]publicchannel.ChannelMessage, error) {
	return s.getChannelMessagesResult, s.getChannelMessagesErr
}

func (s *stubPublicChannelProvider) GetChannelMessage(channelID string, messageID int64) (publicchannel.ChannelMessage, error) {
	return publicchannel.ChannelMessage{}, nil
}

func (s *stubPublicChannelProvider) GetChannelChanges(channelID string, afterSeq int64, limit int) (publicchannel.GetChangesResponse, error) {
	return publicchannel.GetChangesResponse{}, nil
}

func (s *stubPublicChannelProvider) ListChannelsByOwner(ownerPeerID string) ([]publicchannel.ChannelSummary, error) {
	return nil, nil
}

func (s *stubPublicChannelProvider) ListSubscribedChannels() ([]publicchannel.ChannelSummary, error) {
	return s.listSubscribedChannelsResult, nil
}

func (s *stubPublicChannelProvider) ListProviders(channelID string) ([]publicchannel.ChannelProvider, error) {
	return nil, nil
}

func (s *stubPublicChannelProvider) SubscribeChannel(ctx context.Context, channelID string, seedPeerIDs []string, lastSeenSeq int64) (publicchannel.SubscribeResult, error) {
	return publicchannel.SubscribeResult{}, nil
}

func (s *stubPublicChannelProvider) SubscribeChannelAsync(channelID string, seedPeerIDs []string, lastSeenSeq int64) (publicchannel.SubscribeResult, error) {
	return s.subscribeAsyncResult, nil
}

func (s *stubPublicChannelProvider) UnsubscribeChannel(channelID string) error {
	return nil
}

func (s *stubPublicChannelProvider) SyncChannel(ctx context.Context, channelID string) error {
	if s.syncChannelCalls != nil {
		atomic.AddInt32(s.syncChannelCalls, 1)
	}
	if s.syncChannelStarted != nil {
		select {
		case s.syncChannelStarted <- struct{}{}:
		default:
		}
	}
	if s.syncChannelRelease != nil {
		select {
		case <-s.syncChannelRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *stubPublicChannelProvider) LoadMessagesFromProviders(ctx context.Context, channelID string, beforeMessageID int64, limit int) ([]publicchannel.ChannelMessage, error) {
	if s.loadMessagesCalled != nil {
		select {
		case s.loadMessagesCalled <- struct{}{}:
		default:
		}
	}
	return nil, s.loadMessagesErr
}
