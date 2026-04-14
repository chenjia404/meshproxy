package publicchannel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chenjia404/meshproxy/internal/safe"
	"github.com/gorilla/websocket"
)

type MeshChatChallengeSigner interface {
	SignChallenge(challenge string) (signatureBase64, publicKeyBase64, peerID string, err error)
}

type meshChatPublicChannelClient struct {
	baseURL string
	peerID  string
	signer  MeshChatChallengeSigner
	hc      *http.Client

	token       string
	tokenExpiry time.Time
}

func newMeshChatPublicChannelClient(baseURL, localPeerID string, signer MeshChatChallengeSigner) *meshChatPublicChannelClient {
	u := strings.TrimSpace(baseURL)
	if u == "" || signer == nil {
		return nil
	}
	return &meshChatPublicChannelClient{
		baseURL: strings.TrimRight(u, "/"),
		peerID:  strings.TrimSpace(localPeerID),
		signer:  signer,
		hc:      &http.Client{Timeout: 45 * time.Second},
	}
}

func (c *meshChatPublicChannelClient) ensureToken(ctx context.Context) error {
	if c == nil || c.signer == nil {
		return fmt.Errorf("meshchat publicchannel: not configured")
	}
	if c.token != "" && time.Now().Before(c.tokenExpiry.Add(-2*time.Minute)) {
		return nil
	}
	raw, _ := json.Marshal(map[string]string{"peer_id": c.peerID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/auth/challenge", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("meshchat challenge: %s: %s", resp.Status, string(body))
	}
	var challenge struct {
		ChallengeID string `json:"challenge_id"`
		Challenge   string `json:"challenge"`
	}
	if err := json.Unmarshal(body, &challenge); err != nil {
		return err
	}
	sig, pub, pid, err := c.signer.SignChallenge(challenge.Challenge)
	if err != nil {
		return err
	}
	loginRaw, _ := json.Marshal(map[string]string{
		"peer_id":      pid,
		"challenge_id": challenge.ChallengeID,
		"signature":    sig,
		"public_key":   pub,
	})
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/auth/login", bytes.NewReader(loginRaw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("meshchat login: %s: %s", resp.Status, string(body))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	if strings.TrimSpace(out.Token) == "" {
		return fmt.Errorf("meshchat login: empty token")
	}
	c.token = strings.TrimSpace(out.Token)
	c.tokenExpiry = time.Now().Add(24 * time.Hour)
	return nil
}

func (c *meshChatPublicChannelClient) authHeader(ctx context.Context) (string, error) {
	if err := c.ensureToken(ctx); err != nil {
		return "", err
	}
	return "Bearer " + c.token, nil
}

func (c *meshChatPublicChannelClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	auth, err := c.authHeader(ctx)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("meshchat publicchannel %s %s: %s: %s", method, path, resp.Status, string(respBody))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}

