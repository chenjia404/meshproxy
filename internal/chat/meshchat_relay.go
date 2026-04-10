package chat

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/safe"
	"github.com/gorilla/websocket"
)

// MeshChatChallengeSigner 使用 libp2p 身份签名 meshchat-server challenge（与 /api/auth/login 一致）。
type MeshChatChallengeSigner interface {
	SignChallenge(challenge string) (signatureBase64, publicKeyBase64, peerID string, err error)
}

// meshChatHTTPClient 仅用于 mesh-proxy 连接唯一上游 meshchat-server（Android 不直接使用）。
type meshChatHTTPClient struct {
	baseURL string
	peerID  string
	hc      *http.Client
	signer  MeshChatChallengeSigner

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

func newMeshChatHTTPClient(baseURL, localPeerID string, signer MeshChatChallengeSigner) *meshChatHTTPClient {
	u := strings.TrimSpace(baseURL)
	if u == "" {
		return nil
	}
	return &meshChatHTTPClient{
		baseURL: strings.TrimRight(u, "/"),
		peerID:  strings.TrimSpace(localPeerID),
		hc:      &http.Client{Timeout: 45 * time.Second},
		signer:  signer,
	}
}

func (c *meshChatHTTPClient) ensureToken(ctx context.Context) error {
	if c == nil || c.signer == nil {
		return fmt.Errorf("meshchat relay: not configured")
	}
	if c.peerID == "" {
		return fmt.Errorf("meshchat relay: empty peer id")
	}
	c.mu.Lock()
	valid := c.token != "" && time.Now().Before(c.tokenExpiry.Add(-2*time.Minute))
	c.mu.Unlock()
	if valid {
		return nil
	}

	challengeBody := map[string]string{"peer_id": c.peerID}
	raw, _ := json.Marshal(challengeBody)
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
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("meshchat challenge: %s: %s", resp.Status, string(b))
	}
	var ch struct {
		ChallengeID string `json:"challenge_id"`
		Challenge   string `json:"challenge"`
	}
	if err := json.Unmarshal(b, &ch); err != nil {
		return err
	}
	sig, pub, pid, err := c.signer.SignChallenge(ch.Challenge)
	if err != nil {
		return err
	}
	loginBody := map[string]string{
		"peer_id":        pid,
		"challenge_id":   ch.ChallengeID,
		"signature":      sig,
		"public_key":     pub,
	}
	lraw, _ := json.Marshal(loginBody)
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/auth/login", bytes.NewReader(lraw))
	if err != nil {
		return err
	}
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := c.hc.Do(req2)
	if err != nil {
		return err
	}
	defer resp2.Body.Close()
	b2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("meshchat login: %s: %s", resp2.Status, string(b2))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b2, &out); err != nil {
		return err
	}
	if out.Token == "" {
		return fmt.Errorf("meshchat login: empty token")
	}
	c.mu.Lock()
	c.token = out.Token
	c.tokenExpiry = time.Now().Add(24 * time.Hour)
	c.mu.Unlock()
	return nil
}

func (c *meshChatHTTPClient) authHeader(ctx context.Context) (string, error) {
	if err := c.ensureToken(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	tok := c.token
	c.mu.Unlock()
	return "Bearer " + tok, nil
}

type dmConversationView struct {
	ConversationID string    `json:"conversation_id"`
	PeerID         string    `json:"peer_id"`
	LastMessageSeq uint64    `json:"last_message_seq"`
	LastMessageAt  time.Time `json:"last_message_at"`
}

type dmMessageView struct {
	MessageID        string    `json:"message_id"`
	ConversationID   string    `json:"conversation_id"`
	Seq              uint64    `json:"seq"`
	ContentType      string    `json:"content_type"`
	Payload          any       `json:"payload"`
	SenderPeerID     string    `json:"sender_peer_id"`
	RecipientPeerID  string    `json:"recipient_peer_id"`
	ClientMsgID      string    `json:"client_msg_id"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	RecipientAckedAt *time.Time `json:"recipient_acked_at,omitempty"`
}

func (c *meshChatHTTPClient) postDMConversation(ctx context.Context, peerID string) (*dmConversationView, error) {
	auth, err := c.authHeader(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]string{"peer_id": peerID}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/dm/conversations", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("post dm conversation: %s: %s", resp.Status, string(b))
	}
	var out dmConversationView
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *meshChatHTTPClient) postDMMessage(ctx context.Context, upstreamConvID, clientMsgID, text string) (*dmMessageView, error) {
	auth, err := c.authHeader(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"client_msg_id": clientMsgID,
		"content_type":  "text",
		"payload":       map[string]string{"text": text},
	}
	raw, _ := json.Marshal(body)
	u := fmt.Sprintf("%s/api/dm/conversations/%s/messages", c.baseURL, url.PathEscape(upstreamConvID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("post dm message: %s: %s", resp.Status, string(b))
	}
	var out dmMessageView
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *meshChatHTTPClient) postDMAck(ctx context.Context, upstreamMessageID string) error {
	auth, err := c.authHeader(ctx)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/api/dm/messages/%s/ack", c.baseURL, url.PathEscape(upstreamMessageID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", auth)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dm ack: %s: %s", resp.Status, string(b))
	}
	return nil
}

func (c *meshChatHTTPClient) listDMAfter(ctx context.Context, upstreamConvID string, afterSeq uint64, limit int) ([]dmMessageView, error) {
	auth, err := c.authHeader(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	u := fmt.Sprintf("%s/api/dm/conversations/%s/messages?after_seq=%d&limit=%d", c.baseURL, url.PathEscape(upstreamConvID), afterSeq, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dm list: %s: %s", resp.Status, string(b))
	}
	var out []dmMessageView
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) tryMeshChatRelaySend(msgID string) {
	if s == nil || s.meshChat == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer cancel()
	m, err := s.store.GetMessage(msgID)
	if err != nil {
		return
	}
	if m.Direction != "outbound" || m.MsgType != MessageTypeChatText {
		return
	}
	conv, err := s.store.GetConversation(m.ConversationID)
	if err != nil {
		return
	}
	upConvID := strings.TrimSpace(conv.UpstreamConversationID)
	if upConvID == "" {
		dcv, err := s.meshChat.postDMConversation(ctx, conv.PeerID)
		if err != nil {
			log.Printf("[chat] meshchat create conversation: %v", err)
			s.markRelayFailed(&m, err)
			return
		}
		upConvID = dcv.ConversationID
		if err := s.store.SetConversationUpstreamMeta(conv.ConversationID, upConvID, conv.LastUpstreamSyncSeq); err != nil {
			log.Printf("[chat] persist upstream conv id: %v", err)
		}
	}
	cm := strings.TrimSpace(m.ClientMsgID)
	if cm == "" {
		cm = m.MsgID
	}
	dmv, err := s.meshChat.postDMMessage(ctx, upConvID, cm, m.Plaintext)
	if err != nil {
		log.Printf("[chat] meshchat post message msg=%s: %v", msgID, err)
		s.markRelayFailed(&m, err)
		return
	}
	m.TransportKind = TransportKindRelayStore
	m.RelayStatus = RelayStatusRelayed
	m.UpstreamMessageID = dmv.MessageID
	m.ClientMsgID = cm
	m.LastRelayAt = time.Now().UTC()
	blob, _ := s.store.GetMessageBlob(m.MsgID)
	if _, err := s.store.AddMessage(m, blob); err != nil {
		log.Printf("[chat] meshchat update relay meta: %v", err)
	}
	s.publishDirectMessageStateUpdate(m.MsgID)
}

func (s *Service) markRelayFailed(m *Message, _ error) {
	m.TransportKind = TransportKindRelayStore
	m.RelayStatus = RelayStatusFailed
	m.LastRelayAt = time.Now().UTC()
	blob, _ := s.store.GetMessageBlob(m.MsgID)
	if _, err := s.store.AddMessage(*m, blob); err != nil {
		log.Printf("[chat] meshchat mark failed: %v", err)
	}
	s.publishDirectMessageStateUpdate(m.MsgID)
}

// TryMeshChatUpstreamAck 在本地 delivery_ack 已通过 sendEnvelope 成功发出后，对来自上游 relay 的入站消息向 meshchat-server 补 ACK。
func (s *Service) TryMeshChatUpstreamAck(msgID string) {
	if s == nil || s.meshChat == nil {
		return
	}
	m, err := s.store.GetMessage(msgID)
	if err != nil {
		return
	}
	if m.Direction != "inbound" || strings.TrimSpace(m.UpstreamMessageID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	if err := s.meshChat.postDMAck(ctx, m.UpstreamMessageID); err != nil {
		log.Printf("[chat] meshchat upstream ack msg=%s: %v", msgID, err)
		return
	}
	m.RelayStatus = RelayStatusAcked
	m.AckPending = false
	blob, _ := s.store.GetMessageBlob(m.MsgID)
	if _, err := s.store.AddMessage(m, blob); err != nil {
		log.Printf("[chat] meshchat ack persist: %v", err)
	}
	s.publishDirectMessageStateUpdate(msgID)
}

func (s *Service) meshChatBootstrap() {
	if s == nil || s.meshChat == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 120*time.Second)
	defer cancel()
	convs, err := s.store.ListConversations()
	if err != nil {
		return
	}
	for i := range convs {
		c := convs[i]
		if strings.TrimSpace(c.UpstreamConversationID) == "" {
			dcv, err := s.meshChat.postDMConversation(ctx, c.PeerID)
			if err != nil {
				log.Printf("[chat] meshchat bootstrap conv peer=%s: %v", c.PeerID, err)
				continue
			}
			_ = s.store.SetConversationUpstreamMeta(c.ConversationID, dcv.ConversationID, c.LastUpstreamSyncSeq)
		}
	}
	s.syncMeshChatAllUpstreamConversations(ctx)
	safe.Go("chat.meshchat.ws", func() { s.startMeshChatWebSocket(s.ctx) })
}

// syncMeshChatAllUpstreamConversations 对已有上游会话 ID 的会话按 LastUpstreamSyncSeq 做 HTTP 补拉（与 WebSocket 无关，用于启动与重连后填补断线窗口）。
func (s *Service) syncMeshChatAllUpstreamConversations(ctx context.Context) {
	if s == nil || s.meshChat == nil {
		return
	}
	convs, err := s.store.ListConversations()
	if err != nil {
		log.Printf("[chat] meshchat sync all: list conversations: %v", err)
		return
	}
	for i := range convs {
		c := convs[i]
		if strings.TrimSpace(c.UpstreamConversationID) == "" {
			continue
		}
		s.syncMeshChatMessages(ctx, c.ConversationID)
	}
}

func (s *Service) syncMeshChatMessages(ctx context.Context, localConvID string) {
	if s == nil || s.meshChat == nil {
		return
	}
	conv, err := s.store.GetConversation(localConvID)
	if err != nil {
		return
	}
	up := strings.TrimSpace(conv.UpstreamConversationID)
	if up == "" {
		return
	}
	after := conv.LastUpstreamSyncSeq
	for {
		list, err := s.meshChat.listDMAfter(ctx, up, after, 50)
		if err != nil || len(list) == 0 {
			break
		}
		for _, dm := range list {
			if err := s.applyUpstreamDMView(ctx, conv, &dm); err != nil {
				log.Printf("[chat] meshchat apply dm seq=%d: %v", dm.Seq, err)
			}
			if dm.Seq > after {
				after = dm.Seq
			}
		}
		if err := s.store.SetConversationUpstreamMeta(localConvID, up, after); err != nil {
			log.Printf("[chat] meshchat sync seq: %v", err)
		}
		if len(list) < 50 {
			break
		}
	}
}

func (s *Service) applyUpstreamDMView(ctx context.Context, conv Conversation, dm *dmMessageView) error {
	if dm == nil || dm.ContentType != "text" {
		return nil
	}
	text := ""
	if m, ok := dm.Payload.(map[string]any); ok {
		if t, ok := m["text"].(string); ok {
			text = strings.TrimSpace(t)
		}
	}
	if text == "" {
		return nil
	}
	if dm.SenderPeerID == s.localPeer {
		return nil
	}
	// 已存在同 upstream id
	if existing, err := s.store.FindMessageByUpstreamID(dm.MessageID); err == nil && existing != "" {
		return nil
	}
	sess, err := s.store.GetSessionState(conv.ConversationID)
	if err != nil {
		return err
	}
	nonce := protocol.BuildAEADNonce("fwd", sess.RecvCounter)
	aad := []byte(conv.ConversationID + "\x00chat_text")
	ciphertext, err := protocol.AEADSeal(sess.RecvKey, nonce, []byte(text), aad)
	if err != nil {
		return err
	}
	msg := Message{
		MsgID:               dm.MessageID,
		ConversationID:    conv.ConversationID,
		SenderPeerID:        dm.SenderPeerID,
		ReceiverPeerID:      s.localPeer,
		Direction:           "inbound",
		MsgType:             MessageTypeChatText,
		Plaintext:           text,
		TransportMode:       TransportModeDirect,
		State:               MessageStateReceived,
		Counter:             sess.RecvCounter,
		CreatedAt:           dm.CreatedAt.UTC(),
		TransportKind:       TransportKindRelayStore,
		RelayStatus:         RelayStatusRelayed,
		UpstreamMessageID:   dm.MessageID,
		ClientMsgID:         dm.ClientMsgID,
		AckPending:          true,
	}
	if err := s.store.AddInboundMessageAndAdvanceRecvCounter(msg, ciphertext, sess.RecvCounter+1, true); err != nil {
		return err
	}
	s.publishChatEvent(chatEventDirectMessage(msg))
	_ = s.store.UpsertPeer(dm.SenderPeerID, "", "")
	return nil
}

// dmRealtimePayload 与 meshchat-server WS 上 dm.message.created / dm.message.acked 的 data 结构一致。
type dmRealtimePayload struct {
	ConversationID string         `json:"conversation_id"`
	Message        *dmMessageView `json:"message"`
}

// applyUpstreamDMAcked 处理上游 dm.message.acked，将本地对应消息的 relay_status 推进为 acked（出站已读确认；入站与 TryMeshChatUpstreamAck 幂等）。
func (s *Service) applyUpstreamDMAcked(payload *dmRealtimePayload) {
	if s == nil || payload == nil || payload.Message == nil {
		return
	}
	upMid := strings.TrimSpace(payload.Message.MessageID)
	if upMid == "" {
		return
	}
	localMsgID, err := s.store.FindMessageByUpstreamID(upMid)
	if err != nil || localMsgID == "" {
		return
	}
	m, err := s.store.GetMessage(localMsgID)
	if err != nil {
		return
	}
	upConv := strings.TrimSpace(payload.ConversationID)
	if upConv != "" {
		localConvID, err := s.store.GetConversationByUpstreamID(upConv)
		if err == nil && localConvID != "" && localConvID != m.ConversationID {
			return
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return
		}
	}
	if m.RelayStatus == RelayStatusAcked {
		return
	}
	m.RelayStatus = RelayStatusAcked
	if m.Direction == "inbound" {
		m.AckPending = false
	}
	blob, _ := s.store.GetMessageBlob(m.MsgID)
	if _, err := s.store.AddMessage(m, blob); err != nil {
		log.Printf("[chat] meshchat dm ack event persist: %v", err)
		return
	}
	s.publishDirectMessageStateUpdate(m.MsgID)
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
	case "dm.message.created":
		var payload dmRealtimePayload
		if json.Unmarshal(env.Data, &payload) != nil || payload.Message == nil {
			return
		}
		localID, err := s.store.GetConversationByUpstreamID(payload.ConversationID)
		if err != nil || localID == "" {
			return
		}
		c, err := s.store.GetConversation(localID)
		if err != nil {
			return
		}
		_ = s.applyUpstreamDMView(context.Background(), c, payload.Message)
	case "dm.message.acked":
		var payload dmRealtimePayload
		if json.Unmarshal(env.Data, &payload) != nil || payload.Message == nil {
			return
		}
		s.applyUpstreamDMAcked(&payload)
	default:
	}
}

func (s *Service) meshChatWSSubscribe(conn *websocket.Conn) {
	convs, _ := s.store.ListConversations()
	var ids []string
	for _, c := range convs {
		if strings.TrimSpace(c.UpstreamConversationID) != "" {
			ids = append(ids, c.UpstreamConversationID)
		}
	}
	if len(ids) > 0 {
		_ = conn.WriteJSON(map[string]any{"action": "subscribe_dm", "conversation_ids": ids})
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

func meshChatWSNextDialBackoff(cur, max time.Duration) time.Duration {
	cur *= 2
	if cur > max {
		return max
	}
	return cur
}

func (s *Service) startMeshChatWebSocket(ctx context.Context) {
	if s.meshChat == nil {
		return
	}
	if ctx == nil {
		ctx = s.ctx
	}
	dialBackoff := time.Second
	const maxDialBackoff = 60 * time.Second
	const disconnectPause = 2 * time.Second

	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 45 * time.Second,
	}

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		auth, err := s.meshChat.authHeader(ctx)
		if err != nil {
			log.Printf("[chat] meshchat ws: auth: %v", err)
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(dialBackoff):
			}
			dialBackoff = meshChatWSNextDialBackoff(dialBackoff, maxDialBackoff)
			continue
		}
		u, err := url.Parse(s.meshChat.baseURL)
		if err != nil {
			log.Printf("[chat] meshchat ws: parse url: %v", err)
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(dialBackoff):
			}
			dialBackoff = meshChatWSNextDialBackoff(dialBackoff, maxDialBackoff)
			continue
		}
		scheme := "ws"
		if u.Scheme == "https" {
			scheme = "wss"
		}
		tok := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer"))
		tok = strings.TrimSpace(tok)
		wsURL := fmt.Sprintf("%s://%s/api/ws?token=%s", scheme, u.Host, url.QueryEscape(tok))
		conn, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			log.Printf("[chat] meshchat ws dial: %v", err)
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(dialBackoff):
			}
			dialBackoff = meshChatWSNextDialBackoff(dialBackoff, maxDialBackoff)
			continue
		}
		dialBackoff = time.Second

		// 先启动读协程，避免长时间仅做 HTTP 补拉时 WebSocket 层无法响应对端 ping/control。
		readErrCh := make(chan error, 1)
		go func() {
			readErrCh <- s.meshChatWSReadLoop(conn)
		}()

		syncCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		s.syncMeshChatAllUpstreamConversations(syncCtx)
		cancel()

		s.meshChatWSSubscribe(conn)

		readErr := <-readErrCh
		_ = conn.Close()
		if readErr != nil {
			log.Printf("[chat] meshchat ws read: %v", readErr)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(disconnectPause):
		}
	}
}

// SetMeshChatRelay 配置唯一上游 meshchat-server 基址与签名器；空 URL 则禁用。
func (s *Service) SetMeshChatRelay(baseURL string, signer MeshChatChallengeSigner) {
	if s == nil || strings.TrimSpace(baseURL) == "" || signer == nil {
		return
	}
	s.meshChat = newMeshChatHTTPClient(baseURL, s.localPeer, signer)
	safe.Go("chat.meshchat.bootstrap", func() { s.meshChatBootstrap() })
}