func (c *meshChatPublicChannelClient) createChannel(ctx context.Context, input CreateChannelInput) (ChannelSummary, error) {
	var out ChannelSummary
	err := c.doJSON(ctx, http.MethodPost, "/api/public-channels", input, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) updateChannel(ctx context.Context, channelID string, input UpdateChannelProfileInput) (ChannelSummary, error) {
	var out ChannelSummary
	err := c.doJSON(ctx, http.MethodPatch, "/api/public-channels/"+url.PathEscape(channelID), input, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) getChannelSummary(ctx context.Context, channelID string) (ChannelSummary, error) {
	var out ChannelSummary
	err := c.doJSON(ctx, http.MethodGet, "/api/public-channels/"+url.PathEscape(channelID), nil, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) listChannelsByOwner(ctx context.Context, ownerPeerID string) ([]ChannelSummary, error) {
	var out []ChannelSummary
	path := "/api/public-channels?owner_peer_id=" + url.QueryEscape(strings.TrimSpace(ownerPeerID))
	err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) listSubscribedChannels(ctx context.Context) ([]ChannelSummary, error) {
	var out []ChannelSummary
	err := c.doJSON(ctx, http.MethodGet, "/api/public-channels/subscriptions", nil, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) subscribeChannel(ctx context.Context, channelID string, lastSeenSeq int64) (SubscribeResult, error) {
	var out SubscribeResult
	err := c.doJSON(ctx, http.MethodPost, "/api/public-channels/"+url.PathEscape(channelID)+"/subscribe", map[string]any{"last_seen_seq": lastSeenSeq}, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) getChannelHead(ctx context.Context, channelID string) (ChannelHead, error) {
	var out ChannelHead
	err := c.doJSON(ctx, http.MethodGet, "/api/public-channels/"+url.PathEscape(channelID)+"/head", nil, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) getMessages(ctx context.Context, channelID string, beforeMessageID int64, limit int) ([]ChannelMessage, error) {
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	path := fmt.Sprintf("/api/public-channels/%s/messages?before_message_id=%d&limit=%d", url.PathEscape(channelID), beforeMessageID, limit)
	var out struct {
		ChannelID string           `json:"channel_id"`
		Items     []ChannelMessage `json:"items"`
	}
	err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out.Items, err
}

func (c *meshChatPublicChannelClient) getMessage(ctx context.Context, channelID string, messageID int64) (ChannelMessage, error) {
	var out ChannelMessage
	err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/public-channels/%s/messages/%d", url.PathEscape(channelID), messageID), nil, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) createMessage(ctx context.Context, channelID string, input UpsertMessageInput) (ChannelMessage, error) {
	var out ChannelMessage
	err := c.doJSON(ctx, http.MethodPost, "/api/public-channels/"+url.PathEscape(channelID)+"/messages", input, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) updateMessage(ctx context.Context, channelID string, messageID int64, input UpsertMessageInput) (ChannelMessage, error) {
	var out ChannelMessage
	err := c.doJSON(ctx, http.MethodPatch, fmt.Sprintf("/api/public-channels/%s/messages/%d", url.PathEscape(channelID), messageID), input, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) deleteMessage(ctx context.Context, channelID string, messageID int64) (ChannelMessage, error) {
	var out ChannelMessage
	err := c.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/api/public-channels/%s/messages/%d", url.PathEscape(channelID), messageID), nil, &out)
	return out, err
}

func (c *meshChatPublicChannelClient) getChanges(ctx context.Context, channelID string, afterSeq int64, limit int) (GetChangesResponse, error) {
	if limit <= 0 {
		limit = DefaultChangesLimit
	}
	var out GetChangesResponse
	path := fmt.Sprintf("/api/public-channels/%s/changes?after_seq=%d&limit=%d", url.PathEscape(channelID), afterSeq, limit)
	err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (s *Service) isServerModeActive() bool {
	return s != nil && s.serverMode && s.meshChat != nil
}

func (s *Service) SetMeshChatServerMode(enabled bool, baseURL string, signer MeshChatChallengeSigner) {
	if s == nil {
		return
	}
	s.serverMode = enabled && strings.TrimSpace(baseURL) != "" && signer != nil
	if !s.serverMode {
		s.meshChat = nil
		return
	}
	s.meshChat = newMeshChatPublicChannelClient(baseURL, s.localPeer, signer)
	safe.Go("publicchannel.meshchat.bootstrap", func() {
		s.meshChatSyncAll(context.Background())
		s.startMeshChatWebSocket(s.ctx)
	})
}

func (s *Service) serverModeProviderView(channelID string) []ChannelProvider {
	now := time.Now().Unix()
	return []ChannelProvider{{
		ChannelID:     channelID,
		PeerID:        "meshchat-server",
		Source:        "meshchat-server",
		UpdatedAt:     now,
		LastSuccessAt: now,
		SuccessCount:  1,
	}}
}

func (s *Service) cacheServerSummary(summary ChannelSummary) error {
	if err := s.store.ApplyProfile(summary.Profile); err != nil {
		return err
	}
	if err := s.store.ApplyHead(summary.Head); err != nil {
		return err
	}
	now := time.Now().Unix()
	lastSeen := summary.Sync.LastSeenSeq
	if lastSeen <= 0 && summary.Head.LastSeq > 0 {
		lastSeen = summary.Head.LastSeq
	}
	if err := s.store.UpdateSyncState(summary.Profile.ChannelID, lastSeen, maxInt64(summary.Sync.LastSyncedSeq, lastSeen), summary.Sync.Subscribed, now); err != nil {
		return err
	}
	if summary.Sync.UnreadCount == 0 {
		_ = s.store.ClearChannelUnreadCount(summary.Profile.ChannelID, now)
	}
	return nil
}

func (s *Service) cacheServerMessages(channelID string, items []ChannelMessage) error {
	if len(items) == 0 {
		return nil
	}
	for _, item := range items {
		if err := s.store.ApplyMessage(item); err != nil {
			return err
		}
	}
	return s.store.UpdateLoadedRange(channelID, items, time.Now().Unix())
}

func (s *Service) cacheServerSubscribeResult(channelID string, result SubscribeResult, lastSeenSeq int64) error {
	if err := s.store.ApplyProfile(result.Profile); err != nil {
		return err
	}
	if err := s.store.ApplyHead(result.Head); err != nil {
		return err
	}
	if err := s.cacheServerMessages(channelID, result.Messages); err != nil {
		return err
	}
	if lastSeenSeq <= 0 {
		lastSeenSeq = result.Head.LastSeq
	}
	return s.store.UpdateSyncState(channelID, lastSeenSeq, result.Head.LastSeq, true, time.Now().Unix())
}

func (s *Service) cacheServerMessage(channelID string, msg ChannelMessage) error {
	if _, err := s.store.GetChannelProfile(channelID); err != nil {
		if err == sql.ErrNoRows {
			ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
			defer cancel()
			summary, ferr := s.meshChat.getChannelSummary(ctx, channelID)
			if ferr != nil {
				return ferr
			}
			if err := s.cacheServerSummary(summary); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return s.store.ApplyMessage(msg)
}

func (s *Service) serverModeRefreshWSSubscriptions() {
	if !s.isServerModeActive() {
		return
	}
	s.meshChatWSMu.Lock()
	conn := s.meshChatWSConn
	s.meshChatWSMu.Unlock()
	if conn == nil {
		return
	}
	channelIDs := s.serverModeSubscriptionChannelIDs()
	if len(channelIDs) == 0 {
		return
	}
	s.meshChatWSWriteMu.Lock()
	defer s.meshChatWSWriteMu.Unlock()
	_ = conn.WriteJSON(map[string]any{"action": "subscribe_public_channels", "channel_ids": channelIDs})
}

func (s *Service) serverModeSubscriptionChannelIDs() []string {
	seen := map[string]struct{}{}
	var out []string
	if subs, err := s.store.ListSubscribedChannels(s.localPeer); err == nil {
		for _, item := range subs {
			channelID := strings.TrimSpace(item.Profile.ChannelID)
			if channelID == "" {
				continue
			}
			if _, ok := seen[channelID]; ok {
				continue
			}
			seen[channelID] = struct{}{}
			out = append(out, channelID)
		}
	}
	if owned, err := s.store.ListChannelsByOwner(s.localPeer); err == nil {
		for _, item := range owned {
			channelID := strings.TrimSpace(item.Profile.ChannelID)
			if channelID == "" {
				continue
			}
			if _, ok := seen[channelID]; ok {
				continue
			}
			seen[channelID] = struct{}{}
			out = append(out, channelID)
		}
	}
	return out
}

func (s *Service) meshChatSyncAll(ctx context.Context) {
	if !s.isServerModeActive() {
		return
	}
	for _, channelID := range s.serverModeSubscriptionChannelIDs() {
		s.serverModeSyncChannel(ctx, channelID)
	}
}

func (s *Service) serverModeSyncChannel(ctx context.Context, channelID string) error {
	if !s.isServerModeActive() {
		return nil
	}
	summary, err := s.meshChat.getChannelSummary(ctx, channelID)
	if err != nil {
		return err
	}
	if err := s.cacheServerSummary(summary); err != nil {
		return err
	}
	state, err := s.store.GetChannelSyncState(channelID)
	afterSeq := int64(0)
	if err == nil {
		afterSeq = state.LastSyncedSeq
	}
	resp, err := s.meshChat.getChanges(ctx, channelID, afterSeq, DefaultChangesLimit)
	if err != nil {
		return err
	}
	for _, item := range resp.Items {
		switch item.ChangeType {
		case ChangeTypeProfile:
			summary, err := s.meshChat.getChannelSummary(ctx, channelID)
			if err != nil {
				return err
			}
			if err := s.cacheServerSummary(summary); err != nil {
				return err
			}
		case ChangeTypeMessage:
			if item.MessageID == nil {
				continue
			}
			msg, err := s.meshChat.getMessage(ctx, channelID, *item.MessageID)
			if err != nil {
				return err
			}
			if err := s.cacheServerMessage(channelID, msg); err != nil {
				return err
			}
		}
		_ = s.store.RecordChange(item)
	}
	if resp.CurrentLastSeq > afterSeq {
		return s.store.UpdateSyncState(channelID, resp.CurrentLastSeq, resp.CurrentLastSeq, true, time.Now().Unix())
	}
	return nil
}

func (s *Service) handleMeshChatWSFrame(data []byte) {
	var env struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return
	}
	switch env.Type {
	case "publicchannel.profile.updated":
		var payload struct {
			ChannelID string       `json:"channel_id"`
			Profile   ChannelProfile `json:"profile"`
			Head      ChannelHead    `json:"head"`
		}
		if json.Unmarshal(env.Data, &payload) != nil || strings.TrimSpace(payload.ChannelID) == "" {
			return
		}
		_ = s.cacheServerSummary(ChannelSummary{
			Profile: payload.Profile,
			Head:    payload.Head,
			Sync:    ChannelSyncState{ChannelID: payload.ChannelID, Subscribed: true},
		})
	case "publicchannel.message.created", "publicchannel.message.updated", "publicchannel.message.deleted":
		var payload struct {
			ChannelID string         `json:"channel_id"`
			Message   *ChannelMessage `json:"message"`
		}
		if json.Unmarshal(env.Data, &payload) != nil || payload.Message == nil {
			return
		}
		_ = s.cacheServerMessage(payload.ChannelID, *payload.Message)
		_ = s.store.RecordChange(ChannelChange{
			ChannelID:  payload.ChannelID,
			Seq:        payload.Message.Seq,
			ChangeType: ChangeTypeMessage,
			MessageID:  ptrInt64(payload.Message.MessageID),
			Version:    ptrInt64(payload.Message.Version),
			IsDeleted:  ptrBool(payload.Message.IsDeleted),
			CreatedAt:  payload.Message.UpdatedAt,
		})
	}
}

func (s *Service) meshChatWSReadLoop(conn *websocket.Conn) error {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		s.handleMeshChatWSFrame(data)
	}
}

func (s *Service) startMeshChatWebSocket(ctx context.Context) {
	if !s.isServerModeActive() {
		return
	}
	if ctx == nil {
		ctx = s.ctx
	}
	dialBackoff := time.Second
	const maxDialBackoff = 60 * time.Second
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 45 * time.Second}
	for {
		if s.ctx.Err() != nil || !s.isServerModeActive() {
			return
		}
		auth, err := s.meshChat.authHeader(ctx)
		if err != nil {
			log.Printf("[publicchannel] meshchat ws auth: %v", err)
			time.Sleep(dialBackoff)
			dialBackoff = minDuration(dialBackoff*2, maxDialBackoff)
			continue
		}
		u, err := url.Parse(s.meshChat.baseURL)
		if err != nil {
			log.Printf("[publicchannel] meshchat ws parse: %v", err)
			time.Sleep(dialBackoff)
			dialBackoff = minDuration(dialBackoff*2, maxDialBackoff)
			continue
		}
		scheme := "ws"
		if u.Scheme == "https" {
			scheme = "wss"
		}
		wsURL := fmt.Sprintf("%s://%s/api/ws?token=%s", scheme, u.Host, url.QueryEscape(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer"))))
		conn, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			log.Printf("[publicchannel] meshchat ws dial: %v", err)
			time.Sleep(dialBackoff)
			dialBackoff = minDuration(dialBackoff*2, maxDialBackoff)
			continue
		}
		dialBackoff = time.Second
		s.meshChatWSMu.Lock()
		s.meshChatWSConn = conn
		s.meshChatWSMu.Unlock()
		s.serverModeRefreshWSSubscriptions()
		go func() {
			syncCtx, cancel := context.WithTimeout(s.ctx, 120*time.Second)
			defer cancel()
			s.meshChatSyncAll(syncCtx)
		}()
		err = s.meshChatWSReadLoop(conn)
		s.meshChatWSMu.Lock()
		if s.meshChatWSConn == conn {
			s.meshChatWSConn = nil
		}
		s.meshChatWSMu.Unlock()
		_ = conn.Close()
		if err != nil {
			log.Printf("[publicchannel] meshchat ws read: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}


