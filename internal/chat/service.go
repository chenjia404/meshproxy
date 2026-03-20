package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	coreprotocol "github.com/libp2p/go-libp2p/core/protocol"
	corerouting "github.com/libp2p/go-libp2p/core/routing"
	multiaddr "github.com/multiformats/go-multiaddr"

	"github.com/chenjia404/meshproxy/internal/discovery"
	"github.com/chenjia404/meshproxy/internal/p2p"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/safe"
	"github.com/chenjia404/meshproxy/internal/tunnel"
)

type Service struct {
	ctx       context.Context
	host      host.Host
	routing   corerouting.Routing
	discovery *discovery.Store
	store     *Store
	localPeer string
	avatarDir string

	eventsHub *chatEventHub

	autoConnectSeen      sync.Map
	profileSyncSeen      sync.Map
	avatarFetchSeen      sync.Map
	chatRequestProbeSeen sync.Map
	chatSyncPending      sync.Map // key: conversation_id -> chan ChatSyncResponse
}

type chatRequestProbeState struct {
	supported bool
	at        time.Time
}

type profileSyncState struct {
	mu             sync.Mutex
	inFlight       bool
	lastProfileKey string
	nextAttemptAt  time.Time
	failures       int
}

const (
	directRetryBatchSize        = 64
	directAckWait               = 20 * time.Second
	directSyncBatchSize         = 256
	directAutoConnectTTL        = 30 * time.Second
	directChatRequestProbeTTL   = 1 * time.Minute
	directProfileSyncTTL        = 1 * time.Minute
	directProfileSyncBackoff    = 1 * time.Minute
	directProfileSyncMaxBackoff = 1 * time.Hour
	directAvatarFetchTTL        = 1 * time.Minute

	friendRequestRetryDeadline     = 7 * 24 * time.Hour
	friendRequestRetryTickInterval = 20 * time.Second
	friendRequestRetryBatchSize    = 32
	friendRequestRetryBaseDelay    = 10 * time.Second
	friendRequestRetryMaxDelay     = 30 * time.Minute

	// Keep a few relay connections warm so that when direct sending fails,
	// we can quickly switch to relay forwarding without long dial delays.
	relayKeepConnectedInterval = 30 * time.Second
	relayKeepConnectedMax      = 10

	// Periodic retry for queued outbox_jobs (e.g. relay path temporarily unavailable).
	outboxRetryTickInterval = 5 * time.Second
	outboxRetryBatchSize    = 64
)

func NewService(ctx context.Context, dbPath, avatarDir string, h host.Host, routing corerouting.Routing, ds *discovery.Store) (*Service, error) {
	s := &Service{
		ctx:       ctx,
		host:      h,
		routing:   routing,
		discovery: ds,
		localPeer: h.ID().String(),
		avatarDir: avatarDir,
		eventsHub: newChatEventHub(),
	}
	// Used for jitter when scheduling retries.
	rand.Seed(time.Now().UTC().UnixNano())
	if err := os.MkdirAll(avatarDir, 0o755); err != nil {
		return nil, fmt.Errorf("create avatar dir: %w", err)
	}
	store, err := NewStore(dbPath, s.localPeer)
	if err != nil {
		return nil, err
	}
	s.store = store
	if err := s.repairHistoricalDirectState(); err != nil {
		log.Printf("[chat] startup direct data repair failed: %v", err)
	}
	if err := s.repairOrphanPendingSessionAccepts(); err != nil {
		log.Printf("[chat] startup orphan session accept repair failed: %v", err)
	}
	h.SetStreamHandler(p2p.ProtocolChatRequest, s.handleRequestStream)
	h.SetStreamHandler(p2p.ProtocolChatMsg, s.handleMessageStream)
	h.SetStreamHandler(p2p.ProtocolChatAck, s.handleAckStream)
	h.SetStreamHandler(p2p.ProtocolChatSync, s.handleChatSyncStream)
	h.SetStreamHandler(p2p.ProtocolGroupControl, s.handleGroupControlStream)
	h.SetStreamHandler(p2p.ProtocolGroupMsg, s.handleMessageStream)
	h.SetStreamHandler(p2p.ProtocolGroupSync, s.handleGroupSyncStream)
	s.runRetentionSweep(time.Now().UTC())
	safe.Go("chat.retentionLoop", func() { s.runRetentionLoop() })
	safe.Go("chat.recoverOutboxLoop", func() { s.recoverMissingOutboxJobs() })
	safe.Go("chat.outboxRetryLoop", func() { s.runOutboxRetryLoop() })
	safe.Go("chat.groupRetryLoop", func() { s.runGroupRetryLoop() })
	safe.Go("chat.friendRequestRetryLoop", func() { s.runFriendRequestRetryLoop() })
	safe.Go("chat.relayKeepConnectedLoop", func() { s.runRelayKeepConnectedLoop() })
	return s, nil
}

func (s *Service) runRelayKeepConnectedLoop() {
	ticker := time.NewTicker(relayKeepConnectedInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.keepRelaysConnected(relayKeepConnectedMax)
		}
	}
}

func (s *Service) keepRelaysConnected(max int) {
	if s == nil || s.host == nil || s.discovery == nil || max <= 0 {
		return
	}

	// Prefer relays that are already connected, then connect warm-up ones.
	relays := s.discovery.ListRelays()
	if len(relays) == 0 {
		return
	}

	// Shuffle to avoid always dialing the same relays.
	rand.Shuffle(len(relays), func(i, j int) { relays[i], relays[j] = relays[j], relays[i] })

	connected := 0
	for _, relayDesc := range relays {
		if relayDesc == nil || relayDesc.PeerID == "" || relayDesc.PeerID == s.localPeer {
			continue
		}
		if connected >= max {
			return
		}
		pid, err := peer.Decode(relayDesc.PeerID)
		if err != nil {
			continue
		}
		if s.host.Network().Connectedness(pid) == network.Connected {
			connected++
			continue
		}
		// EnsurePeerConnected is bounded by internal timeouts.
		if err := s.ensurePeerConnected(relayDesc.PeerID); err == nil {
			connected++
		}
	}
}

func (s *Service) HandleRelayE2EStream(str network.Stream) {
	safe.Go("chat.handleRelayE2EStream", func() { s.serveRelayE2EStream(str) })
}

func (s *Service) HandleRelayE2EStreamWithHeader(str network.Stream, header tunnel.RouteHeader) {
	safe.Go("chat.handleRelayE2EStreamWithHeader", func() { s.serveRelayE2EStreamWithHeader(str, header) })
}

func (s *Service) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Close()
}

func (s *Service) GetProfile() (Profile, error) {
	p, _, err := s.store.GetProfile(s.localPeer)
	return p, err
}

func (s *Service) UpdateProfile(nickname, bio string) (Profile, error) {
	return s.store.UpdateProfile(s.localPeer, nickname, bio)
}

func (s *Service) UpdateProfileAvatar(fileName string, data []byte) (Profile, error) {
	stored, err := SaveAvatarFile(s.avatarDir, fileName, data)
	if err != nil {
		return Profile{}, err
	}
	return s.store.UpdateProfileAvatar(s.localPeer, stored)
}

func (s *Service) AvatarPath(fileName string) (string, error) {
	return AvatarPath(s.avatarDir, fileName)
}

func (s *Service) avatarExists(fileName string) bool {
	if fileName == "" {
		return false
	}
	path, err := s.AvatarPath(fileName)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func (s *Service) profileAvatarName() string {
	profile, _, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		return ""
	}
	return profile.Avatar
}

func (s *Service) persistAvatarPayload(fileName string, data []byte) (string, error) {
	fileName = NormalizeAvatarFileName(fileName)
	if len(data) == 0 {
		return fileName, nil
	}
	if path, err := AvatarPath(s.avatarDir, fileName); err == nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return fileName, nil
		}
	}
	return SaveAvatarFile(s.avatarDir, fileName, data)
}

func (s *Service) requestAvatarFetch(peerID, avatarName string) {
	avatarName = NormalizeAvatarFileName(avatarName)
	if peerID == "" || avatarName == "" {
		return
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return
	}
	if !s.peerHasActiveConnection(pid) {
		return
	}
	if !s.chatRequestProbeAllowed(peerID) {
		return
	}
	if s.avatarExists(avatarName) {
		return
	}
	key := peerID + "\x00" + avatarName
	if last, ok := s.avatarFetchSeen.Load(key); ok {
		if ts, ok := last.(time.Time); ok && time.Since(ts) < directAvatarFetchTTL {
			return
		}
	}
	s.avatarFetchSeen.Store(key, time.Now())
	safe.Go("chat.avatarFetch", func() {
		req := AvatarRequest{
			Type:       MessageTypeAvatarRequest,
			FromPeerID: s.localPeer,
			ToPeerID:   peerID,
			AvatarName: avatarName,
			SentAtUnix: time.Now().UnixMilli(),
		}
		if err := s.sendEnvelopeConnectedOnly(peerID, req); err != nil {
			if isIgnorableChatRequestError(err) {
				s.markChatRequestProbeResult(peerID, false)
			}
			if !isIgnorableChatRequestError(err) {
				log.Printf("[chat] request avatar fetch failed peer=%s avatar=%s err=%v", peerID, avatarName, err)
			}
			return
		}
		s.markChatRequestProbeResult(peerID, true)
	})
}

func (s *Service) maybeSyncProfile(peerID string) {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" || peerID == s.localPeer {
		return
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return
	}
	if !s.peerSupportsChatRequest(peerID) {
		return
	}
	if !s.peerHasActiveConnection(pid) {
		return
	}
	if !s.chatRequestProbeAllowed(peerID) {
		return
	}
	if _, err := s.store.GetConversationByPeer(peerID); err != nil {
		return
	}
	profile, err := s.GetProfile()
	if err != nil {
		return
	}
	key := peerID + "\x00" + profile.Nickname + "\x00" + profile.Bio + "\x00" + profile.Avatar
	if !s.beginProfileSync(peerID, key) {
		return
	}
	safe.Go("chat.profileSync", func() {
		wire := ProfileSync{
			Type:       MessageTypeProfileSync,
			FromPeerID: s.localPeer,
			ToPeerID:   peerID,
			Nickname:   profile.Nickname,
			Bio:        profile.Bio,
			AvatarName: profile.Avatar,
			SentAtUnix: time.Now().UnixMilli(),
		}
		if err := s.sendEnvelopeConnectedOnly(peerID, wire); err != nil {
			s.finishProfileSync(peerID, key, false, err)
			if !isIgnorableProfileSyncError(err) {
				log.Printf("[chat] send profile sync failed peer=%s err=%v", peerID, err)
			}
			return
		}
		s.finishProfileSync(peerID, key, true, nil)

		s.syncPeerAvatar(peerID)
	})
}

func (s *Service) beginProfileSync(peerID, profileKey string) bool {
	if s == nil || peerID == "" || profileKey == "" {
		return false
	}
	stateAny, _ := s.profileSyncSeen.LoadOrStore(peerID, &profileSyncState{})
	state := stateAny.(*profileSyncState)
	now := time.Now()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.inFlight {
		return false
	}
	if state.lastProfileKey == profileKey && !state.nextAttemptAt.IsZero() && now.Before(state.nextAttemptAt) {
		return false
	}
	state.inFlight = true
	state.lastProfileKey = profileKey
	return true
}

func (s *Service) finishProfileSync(peerID, profileKey string, ok bool, err error) {
	if s == nil || peerID == "" {
		return
	}
	stateAny, okLoad := s.profileSyncSeen.Load(peerID)
	if !okLoad {
		return
	}
	state, okType := stateAny.(*profileSyncState)
	if !okType || state == nil {
		return
	}
	now := time.Now()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.inFlight = false
	state.lastProfileKey = profileKey
	if ok {
		state.failures = 0
		state.nextAttemptAt = now.Add(directProfileSyncTTL)
		s.markChatRequestProbeResult(peerID, true)
		return
	}
	state.failures++
	if err != nil && isIgnorableChatRequestError(err) {
		s.markChatRequestProbeResult(peerID, false)
	}
	delay := directProfileSyncBackoff << min(state.failures-1, 6)
	if delay > directProfileSyncMaxBackoff {
		delay = directProfileSyncMaxBackoff
	}
	state.nextAttemptAt = now.Add(delay)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Service) chatRequestProbeAllowed(peerID string) bool {
	if s == nil || peerID == "" {
		return false
	}
	if last, ok := s.chatRequestProbeSeen.Load(peerID); ok {
		if probe, ok := last.(chatRequestProbeState); ok {
			if time.Since(probe.at) < directChatRequestProbeTTL && !probe.supported {
				return false
			}
			if time.Since(probe.at) < directChatRequestProbeTTL && probe.supported {
				return true
			}
		}
	}
	return true
}

func (s *Service) markChatRequestProbeResult(peerID string, supported bool) {
	if s == nil || peerID == "" {
		return
	}
	s.chatRequestProbeSeen.Store(peerID, chatRequestProbeState{
		supported: supported,
		at:        time.Now(),
	})
}

func (s *Service) peerHasActiveConnection(pid peer.ID) bool {
	if s == nil || s.host == nil || pid == "" {
		return false
	}
	return len(s.host.Network().ConnsToPeer(pid)) > 0
}

func isIgnorableChatRequestError(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sent go away") ||
		strings.Contains(msg, "transport error") ||
		strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "unknown protocol") ||
		strings.Contains(msg, "protocol not supported")
}

func isIgnorableProfileSyncError(err error) bool {
	return isIgnorableChatRequestError(err)
}

func (s *Service) peerSupportsChatRequest(peerID string) bool {
	if s == nil || s.host == nil || peerID == "" {
		return false
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return false
	}
	supported, err := s.host.Peerstore().SupportsProtocols(pid, p2p.ProtocolChatRequest)
	return err == nil && len(supported) > 0
}

func (s *Service) syncPeerAvatar(peerID string) {
	contact, err := s.store.GetPeer(peerID)
	if err != nil || contact.Avatar == "" {
		return
	}
	s.requestAvatarFetch(peerID, contact.Avatar)
}

func (s *Service) handleProfileSync(sync ProfileSync) error {
	if sync.ToPeerID != "" && sync.ToPeerID != s.localPeer {
		return nil
	}
	if sync.FromPeerID == "" {
		return nil
	}
	// 忽略偽造為本機 peer 的同步，避免寫入 peers 汙染本機顯示。
	if sync.FromPeerID == s.localPeer {
		return nil
	}
	if _, err := s.store.GetConversationByPeer(sync.FromPeerID); err != nil {
		return nil
	}
	if !s.peerSupportsChatRequest(sync.FromPeerID) {
		return nil
	}
	avatarName := NormalizeAvatarFileName(sync.AvatarName)
	if err := s.store.UpsertPeer(sync.FromPeerID, sync.Nickname, sync.Bio); err != nil {
		return err
	}
	if avatarName != "" {
		if err := s.store.UpdatePeerAvatar(sync.FromPeerID, avatarName); err != nil {
			return err
		}
		_ = s.store.UpdateRequestsAvatar(sync.FromPeerID, avatarName)
		s.requestAvatarFetch(sync.FromPeerID, avatarName)
	}
	return nil
}

func (s *Service) loadAvatarData(fileName string) ([]byte, error) {
	path, err := s.AvatarPath(fileName)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (s *Service) handleAvatarRequest(req AvatarRequest) error {
	if req.ToPeerID != "" && req.ToPeerID != s.localPeer {
		return nil
	}
	if req.FromPeerID == "" || req.AvatarName == "" {
		return nil
	}
	data, err := s.loadAvatarData(req.AvatarName)
	if err != nil || len(data) == 0 {
		return nil
	}
	resp := AvatarResponse{
		Type:       MessageTypeAvatarResponse,
		FromPeerID: s.localPeer,
		ToPeerID:   req.FromPeerID,
		AvatarName: NormalizeAvatarFileName(req.AvatarName),
		AvatarData: data,
		SentAtUnix: time.Now().UnixMilli(),
	}
	return s.sendEnvelopeConnectedOnly(req.FromPeerID, resp)
}

func (s *Service) handleAvatarResponse(resp AvatarResponse) error {
	if resp.ToPeerID != "" && resp.ToPeerID != s.localPeer {
		return nil
	}
	avatarName := NormalizeAvatarFileName(resp.AvatarName)
	if avatarName == "" {
		return nil
	}
	if len(resp.AvatarData) == 0 && !s.avatarExists(avatarName) {
		return errors.New("avatar response is empty")
	}
	avatarName, err := s.persistAvatarPayload(avatarName, resp.AvatarData)
	if err != nil || avatarName == "" {
		return err
	}
	if resp.FromPeerID == "" || resp.FromPeerID == s.localPeer {
		return nil
	}
	_ = s.store.UpsertPeer(resp.FromPeerID, "", "")
	_ = s.store.UpdatePeerAvatar(resp.FromPeerID, avatarName)
	_ = s.store.UpdateRequestsAvatar(resp.FromPeerID, avatarName)
	return nil
}

func (s *Service) ListRequests() ([]Request, error) {
	return s.store.ListRequests(s.localPeer)
}

func (s *Service) ListConversations() ([]Conversation, error) {
	return s.store.ListConversations()
}

func (s *Service) UpdateConversationRetention(conversationID string, minutes int) (Conversation, error) {
	conv, err := s.store.GetConversation(conversationID)
	if err != nil {
		return Conversation{}, err
	}
	update := RetentionUpdate{
		Type:             MessageTypeRetentionUpdate,
		ConversationID:   conversationID,
		FromPeerID:       s.localPeer,
		ToPeerID:         conv.PeerID,
		RetentionMinutes: minutes,
		UpdatedAtUnix:    time.Now().UnixMilli(),
	}
	if err := s.sendEnvelope(conv.PeerID, update); err != nil {
		return Conversation{}, err
	}
	updated, err := s.store.UpdateConversationRetention(conversationID, minutes)
	if err != nil {
		return Conversation{}, err
	}
	if err := s.store.UpdateConversationRetentionSync(conversationID, "pending", time.Time{}); err != nil {
		return Conversation{}, err
	}
	// Notify websocket clients so they can refresh message list (messages may be deleted).
	s.publishChatEvent(newMessageEvent("direct", updated.ConversationID, "", MessageTypeRetentionUpdate))

	return s.store.GetConversation(updated.ConversationID)
}

func (s *Service) ListContacts() ([]Contact, error) {
	return s.store.ListContacts()
}

// DeleteConversationLocal removes the conversation and all local messages/cursors only (no network).
func (s *Service) DeleteConversationLocal(conversationID string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return errors.New("conversation_id is empty")
	}
	conv, err := s.store.GetConversation(conversationID)
	if err != nil {
		return err
	}
	if conv.PeerID == s.localPeer {
		return errors.New("invalid conversation")
	}
	if err := s.store.DeleteConversationData(conversationID); err != nil {
		return err
	}
	s.publishChatEvent(newConversationDeletedEvent(conversationID, conv.PeerID))
	return nil
}

// DeleteContactLocal deletes local conversations with that peer + their messages, then removes the peer and friend requests.
func (s *Service) DeleteContactLocal(peerID string) error {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return errors.New("peer_id is empty")
	}
	if peerID == s.localPeer {
		return errors.New("cannot delete self")
	}
	for {
		conv, err := s.store.GetConversationByPeer(peerID)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if err := s.store.DeleteConversationData(conv.ConversationID); err != nil {
			return err
		}
	}
	if err := s.store.DeletePeerAndRelatedData(peerID); err != nil {
		return err
	}
	s.publishChatEvent(newContactDeletedEvent(peerID))
	return nil
}

func (s *Service) UpdateContactNickname(peerID, nickname string) (Contact, error) {
	return s.store.UpdatePeerNickname(peerID, nickname)
}

func (s *Service) SetContactBlocked(peerID string, blocked bool) (Contact, error) {
	return s.store.SetPeerBlocked(peerID, blocked)
}

func (s *Service) ListMessages(conversationID string) ([]Message, error) {
	return s.store.ListMessages(conversationID)
}

func (s *Service) ListMessagesPage(conversationID string, limit, offset int) ([]Message, int, error) {
	return s.store.ListMessagesPage(conversationID, limit, offset)
}

func (s *Service) SendFile(conversationID, fileName, mimeType string, data []byte) (Message, error) {
	if len(data) == 0 {
		return Message{}, errors.New("file is empty")
	}
	if len(data) > MaxChatFileBytes {
		return Message{}, fmt.Errorf("file too large: max %d bytes", MaxChatFileBytes)
	}
	fileName = NormalizeChatFileName(fileName)
	conv, err := s.store.GetConversation(conversationID)
	if err != nil {
		return Message{}, err
	}
	if contact, err := s.store.GetPeer(conv.PeerID); err == nil && contact.Blocked {
		return Message{}, errors.New("peer is blocked")
	}
	sess, err := s.store.GetSessionState(conversationID)
	if err != nil {
		return Message{}, err
	}
	counter := sess.SendCounter
	nonce := protocol.BuildAEADNonce("fwd", counter)
	aad := []byte(conversationID + "\x00chat_file")
	ciphertext, err := protocol.AEADSeal(sess.SendKey, nonce, data, aad)
	if err != nil {
		return Message{}, err
	}
	msg := Message{
		MsgID:          uuid.NewString(),
		ConversationID: conversationID,
		SenderPeerID:   s.localPeer,
		ReceiverPeerID: conv.PeerID,
		Direction:      "outbound",
		MsgType:        MessageTypeChatFile,
		FileName:       fileName,
		MIMEType:       mimeType,
		FileSize:       int64(len(data)),
		TransportMode:  TransportModeDirect,
		State:          MessageStateLocalOnly,
		Counter:        counter,
		CreatedAt:      time.Now().UTC(),
	}
	msg, err = s.store.AddMessage(msg, data)
	if err != nil {
		return Message{}, err
	}
	if err := s.store.UpdateSendCounter(conversationID, counter+1); err != nil {
		return Message{}, err
	}
	if err := s.store.UpsertOutboxJob(msg.MsgID, conv.PeerID, MessageStateQueuedForRetry, 0, time.Now().UTC(), time.Time{}); err != nil {
		return Message{}, err
	}
	if err := s.sendStoredDirectMessage(msg, data, ciphertext); err != nil {
		msg.State = MessageStateQueuedForRetry
		if _, updateErr := s.store.AddMessage(msg, data); updateErr != nil {
			return Message{}, updateErr
		}
		if retryErr := s.scheduleOutboxRetry(msg.MsgID, conv.PeerID, 1); retryErr != nil {
			return Message{}, retryErr
		}
		return msg, nil
	}
	msg.State = MessageStateSentToTransport
	if _, err := s.store.AddMessage(msg, data); err != nil {
		return Message{}, err
	}
	if err := s.markOutboxSentToTransport(msg.MsgID, conv.PeerID, 0, time.Now().UTC()); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func (s *Service) RevokeMessage(conversationID, msgID string) error {
	msg, err := s.store.GetMessage(msgID)
	if err != nil {
		return err
	}
	if msg.ConversationID != conversationID {
		return errors.New("message does not belong to conversation")
	}
	if msg.State == MessageStateLocalOnly || msg.State == MessageStateQueuedForRetry {
		_ = s.store.DeleteOutboxJob(msgID)
		return s.store.DeleteMessage(conversationID, msgID)
	}
	if msg.Direction != "outbound" || msg.SenderPeerID != s.localPeer {
		return errors.New("only outbound local messages can be revoked")
	}
	revoke := MessageRevoke{
		Type:           MessageTypeMessageRevoke,
		ConversationID: conversationID,
		MsgID:          msgID,
		FromPeerID:     s.localPeer,
		ToPeerID:       msg.ReceiverPeerID,
		RevokedAtUnix:  time.Now().UnixMilli(),
	}
	if err := s.sendEnvelope(msg.ReceiverPeerID, revoke); err == nil {
		_ = s.store.DeleteOutboxJob(msgID)
		return s.store.DeleteMessage(conversationID, msgID)
	}
	if err := s.store.QueueMessageRevoke(conversationID, msg.ReceiverPeerID, msgID); err != nil {
		return err
	}
	return nil
}

func (s *Service) GetMessageFile(conversationID, msgID string) (Message, []byte, error) {
	msg, err := s.store.GetMessage(msgID)
	if err != nil {
		return Message{}, nil, err
	}
	if msg.ConversationID != conversationID {
		return Message{}, nil, errors.New("message does not belong to conversation")
	}
	if msg.MsgType != MessageTypeChatFile {
		return Message{}, nil, errors.New("message is not a file")
	}
	blob, err := s.store.GetMessageBlob(msgID)
	if err != nil {
		return Message{}, nil, err
	}
	return msg, blob, nil
}

func (s *Service) SendRequest(toPeerID, introText string) (Request, error) {
	targetAvatar := ""
	if contact, err := s.store.GetPeer(toPeerID); err == nil {
		if contact.Blocked {
			return Request{}, errors.New("peer is blocked")
		}
		targetAvatar = contact.Avatar
	}
	profile, _, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		return Request{}, err
	}
	avatarName := profile.Avatar
	retentionMinutes := 0
	if conv, err := s.store.GetConversationByPeer(toPeerID); err == nil {
		retentionMinutes = conv.RetentionMinutes
	}
	now := time.Now().UTC()
	req := Request{
		RequestID:         uuid.NewString(),
		FromPeerID:        s.localPeer,
		ToPeerID:          toPeerID,
		State:             RequestStatePending,
		IntroText:         strings.TrimSpace(introText),
		Nickname:          profile.Nickname,
		Bio:               profile.Bio,
		Avatar:            targetAvatar,
		RetentionMinutes:  retentionMinutes,
		LastTransportMode: TransportModeDirect,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	wire := SessionRequest{
		Type:             MessageTypeSessionRequest,
		RequestID:        req.RequestID,
		FromPeerID:       req.FromPeerID,
		ToPeerID:         req.ToPeerID,
		Nickname:         profile.Nickname,
		Bio:              profile.Bio,
		AvatarName:       avatarName,
		RetentionMinutes: retentionMinutes,
		IntroText:        req.IntroText,
		ChatKexPub:       profile.ChatKexPub,
		SentAtUnix:       now.UnixMilli(),
	}
	req.RemoteChatKexPub = profile.ChatKexPub
	if err := s.store.SaveOutgoingRequest(req); err != nil {
		return Request{}, err
	}
	_ = s.store.UpsertPeer(toPeerID, "", "")
	if err := s.sendEnvelope(toPeerID, wire); err != nil {
		// Can't reach the friend right now: persist + retry with backoff for up to 1 week.
		log.Printf("[chat] send friend request queued request=%s peer=%s err=%v", req.RequestID, toPeerID, err)
		if retryErr := s.scheduleFriendRequestRetry(req.RequestID, toPeerID, 0); retryErr != nil {
			log.Printf("[chat] schedule friend request retry failed request=%s peer=%s err=%v", req.RequestID, toPeerID, retryErr)
		}
	}
	return s.store.GetRequest(req.RequestID)
}

func (s *Service) AcceptRequest(requestID string) (Conversation, error) {
	req, err := s.store.GetRequest(requestID)
	if err != nil {
		return Conversation{}, err
	}
	if req.ToPeerID != s.localPeer {
		return Conversation{}, errors.New("request is not addressed to local peer")
	}
	profile, priv, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		return Conversation{}, err
	}
	avatarName := profile.Avatar
	retentionMinutes := req.RetentionMinutes
	if existingConv, err := s.store.GetConversationByPeer(req.FromPeerID); err == nil {
		targetConvID := deriveStableConversationID(s.localPeer, req.FromPeerID)
		if existingConv.RetentionMinutes > retentionMinutes {
			retentionMinutes = existingConv.RetentionMinutes
		}
		if existingConv.ConversationID != targetConvID {
			if err := s.store.MigrateConversationID(existingConv.ConversationID, targetConvID); err != nil {
				return Conversation{}, err
			}
			existingConv, err = s.store.GetConversation(targetConvID)
			if err != nil {
				return Conversation{}, err
			}
		}
		sess, err := deriveSessionState(targetConvID, s.localPeer, req.FromPeerID, priv, req.RemoteChatKexPub)
		if err != nil {
			return Conversation{}, err
		}
		updatedConv, err := s.store.CreateConversation(Conversation{
			ConversationID:    targetConvID,
			PeerID:            req.FromPeerID,
			State:             ConversationStateActive,
			LastTransportMode: TransportModeDirect,
			RetentionMinutes:  retentionMinutes,
			CreatedAt:         existingConv.CreatedAt,
			UpdatedAt:         time.Now(),
		}, sess)
		if err != nil {
			return Conversation{}, err
		}
		existingConv = updatedConv
		if err := s.store.UpdateRequestState(requestID, RequestStateAccepted, existingConv.ConversationID); err != nil {
			return Conversation{}, err
		}
		_ = s.store.UpsertPeer(req.FromPeerID, req.Nickname, req.Bio)
		// retentionMinutes already applied in CreateConversation.
		if err := s.ensurePeerConnected(req.FromPeerID); err == nil {
			wire := SessionAccept{
				Type:             MessageTypeSessionAccept,
				RequestID:        req.RequestID,
				ConversationID:   targetConvID,
				FromPeerID:       s.localPeer,
				ToPeerID:         req.FromPeerID,
				Bio:              profile.Bio,
				AvatarName:       avatarName,
				RetentionMinutes: retentionMinutes,
				ChatKexPub:       profile.ChatKexPub,
				SentAtUnix:       time.Now().UnixMilli(),
			}
			if err := s.sendEnvelope(req.FromPeerID, wire); err != nil {
				log.Printf("[chat] send accept for existing conversation failed request=%s err=%v", requestID, err)
				// Schedule retry for up to friendRequestRetryDeadline.
				if retryErr := s.scheduleFriendRequestRetry(req.RequestID, req.FromPeerID, 0); retryErr != nil {
					log.Printf("[chat] schedule friend accept retry failed request=%s peer=%s err=%v", requestID, req.FromPeerID, retryErr)
				}
			} else {
				// Wait for SessionAcceptAck: sender side keep retry job alive until receiver confirms.
				_ = s.scheduleFriendRequestRetry(req.RequestID, req.FromPeerID, 0)
			}
		} else {
			// Can't connect now: schedule accept retry.
			if retryErr := s.scheduleFriendRequestRetry(req.RequestID, req.FromPeerID, 0); retryErr != nil {
				log.Printf("[chat] schedule friend accept retry (existing conv, connect failed) request=%s peer=%s err=%v", requestID, req.FromPeerID, retryErr)
			}
		}
		return existingConv, nil
	} else if err != sql.ErrNoRows {
		return Conversation{}, err
	}
	convID := deriveStableConversationID(s.localPeer, req.FromPeerID)
	sess, err := deriveSessionState(convID, s.localPeer, req.FromPeerID, priv, req.RemoteChatKexPub)
	if err != nil {
		return Conversation{}, err
	}
	conv := Conversation{
		ConversationID:    convID,
		PeerID:            req.FromPeerID,
		State:             ConversationStateActive,
		LastTransportMode: TransportModeDirect,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	conv, err = s.store.CreateConversation(conv, sess)
	if err != nil {
		return Conversation{}, err
	}
	if retentionMinutes != conv.RetentionMinutes {
		if updatedConv, err := s.store.UpdateConversationRetention(conv.ConversationID, retentionMinutes); err == nil {
			conv = updatedConv
		}
	}
	if err := s.store.UpdateRequestState(requestID, RequestStateAccepted, convID); err != nil {
		return Conversation{}, err
	}
	_ = s.store.UpsertPeer(req.FromPeerID, req.Nickname, req.Bio)
	if err := s.ensurePeerConnected(req.FromPeerID); err != nil {
		// Connection not currently available: schedule retry of SessionAccept.
		if retryErr := s.scheduleFriendRequestRetry(req.RequestID, req.FromPeerID, 0); retryErr != nil {
			log.Printf("[chat] schedule friend accept retry (connect failed) request=%s peer=%s err=%v", requestID, req.FromPeerID, retryErr)
		}
		return conv, nil
	}
	wire := SessionAccept{
		Type:             MessageTypeSessionAccept,
		RequestID:        req.RequestID,
		ConversationID:   convID,
		FromPeerID:       s.localPeer,
		ToPeerID:         req.FromPeerID,
		Bio:              profile.Bio,
		AvatarName:       avatarName,
		RetentionMinutes: retentionMinutes,
		ChatKexPub:       profile.ChatKexPub,
		SentAtUnix:       time.Now().UnixMilli(),
	}
	if err := s.sendEnvelope(req.FromPeerID, wire); err != nil {
		log.Printf("[chat] send accept failed request=%s err=%v", requestID, err)
		// Schedule retry for up to friendRequestRetryDeadline.
		if retryErr := s.scheduleFriendRequestRetry(req.RequestID, req.FromPeerID, 0); retryErr != nil {
			log.Printf("[chat] schedule friend accept retry failed request=%s peer=%s err=%v", requestID, req.FromPeerID, retryErr)
		}
	} else {
		// Wait for SessionAcceptAck: sender side keep retry job alive until receiver confirms.
		_ = s.scheduleFriendRequestRetry(req.RequestID, req.FromPeerID, 0)
	}
	return conv, nil
}

func (s *Service) RejectRequest(requestID string) error {
	req, err := s.store.GetRequest(requestID)
	if err != nil {
		return err
	}
	if req.ToPeerID != s.localPeer {
		return errors.New("request is not addressed to local peer")
	}
	if err := s.store.UpdateRequestState(requestID, RequestStateRejected, ""); err != nil {
		return err
	}
	if err := s.ensurePeerConnected(req.FromPeerID); err != nil {
		return nil
	}
	wire := SessionReject{
		Type:       MessageTypeSessionReject,
		RequestID:  requestID,
		FromPeerID: s.localPeer,
		ToPeerID:   req.FromPeerID,
		SentAtUnix: time.Now().UnixMilli(),
	}
	if err := s.sendEnvelope(req.FromPeerID, wire); err != nil {
		log.Printf("[chat] send reject failed request=%s err=%v", requestID, err)
	}
	return nil
}

func (s *Service) SendText(conversationID, text string) (Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Message{}, errors.New("message text is empty")
	}
	conv, err := s.store.GetConversation(conversationID)
	if err != nil {
		return Message{}, err
	}
	if contact, err := s.store.GetPeer(conv.PeerID); err == nil && contact.Blocked {
		return Message{}, errors.New("peer is blocked")
	}
	sess, err := s.store.GetSessionState(conversationID)
	if err != nil {
		return Message{}, err
	}
	counter := sess.SendCounter
	nonce := protocol.BuildAEADNonce("fwd", counter)
	aad := []byte(conversationID + "\x00chat_text")
	ciphertext, err := protocol.AEADSeal(sess.SendKey, nonce, []byte(text), aad)
	if err != nil {
		return Message{}, err
	}
	msg := Message{
		MsgID:          uuid.NewString(),
		ConversationID: conversationID,
		SenderPeerID:   s.localPeer,
		ReceiverPeerID: conv.PeerID,
		Direction:      "outbound",
		MsgType:        MessageTypeChatText,
		Plaintext:      text,
		TransportMode:  TransportModeDirect,
		State:          MessageStateLocalOnly,
		Counter:        counter,
		CreatedAt:      time.Now().UTC(),
	}
	msg, err = s.store.AddMessage(msg, ciphertext)
	if err != nil {
		return Message{}, err
	}
	if err := s.store.UpdateSendCounter(conversationID, counter+1); err != nil {
		return Message{}, err
	}
	if err := s.store.UpsertOutboxJob(msg.MsgID, conv.PeerID, MessageStateQueuedForRetry, 0, time.Now().UTC(), time.Time{}); err != nil {
		return Message{}, err
	}
	if err := s.sendStoredDirectMessage(msg, ciphertext, nil); err != nil {
		msg.State = MessageStateQueuedForRetry
		if _, updateErr := s.store.AddMessage(msg, ciphertext); updateErr != nil {
			return Message{}, updateErr
		}
		if retryErr := s.scheduleOutboxRetry(msg.MsgID, conv.PeerID, 1); retryErr != nil {
			return Message{}, retryErr
		}
		return msg, nil
	}
	msg.State = MessageStateSentToTransport
	if _, err := s.store.AddMessage(msg, ciphertext); err != nil {
		return Message{}, err
	}
	if err := s.markOutboxSentToTransport(msg.MsgID, conv.PeerID, 0, time.Now().UTC()); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func (s *Service) SyncConversation(conversationID string) error {
	conv, err := s.store.GetConversation(conversationID)
	if err != nil {
		return err
	}
	sess, err := s.store.GetSessionState(conversationID)
	if err != nil {
		return err
	}
	req := ChatSyncRequest{
		Type:           MessageTypeChatSyncRequest,
		ConversationID: conversationID,
		FromPeerID:     s.localPeer,
		ToPeerID:       conv.PeerID,
		NextCounter:    sess.RecvCounter,
		SentAtUnix:     time.Now().UTC().UnixMilli(),
	}
	resp, err := s.requestChatSync(conv.PeerID, req)
	if err != nil {
		return err
	}
	return s.applyChatSyncResponse(conversationID, resp)
}

func (s *Service) NetworkStatus() map[string]any {
	out := map[string]any{
		"local_peer_id":   s.localPeer,
		"connected_peers": len(s.host.Network().Peers()),
	}

	// Provide which relay peers are currently connected so the console can display it.
	if s.discovery != nil {
		connectedRelays := make([]string, 0)
		for _, relayDesc := range s.discovery.ListRelays() {
			if relayDesc == nil || relayDesc.PeerID == "" || relayDesc.PeerID == s.localPeer {
				continue
			}
			pid, err := peer.Decode(relayDesc.PeerID)
			if err != nil {
				continue
			}
			if s.host.Network().Connectedness(pid) == network.Connected {
				connectedRelays = append(connectedRelays, relayDesc.PeerID)
			}
		}
		out["connected_relays"] = connectedRelays
	}

	return out
}

func (s *Service) PeerStatus(peerID string) (map[string]any, error) {
	pid, err := peer.Decode(peerID)
	if err != nil {
		return nil, err
	}
	addrs := s.host.Peerstore().Addrs(pid)
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return map[string]any{
		"peer_id":       peerID,
		"connectedness": s.host.Network().Connectedness(pid).String(),
		"known_addrs":   out,
	}, nil
}

func (s *Service) ConnectPeer(peerID string) error {
	if err := s.ensurePeerConnected(peerID); err != nil {
		return err
	}
	s.OnPeerConnected(peerID)
	return nil
}

func (s *Service) OnPeerDiscovered(peerID string) {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" || peerID == s.localPeer {
		return
	}
	if last, ok := s.autoConnectSeen.Load(peerID); ok {
		if lastTime, ok := last.(time.Time); ok && time.Since(lastTime) < directAutoConnectTTL {
			return
		}
	}
	s.autoConnectSeen.Store(peerID, time.Now())
	safe.Go("chat.onPeerDiscovered", func() {
		conv, err := s.store.GetConversationByPeer(peerID)
		if err != nil || conv.State != ConversationStateActive {
			return
		}
		contact, err := s.store.GetPeer(peerID)
		if err == nil && contact.Blocked {
			return
		}
		if err := s.ensurePeerConnected(peerID); err != nil {
			log.Printf("[chat] auto-connect discovered peer=%s failed: %v", peerID, err)
			return
		}
		s.OnPeerConnected(peerID)
	})
}

func (s *Service) OnPeerConnected(peerID string) {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" || peerID == s.localPeer {
		return
	}
	safe.Go("chat.onPeerConnected", func() {
		if err := s.sendPendingMessageRevokes(peerID); err != nil {
			log.Printf("[chat] send pending revokes on connect peer=%s failed: %v", peerID, err)
		}
		if err := s.recoverMissingOutboxJobs(); err != nil {
			log.Printf("[chat] recover outbox on connect peer=%s failed: %v", peerID, err)
			return
		}
		items, err := s.store.ListOutboxJobsForPeer(peerID, directRetryBatchSize)
		if err != nil {
			log.Printf("[chat] list outbox for peer=%s failed: %v", peerID, err)
			return
		}
		for _, item := range items {
			if item.SenderPeerID != s.localPeer {
				continue
			}
			if err := s.retryOutboxJob(item); err != nil {
				log.Printf("[chat] immediate retry on connect msg=%s peer=%s failed: %v", item.MsgID, item.PeerID, err)
			}
		}
		s.maybeSyncProfile(peerID)
		conv, err := s.store.GetConversationByPeer(peerID)
		if err == nil {
			if err := s.SyncConversation(conv.ConversationID); err != nil {
				log.Printf("[chat] sync on connect conversation=%s peer=%s failed: %v", conv.ConversationID, peerID, err)
			}
		}
	})
}

func (s *Service) sendPendingMessageRevokes(peerID string) error {
	items, err := s.store.ListMessageRevokeJobsForPeer(peerID, directRetryBatchSize)
	if err != nil {
		return err
	}
	for _, item := range items {
		revoke := MessageRevoke{
			Type:           MessageTypeMessageRevoke,
			ConversationID: item.ConversationID,
			MsgID:          item.MsgID,
			FromPeerID:     s.localPeer,
			ToPeerID:       peerID,
			RevokedAtUnix:  time.Now().UTC().UnixMilli(),
		}
		if err := s.sendEnvelope(peerID, revoke); err != nil {
			return err
		}
		if err := s.store.DeleteMessageRevokeJob(item.MsgID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) handleRequestStream(str network.Stream) {
	safe.Go("chat.handleRequestStream", func() { s.serveRequestStream(str) })
}

func (s *Service) handleMessageStream(str network.Stream) {
	safe.Go("chat.handleMessageStream", func() { s.serveMessageStream(str) })
}

func (s *Service) handleAckStream(str network.Stream) {
	safe.Go("chat.handleAckStream", func() { s.serveAckStream(str) })
}

func (s *Service) handleChatSyncStream(str network.Stream) {
	safe.Go("chat.handleChatSyncStream", func() { s.serveChatSyncStream(str) })
}

func (s *Service) handleGroupControlStream(str network.Stream) {
	safe.Go("chat.handleGroupControlStream", func() { s.serveGroupControlStream(str) })
}

func (s *Service) handleGroupSyncStream(str network.Stream) {
	safe.Go("chat.handleGroupSyncStream", func() { s.serveGroupSyncStream(str) })
}

func (s *Service) serveRequestStream(str network.Stream) {
	defer str.Close()
	var env map[string]any
	if err := tunnel.ReadJSONFrame(str, &env); err != nil {
		return
	}
	switch env["type"] {
	case MessageTypeSessionRequest:
		var req SessionRequest
		if err := remarshal(env, &req); err != nil {
			return
		}
		if err := s.handleIncomingSessionRequestEnvelope(req, TransportModeDirect, true); err != nil {
			log.Printf("[chat] save request failed: %v", err)
		}
	case MessageTypeSessionAccept:
		var accept SessionAccept
		if err := remarshal(env, &accept); err != nil {
			return
		}
		if err := s.handleIncomingSessionAcceptEnvelope(accept, true, true); err != nil {
			log.Printf("[chat] handle session accept failed request=%s peer=%s err=%v", accept.RequestID, accept.FromPeerID, err)
		}
	case MessageTypeSessionReject:
		var reject SessionReject
		if err := remarshal(env, &reject); err != nil {
			return
		}
		if err := s.handleIncomingSessionRejectEnvelope(reject, true); err != nil {
			log.Printf("[chat] handle session reject failed request=%s peer=%s err=%v", reject.RequestID, reject.FromPeerID, err)
		}
	case MessageTypeSessionAcceptAck:
		var ack SessionAcceptAck
		if err := remarshal(env, &ack); err != nil {
			return
		}
		if err := s.handleIncomingSessionAcceptAckEnvelope(ack); err != nil {
			log.Printf("[chat] handle session accept ack failed request=%s err=%v", ack.RequestID, err)
		}
	case MessageTypeProfileSync:
		var sync ProfileSync
		if err := remarshal(env, &sync); err != nil {
			return
		}
		if err := s.handleProfileSync(sync); err != nil {
			log.Printf("[chat] handle profile sync failed peer=%s err=%v", sync.FromPeerID, err)
		}
	case MessageTypeAvatarRequest:
		var req AvatarRequest
		if err := remarshal(env, &req); err != nil {
			return
		}
		safe.Go("chat.avatarRequest", func() {
			if err := s.handleAvatarRequest(req); err != nil {
				log.Printf("[chat] handle avatar request failed peer=%s avatar=%s err=%v", req.FromPeerID, req.AvatarName, err)
			}
		})
	case MessageTypeAvatarResponse:
		var resp AvatarResponse
		if err := remarshal(env, &resp); err != nil {
			return
		}
		if err := s.handleAvatarResponse(resp); err != nil {
			log.Printf("[chat] handle avatar response failed peer=%s avatar=%s err=%v", resp.FromPeerID, resp.AvatarName, err)
		}
	}
}

// errChatEnvelopeIgnored 表示控制面封包被丢弃（伪造、未知 request_id、或对端不匹配）。
var errChatEnvelopeIgnored = errors.New("chat: envelope ignored")

// validateFriendRequestEnvelope 校验 SessionAccept/SessionReject 是否对应本地 requests 表中的同一条好友请求，
// 且 from_peer_id 必须是该请求中的「对端」peer（防止伪造 request_id 建会话或改状态）。
func (s *Service) validateFriendRequestEnvelope(requestID, fromPeerID string) (Request, error) {
	if requestID == "" || fromPeerID == "" {
		return Request{}, errChatEnvelopeIgnored
	}
	if fromPeerID == s.localPeer {
		return Request{}, errChatEnvelopeIgnored
	}
	reqRow, err := s.store.GetRequest(requestID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Request{}, errChatEnvelopeIgnored
		}
		return Request{}, err
	}
	if peer := s.peerIDForRequest(reqRow); peer == "" || peer != fromPeerID {
		log.Printf("[chat] friend request envelope ignored: peer mismatch request=%s want=%s got=%s", requestID, peer, fromPeerID)
		return Request{}, errChatEnvelopeIgnored
	}
	return reqRow, nil
}

// validateSessionAcceptAck 校验 SessionAcceptAck：必须存在对应 requests 行，且发送方 from_peer_id
// 必须为该请求中的对端；可选校验 conversation_id 一致（双方均非空时）。
func (s *Service) validateSessionAcceptAck(ack SessionAcceptAck) (Request, error) {
	if ack.RequestID == "" {
		return Request{}, errChatEnvelopeIgnored
	}
	if ack.FromPeerID == "" || ack.FromPeerID == s.localPeer {
		return Request{}, errChatEnvelopeIgnored
	}
	reqRow, err := s.store.GetRequest(ack.RequestID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Request{}, errChatEnvelopeIgnored
		}
		return Request{}, err
	}
	if peer := s.peerIDForRequest(reqRow); peer == "" || peer != ack.FromPeerID {
		log.Printf("[chat] session accept ack ignored: peer mismatch request=%s want=%s got=%s", ack.RequestID, peer, ack.FromPeerID)
		return Request{}, errChatEnvelopeIgnored
	}
	if ack.ConversationID != "" && reqRow.ConversationID != "" && reqRow.ConversationID != ack.ConversationID {
		log.Printf("[chat] session accept ack ignored: conversation mismatch request=%s want=%s got=%s", ack.RequestID, reqRow.ConversationID, ack.ConversationID)
		return Request{}, errChatEnvelopeIgnored
	}
	return reqRow, nil
}

func (s *Service) handleIncomingSessionRequestEnvelope(req SessionRequest, transportMode string, publishEvent bool) error {
	if req.FromPeerID == "" || req.FromPeerID == s.localPeer {
		return nil
	}
	if req.ToPeerID != "" && req.ToPeerID != s.localPeer {
		return nil
	}
	if contact, err := s.store.GetPeer(req.FromPeerID); err == nil && contact.Blocked {
		return nil
	}
	avatarName := NormalizeAvatarFileName(req.AvatarName)
	stored := Request{
		RequestID:         req.RequestID,
		FromPeerID:        req.FromPeerID,
		ToPeerID:          req.ToPeerID,
		State:             RequestStatePending,
		IntroText:         req.IntroText,
		Nickname:          req.Nickname,
		Bio:               req.Bio,
		Avatar:            avatarName,
		RetentionMinutes:  req.RetentionMinutes,
		RemoteChatKexPub:  req.ChatKexPub,
		LastTransportMode: transportMode,
		CreatedAt:         time.UnixMilli(req.SentAtUnix),
		UpdatedAt:         time.Now(),
	}
	_ = s.store.UpsertPeer(req.FromPeerID, req.Nickname, req.Bio)
	if avatarName != "" {
		_ = s.store.UpdatePeerAvatar(req.FromPeerID, avatarName)
		s.requestAvatarFetch(req.FromPeerID, avatarName)
	}
	if err := s.store.UpsertIncomingRequest(stored); err != nil {
		return err
	}
	if publishEvent {
		s.publishChatEvent(newFriendRequestEvent(
			RequestStatePending,
			stored.RequestID,
			stored.FromPeerID,
			stored.ToPeerID,
			"",
		))
	}
	return nil
}

func (s *Service) handleIncomingSessionAcceptEnvelope(accept SessionAccept, publishEvent, sendAck bool) error {
	if accept.ToPeerID != "" && accept.ToPeerID != s.localPeer {
		return nil
	}
	if accept.FromPeerID == "" || accept.FromPeerID == s.localPeer {
		return nil
	}
	if _, err := s.validateFriendRequestEnvelope(accept.RequestID, accept.FromPeerID); err != nil {
		if errors.Is(err, errChatEnvelopeIgnored) {
			return nil
		}
		return err
	}
	avatarName := NormalizeAvatarFileName(accept.AvatarName)
	// Do not persist RemoteChatKexPub until session + conversation are created successfully.
	// Early writes caused pending requests with no conversation if deriveSessionState failed.
	if existingConv, err := s.store.GetConversationByPeer(accept.FromPeerID); err == nil {
		targetConvID := deriveStableConversationID(s.localPeer, accept.FromPeerID)
		_ = s.store.UpsertPeer(accept.FromPeerID, "", accept.Bio)
		if avatarName != "" {
			_ = s.store.UpdatePeerAvatar(accept.FromPeerID, avatarName)
			_ = s.store.UpdateRequestAvatar(accept.RequestID, avatarName)
			s.requestAvatarFetch(accept.FromPeerID, avatarName)
		}
		targetRetention := accept.RetentionMinutes
		if existingConv.RetentionMinutes > targetRetention {
			targetRetention = existingConv.RetentionMinutes
		}
		_, priv, err := s.store.GetProfile(s.localPeer)
		if err != nil {
			return err
		}
		if existingConv.ConversationID != targetConvID {
			if err := s.store.MigrateConversationID(existingConv.ConversationID, targetConvID); err != nil {
				return err
			}
			existingConv, err = s.store.GetConversation(targetConvID)
			if err != nil {
				return err
			}
		}
		sess, err := deriveSessionState(targetConvID, s.localPeer, accept.FromPeerID, priv, accept.ChatKexPub)
		if err != nil {
			return err
		}
		existingConv, err = s.store.CreateConversation(Conversation{
			ConversationID:    targetConvID,
			PeerID:            accept.FromPeerID,
			State:             ConversationStateActive,
			LastTransportMode: TransportModeDirect,
			RetentionMinutes:  targetRetention,
			CreatedAt:         existingConv.CreatedAt,
			UpdatedAt:         time.Now(),
		}, sess)
		if err != nil {
			return err
		}
		if strings.TrimSpace(accept.ChatKexPub) != "" {
			_ = s.store.UpdateRequestRemoteChatKexPub(accept.RequestID, accept.ChatKexPub)
		}
		if err := s.store.UpdateRequestState(accept.RequestID, RequestStateAccepted, targetConvID); err != nil {
			return err
		}
		_ = s.store.DeleteFriendRequestJob(accept.RequestID)
		if publishEvent {
			s.publishChatEvent(newFriendRequestEvent(
				RequestStateAccepted,
				accept.RequestID,
				accept.FromPeerID,
				accept.ToPeerID,
				targetConvID,
			))
		}
		if sendAck {
			_ = s.sendEnvelope(accept.FromPeerID, SessionAcceptAck{
				Type:           MessageTypeSessionAcceptAck,
				RequestID:      accept.RequestID,
				ConversationID: targetConvID,
				FromPeerID:     s.localPeer,
				ToPeerID:       accept.FromPeerID,
				SentAtUnix:     time.Now().UTC().UnixMilli(),
			})
		}
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	_, priv, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		return err
	}
	existingConv, err := s.store.GetConversationByPeer(accept.FromPeerID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	convID := deriveStableConversationID(s.localPeer, accept.FromPeerID)
	if err == nil && existingConv.ConversationID != convID {
		if err := s.store.MigrateConversationID(existingConv.ConversationID, convID); err != nil {
			return err
		}
		existingConv, err = s.store.GetConversation(convID)
		if err != nil {
			return err
		}
	}
	sess, err := deriveSessionState(convID, s.localPeer, accept.FromPeerID, priv, accept.ChatKexPub)
	if err != nil {
		return err
	}
	conv := Conversation{
		ConversationID:    convID,
		PeerID:            accept.FromPeerID,
		State:             ConversationStateActive,
		LastTransportMode: TransportModeDirect,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	if _, err := s.store.CreateConversation(conv, sess); err != nil {
		return err
	}
	_ = s.store.UpsertPeer(accept.FromPeerID, "", accept.Bio)
	if avatarName != "" {
		_ = s.store.UpdatePeerAvatar(accept.FromPeerID, avatarName)
		_ = s.store.UpdateRequestAvatar(accept.RequestID, avatarName)
		s.requestAvatarFetch(accept.FromPeerID, avatarName)
	}
	if accept.RetentionMinutes > 0 {
		if _, err := s.store.UpdateConversationRetention(convID, accept.RetentionMinutes); err != nil {
			return err
		}
	}
	if strings.TrimSpace(accept.ChatKexPub) != "" {
		_ = s.store.UpdateRequestRemoteChatKexPub(accept.RequestID, accept.ChatKexPub)
	}
	if err := s.store.UpdateRequestState(accept.RequestID, RequestStateAccepted, convID); err != nil {
		return err
	}
	_ = s.store.DeleteFriendRequestJob(accept.RequestID)
	if publishEvent {
		s.publishChatEvent(newFriendRequestEvent(
			RequestStateAccepted,
			accept.RequestID,
			accept.FromPeerID,
			accept.ToPeerID,
			convID,
		))
	}
	if sendAck {
		_ = s.sendEnvelope(accept.FromPeerID, SessionAcceptAck{
			Type:           MessageTypeSessionAcceptAck,
			RequestID:      accept.RequestID,
			ConversationID: convID,
			FromPeerID:     s.localPeer,
			ToPeerID:       accept.FromPeerID,
			SentAtUnix:     time.Now().UTC().UnixMilli(),
		})
	}
	return nil
}

func (s *Service) handleIncomingSessionRejectEnvelope(reject SessionReject, publishEvent bool) error {
	if reject.ToPeerID != "" && reject.ToPeerID != s.localPeer {
		return nil
	}
	if reject.FromPeerID == "" || reject.FromPeerID == s.localPeer {
		return nil
	}
	reqRow, err := s.validateFriendRequestEnvelope(reject.RequestID, reject.FromPeerID)
	if err != nil {
		if errors.Is(err, errChatEnvelopeIgnored) {
			return nil
		}
		return err
	}
	if reqRow.State != RequestStatePending {
		return nil
	}
	if err := s.store.UpdateRequestState(reject.RequestID, RequestStateRejected, ""); err != nil {
		return err
	}
	_ = s.store.DeleteFriendRequestJob(reject.RequestID)
	convID := deriveStableConversationID(s.localPeer, reject.FromPeerID)
	if err := s.store.UpdateConversationState(convID, ConversationStateNoFriend); err != nil {
		log.Printf("[chat] update conversation state failed request=%s conversation=%s err=%v", reject.RequestID, convID, err)
	}
	if publishEvent {
		s.publishChatEvent(newFriendRequestEvent(
			RequestStateRejected,
			reject.RequestID,
			reject.FromPeerID,
			reject.ToPeerID,
			convID,
		))
	}
	return nil
}

func (s *Service) handleIncomingSessionAcceptAckEnvelope(ack SessionAcceptAck) error {
	if ack.ToPeerID != s.localPeer {
		return nil
	}
	if _, err := s.validateSessionAcceptAck(ack); err != nil {
		if errors.Is(err, errChatEnvelopeIgnored) {
			return nil
		}
		return err
	}
	if err := s.store.DeleteFriendRequestJob(ack.RequestID); err != nil {
		log.Printf("[chat] delete friend request job on accept ack failed request=%s err=%v", ack.RequestID, err)
	}
	return nil
}

func (s *Service) serveMessageStream(str network.Stream) {
	defer str.Close()
	var env map[string]any
	if err := tunnel.ReadJSONFrame(str, &env); err != nil {
		return
	}
	if err := s.processDirectEnvelope(env); err != nil {
		log.Printf("[chat] process direct envelope failed: %v", err)
	}
}

func (s *Service) serveAckStream(str network.Stream) {
	defer str.Close()
	var ack DeliveryAck
	if err := tunnel.ReadJSONFrame(str, &ack); err != nil {
		return
	}
	_ = s.handleIncomingDeliveryAck(ack)
}

func (s *Service) serveChatSyncStream(str network.Stream) {
	defer str.Close()
	var req ChatSyncRequest
	if err := tunnel.ReadJSONFrame(str, &req); err != nil {
		return
	}
	resp, err := s.handleChatSyncRequest(req, str.Conn().RemotePeer().String())
	if err != nil {
		log.Printf("[chat] handle sync request failed: %v", err)
		return
	}
	if err := tunnel.WriteJSONFrame(str, resp); err != nil {
		log.Printf("[chat] write sync response failed: %v", err)
	}
}

func (s *Service) serveGroupControlStream(str network.Stream) {
	defer str.Close()
	var env map[string]any
	if err := tunnel.ReadJSONFrame(str, &env); err != nil {
		return
	}
	if err := s.processGroupEnvelope(env); err != nil {
		log.Printf("[group] process control envelope failed: %v", err)
	}
}

func (s *Service) serveGroupSyncStream(str network.Stream) {
	defer str.Close()
	var req GroupSyncRequest
	if err := tunnel.ReadJSONFrame(str, &req); err != nil {
		return
	}
	resp, err := s.handleGroupSyncRequest(req, str.Conn().RemotePeer().String())
	if err != nil {
		log.Printf("[group] handle sync request failed: %v", err)
		return
	}
	if err := tunnel.WriteJSONFrame(str, resp); err != nil {
		log.Printf("[group] write sync response failed: %v", err)
	}
}

func (s *Service) serveRelayE2EStream(str network.Stream) {
	defer str.Close()
	var header tunnel.RouteHeader
	if err := tunnel.ReadJSONFrame(str, &header); err != nil {
		return
	}
	s.serveRelayE2EStreamWithHeader(str, header)
}

func (s *Service) serveRelayE2EStreamWithHeader(str network.Stream, header tunnel.RouteHeader) {
	if len(header.Path) == 0 || header.HopIndex < 0 || header.HopIndex >= len(header.Path) {
		return
	}
	selfID := s.host.ID().String()
	if header.Path[header.HopIndex] != selfID || header.Path[len(header.Path)-1] != selfID {
		return
	}
	if header.TargetExit != "" && header.TargetExit != selfID {
		return
	}
	sess, err := tunnel.ServerHandshake(str, header.TunnelID)
	if err != nil {
		return
	}
	for {
		var frame tunnel.EncryptedFrame
		if err := tunnel.ReadJSONFrame(str, &frame); err != nil {
			return
		}
		switch frame.Type {
		case tunnel.FrameTypeData:
			plain, err := sess.Open(frame)
			if err != nil {
				return
			}
			if err := s.processEnvelopeBytes(plain); err != nil {
				log.Printf("[chat] relay envelope processing failed: %v", err)
			}
		case tunnel.FrameTypeClose:
			return
		}
	}
}

func (s *Service) sendJSON(peerID string, protocolID coreprotocol.ID, v any) error {
	if err := s.ensurePeerConnected(peerID); err != nil {
		return err
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	str, err := s.host.NewStream(ctx, pid, protocolID)
	if err != nil {
		return err
	}
	defer str.Close()
	return tunnel.WriteJSONFrame(str, v)
}

func (s *Service) sendJSONConnectedOnly(peerID string, protocolID coreprotocol.ID, v any) error {
	if s == nil || s.host == nil || peerID == "" {
		return errors.New("peer not connected")
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return err
	}
	if !s.peerHasActiveConnection(pid) {
		return errors.New("peer not connected")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	str, err := s.host.NewStream(ctx, pid, protocolID)
	if err != nil {
		return err
	}
	defer str.Close()
	return tunnel.WriteJSONFrame(str, v)
}

func (s *Service) sendEnvelope(peerID string, v any) error {
	protoID := protocolForEnvelope(v)
	if err := s.sendJSON(peerID, protoID, v); err == nil {
		return nil
	}
	return s.sendViaRelay(peerID, v)
}

func (s *Service) sendEnvelopeConnectedOnly(peerID string, v any) error {
	protoID := protocolForEnvelope(v)
	return s.sendJSONConnectedOnly(peerID, protoID, v)
}

func isProfileSyncEnvelope(v any) bool {
	switch v.(type) {
	case ProfileSync, *ProfileSync:
		return true
	default:
		return false
	}
}

func isAvatarEnvelope(v any) bool {
	switch v.(type) {
	case AvatarRequest, *AvatarRequest, AvatarResponse, *AvatarResponse:
		return true
	default:
		return false
	}
}

func (s *Service) sendViaRelay(peerID string, v any) error {
	const relayAttemptLimit = 5
	relays, err := s.pickRelayCandidates(peerID, relayAttemptLimit)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}

	var lastErr error
	for _, relayPID := range relays {
		path := []string{relayPID.String(), peerID}
		tunnelID := uuid.NewString()

		ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
		str, openErr := s.host.NewStream(ctx, relayPID, p2p.ProtocolChatRelayE2E)
		cancel()
		if openErr != nil {
			lastErr = openErr
			continue
		}

		func() {
			defer str.Close()
			header := tunnel.RouteHeader{
				Version:    1,
				TunnelID:   tunnelID,
				Path:       path,
				HopIndex:   0,
				TargetExit: peerID,
			}
			if err := tunnel.WriteJSONFrame(str, header); err != nil {
				lastErr = err
				return
			}
			sess, err := tunnel.ClientHandshake(str, tunnelID)
			if err != nil {
				lastErr = err
				return
			}
			frame, err := sess.Seal(tunnel.FrameTypeData, payload)
			if err != nil {
				lastErr = err
				return
			}
			if err := tunnel.WriteJSONFrame(str, frame); err != nil {
				lastErr = err
				return
			}
			if closeFrame, err := sess.Seal(tunnel.FrameTypeClose, nil); err == nil {
				_ = tunnel.WriteJSONFrame(str, closeFrame)
			}
			lastErr = nil
		}()

		if lastErr == nil {
			return nil
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("no relay path available")
}

func (s *Service) pickRelayCandidates(targetPeerID string, limit int) ([]peer.ID, error) {
	if s.discovery == nil {
		return nil, errors.New("discovery store not available")
	}
	all := s.discovery.ListRelays()
	if len(all) == 0 {
		return nil, errors.New("no relay path available")
	}
	if limit <= 0 {
		limit = 1
	}

	// Shuffle relay list to randomize selection.
	rand.Shuffle(len(all), func(i, j int) { all[i], all[j] = all[j], all[i] })

	connected := make([]peer.ID, 0, min(limit, len(all)))
	disconnected := make([]peer.ID, 0, min(limit, len(all)))
	for _, relayDesc := range all {
		if relayDesc == nil || relayDesc.PeerID == "" || relayDesc.PeerID == s.localPeer || relayDesc.PeerID == targetPeerID {
			continue
		}
		pid, err := peer.Decode(relayDesc.PeerID)
		if err != nil {
			continue
		}
		if s.host.Network().Connectedness(pid) == network.Connected {
			connected = append(connected, pid)
		} else {
			disconnected = append(disconnected, pid)
		}
	}
	out := make([]peer.ID, 0, limit)
	out = append(out, connected...)
	if len(out) < limit {
		out = append(out, disconnected...)
	}
	if len(out) == 0 {
		return nil, errors.New("no relay path available")
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func protocolForEnvelope(v any) coreprotocol.ID {
	switch v.(type) {
	case SessionRequest, *SessionRequest, SessionAccept, *SessionAccept, SessionReject, *SessionReject, SessionAcceptAck, *SessionAcceptAck, ProfileSync, *ProfileSync, AvatarRequest, *AvatarRequest, AvatarResponse, *AvatarResponse:
		return p2p.ProtocolChatRequest
	case ChatText, *ChatText:
		return p2p.ProtocolChatMsg
	case DeliveryAck, *DeliveryAck:
		return p2p.ProtocolChatAck
	case RetentionAck, *RetentionAck:
		return p2p.ProtocolChatAck
	case RetentionUpdate, *RetentionUpdate:
		return p2p.ProtocolChatMsg
	case GroupControlEnvelope, *GroupControlEnvelope, GroupJoinRequest, *GroupJoinRequest, GroupLeaveRequest, *GroupLeaveRequest:
		return p2p.ProtocolGroupControl
	case GroupChatText, *GroupChatText, GroupChatFile, *GroupChatFile, GroupDeliveryAck, *GroupDeliveryAck:
		return p2p.ProtocolGroupMsg
	default:
		return p2p.ProtocolChatMsg
	}
}

func (s *Service) ensurePeerConnected(peerID string) error {
	pid, err := peer.Decode(peerID)
	if err != nil {
		return err
	}
	if s.host.Network().Connectedness(pid) == network.Connected {
		return nil
	}
	if len(s.host.Peerstore().Addrs(pid)) == 0 && s.discovery != nil {
		if desc, ok := s.discovery.Get(peerID); ok {
			for _, addr := range desc.ListenAddrs {
				if maddr, err := multiaddr.NewMultiaddr(addr); err == nil {
					s.host.Peerstore().AddAddr(pid, maddr, time.Hour)
				}
			}
		}
	}
	if len(s.host.Peerstore().Addrs(pid)) == 0 && s.routing != nil {
		if pr, ok := s.routing.(corerouting.PeerRouting); ok {
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			defer cancel()
			if info, err := pr.FindPeer(ctx, pid); err == nil {
				s.host.Peerstore().AddAddrs(pid, info.Addrs, time.Hour)
			}
		}
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	return s.host.Connect(ctx, peer.AddrInfo{ID: pid, Addrs: s.host.Peerstore().Addrs(pid)})
}

func remarshal(in any, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (s *Service) processEnvelopeBytes(data []byte) error {
	var env map[string]any
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	switch env["type"] {
	case MessageTypeGroupControl, MessageTypeGroupJoinRequest, MessageTypeGroupLeaveRequest, MessageTypeGroupChatText, MessageTypeGroupChatFile, MessageTypeGroupDeliveryAck:
		return s.processGroupEnvelope(env)
	case MessageTypeSessionRequest:
		var req SessionRequest
		if err := remarshal(env, &req); err != nil {
			return err
		}
		return s.handleIncomingSessionRequestEnvelope(req, "relay", true)
	case MessageTypeChatSyncRequest:
		var req ChatSyncRequest
		if err := remarshal(env, &req); err != nil {
			return err
		}
		resp, err := s.handleChatSyncRequest(req, req.FromPeerID)
		if err != nil {
			return err
		}
		return s.sendEnvelope(req.FromPeerID, resp)
	case MessageTypeChatSyncResponse:
		var resp ChatSyncResponse
		if err := remarshal(env, &resp); err != nil {
			return err
		}
		s.resolvePendingChatSync(resp)
		return nil
	case MessageTypeSessionAccept:
		var accept SessionAccept
		if err := remarshal(env, &accept); err != nil {
			return err
		}
		return s.handleIncomingSessionAcceptEnvelope(accept, true, true)
	case MessageTypeSessionReject:
		var reject SessionReject
		if err := remarshal(env, &reject); err != nil {
			return err
		}
		return s.handleIncomingSessionRejectEnvelope(reject, true)
	case MessageTypeSessionAcceptAck:
		var ack SessionAcceptAck
		if err := remarshal(env, &ack); err != nil {
			return err
		}
		return s.handleIncomingSessionAcceptAckEnvelope(ack)
	case MessageTypeProfileSync:
		var sync ProfileSync
		if err := remarshal(env, &sync); err != nil {
			return err
		}
		return s.handleProfileSync(sync)
	case MessageTypeAvatarRequest:
		var req AvatarRequest
		if err := remarshal(env, &req); err != nil {
			return err
		}
		return s.handleAvatarRequest(req)
	case MessageTypeAvatarResponse:
		var resp AvatarResponse
		if err := remarshal(env, &resp); err != nil {
			return err
		}
		return s.handleAvatarResponse(resp)
	case MessageTypeChatText, MessageTypeGroupInviteNote:
		var msg ChatText
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		return s.handleIncomingChatText(msg, "relay")
	case MessageTypeChatFile:
		var msg ChatFile
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		return s.handleIncomingChatFile(msg, "relay")
	case MessageTypeDeliveryAck:
		var ack DeliveryAck
		if err := remarshal(env, &ack); err != nil {
			return err
		}
		return s.handleIncomingDeliveryAck(ack)
	case MessageTypeMessageRevoke:
		var revoke MessageRevoke
		if err := remarshal(env, &revoke); err != nil {
			return err
		}
		return s.store.DeleteMessage(revoke.ConversationID, revoke.MsgID)
	case MessageTypeRetentionUpdate:
		var update RetentionUpdate
		if err := remarshal(env, &update); err != nil {
			return err
		}
		return s.handleIncomingRetentionUpdate(update, "relay")
	case MessageTypeRetentionAck:
		var ack RetentionAck
		if err := remarshal(env, &ack); err != nil {
			return err
		}
		return s.store.UpdateConversationRetentionSync(ack.ConversationID, "synced", time.UnixMilli(ack.AckedAtUnix))
	default:
		return nil
	}
}

func (s *Service) processDirectEnvelope(env map[string]any) error {
	switch env["type"] {
	case MessageTypeGroupControl, MessageTypeGroupJoinRequest, MessageTypeGroupLeaveRequest, MessageTypeGroupChatText, MessageTypeGroupChatFile, MessageTypeGroupDeliveryAck:
		return s.processGroupEnvelope(env)
	case MessageTypeChatSyncResponse:
		var resp ChatSyncResponse
		if err := remarshal(env, &resp); err != nil {
			return err
		}
		s.resolvePendingChatSync(resp)
		return nil
	case MessageTypeChatText, MessageTypeGroupInviteNote:
		var msg ChatText
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		return s.handleIncomingChatText(msg, TransportModeDirect)
	case MessageTypeChatFile:
		var msg ChatFile
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		return s.handleIncomingChatFile(msg, TransportModeDirect)
	case MessageTypeMessageRevoke:
		var revoke MessageRevoke
		if err := remarshal(env, &revoke); err != nil {
			return err
		}
		return s.store.DeleteMessage(revoke.ConversationID, revoke.MsgID)
	case MessageTypeRetentionUpdate:
		var update RetentionUpdate
		if err := remarshal(env, &update); err != nil {
			return err
		}
		return s.handleIncomingRetentionUpdate(update, TransportModeDirect)
	case MessageTypeRetentionAck:
		var ack RetentionAck
		if err := remarshal(env, &ack); err != nil {
			return err
		}
		return s.store.UpdateConversationRetentionSync(ack.ConversationID, "synced", time.UnixMilli(ack.AckedAtUnix))
	case MessageTypeDeliveryAck:
		var ack DeliveryAck
		if err := remarshal(env, &ack); err != nil {
			return err
		}
		return s.handleIncomingDeliveryAck(ack)
	case MessageTypeProfileSync:
		var sync ProfileSync
		if err := remarshal(env, &sync); err != nil {
			return err
		}
		return s.handleProfileSync(sync)
	case MessageTypeAvatarRequest:
		var req AvatarRequest
		if err := remarshal(env, &req); err != nil {
			return err
		}
		return s.handleAvatarRequest(req)
	case MessageTypeAvatarResponse:
		var resp AvatarResponse
		if err := remarshal(env, &resp); err != nil {
			return err
		}
		return s.handleAvatarResponse(resp)
	default:
		return nil
	}
}

func (s *Service) handleIncomingChatText(msg ChatText, transportMode string) error {
	if contact, err := s.store.GetPeer(msg.FromPeerID); err == nil && contact.Blocked {
		return nil
	}
	conv, sess, _, err := s.loadIncomingDirectState(msg.ConversationID, msg.FromPeerID, transportMode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 对方尚未建立好友关系，或者本地仍无法恢复会话状态：直接拒绝发送方。
			if s.maybeSendSessionRejectForConversation(msg.FromPeerID) {
				return nil
			}
		}
		return err
	}
	duplicate, err := s.checkIncomingDirectCounter(conv, sess, msg.MsgID, msg.Counter)
	if err != nil || duplicate {
		return err
	}
	msgType := msg.Type
	if msgType == "" {
		msgType = MessageTypeChatText
	}
	if msgType != MessageTypeChatText && msgType != MessageTypeGroupInviteNote {
		return fmt.Errorf("unsupported chat text type: %s", msgType)
	}
	nonce := protocol.BuildAEADNonce("fwd", msg.Counter)
	aad := []byte(msg.ConversationID + "\x00" + msgType)
	plain, err := protocol.AEADOpen(sess.RecvKey, nonce, msg.Ciphertext, aad)
	if err != nil {
		return err
	}
	if msgType == MessageTypeGroupInviteNote {
		if err := s.handleIncomingGroupInviteNotice(msg, plain); err != nil {
			return err
		}
	}
	incoming := Message{
		MsgID:          msg.MsgID,
		ConversationID: msg.ConversationID,
		SenderPeerID:   msg.FromPeerID,
		ReceiverPeerID: s.localPeer,
		Direction:      "inbound",
		MsgType:        msgType,
		Plaintext:      string(plain),
		TransportMode:  transportMode,
		State:          MessageStateReceived,
		Counter:        msg.Counter,
		CreatedAt:      time.UnixMilli(msg.SentAtUnix),
	}
	if _, err := s.store.AddMessage(incoming, msg.Ciphertext); err != nil {
		return err
	}
	// Notify websocket clients that this conversation has a new inbound message.
	s.publishChatEvent(newMessageEvent(
		"direct",
		msg.ConversationID,
		msg.MsgID,
		msgType,
	))
	_ = s.store.UpsertPeer(msg.FromPeerID, "", "")
	_ = s.store.UpdateRecvCounter(msg.ConversationID, msg.Counter+1)
	return s.sendDeliveryAck(conv, msg.MsgID, msg.FromPeerID)
}

func (s *Service) handleIncomingGroupInviteNotice(msg ChatText, plain []byte) error {
	var payload GroupInviteNoticePayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return err
	}
	if payload.InviteePeerID != "" && payload.InviteePeerID != s.localPeer {
		return errors.New("group invite notice is not addressed to local peer")
	}
	if payload.ControllerPeerID != "" && payload.ControllerPeerID != msg.FromPeerID {
		return errors.New("group invite controller does not match sender")
	}
	if payload.InviteEnvelope.Type != MessageTypeGroupControl || payload.InviteEnvelope.EventType != GroupEventInvite {
		return errors.New("group invite notice is missing invite envelope")
	}
	if payload.InviteEnvelope.GroupID != "" && payload.GroupID != "" && payload.InviteEnvelope.GroupID != payload.GroupID {
		return errors.New("group invite notice group mismatch")
	}
	if payload.InviteEnvelope.SignerPeerID != msg.FromPeerID {
		return errors.New("group invite envelope signer does not match sender")
	}
	if err := s.verifyGroupControlEnvelope(payload.InviteEnvelope); err != nil {
		return err
	}
	return s.applyRemoteGroupInviteEnvelope(payload.InviteEnvelope, false)
}

func (s *Service) handleIncomingChatFile(msg ChatFile, transportMode string) error {
	if contact, err := s.store.GetPeer(msg.FromPeerID); err == nil && contact.Blocked {
		return nil
	}
	conv, sess, _, err := s.loadIncomingDirectState(msg.ConversationID, msg.FromPeerID, transportMode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 对方尚未建立好友关系，或者本地仍无法恢复会话状态：直接拒绝发送方。
			if s.maybeSendSessionRejectForConversation(msg.FromPeerID) {
				return nil
			}
		}
		return err
	}
	duplicate, err := s.checkIncomingDirectCounter(conv, sess, msg.MsgID, msg.Counter)
	if err != nil || duplicate {
		return err
	}
	nonce := protocol.BuildAEADNonce("fwd", msg.Counter)
	aad := []byte(msg.ConversationID + "\x00chat_file")
	plain, err := protocol.AEADOpen(sess.RecvKey, nonce, msg.Ciphertext, aad)
	if err != nil {
		return err
	}
	incoming := Message{
		MsgID:          msg.MsgID,
		ConversationID: msg.ConversationID,
		SenderPeerID:   msg.FromPeerID,
		ReceiverPeerID: s.localPeer,
		Direction:      "inbound",
		MsgType:        MessageTypeChatFile,
		FileName:       NormalizeChatFileName(msg.FileName),
		MIMEType:       msg.MIMEType,
		FileSize:       int64(len(plain)),
		TransportMode:  transportMode,
		State:          MessageStateReceived,
		Counter:        msg.Counter,
		CreatedAt:      time.UnixMilli(msg.SentAtUnix),
	}
	if _, err := s.store.AddMessage(incoming, plain); err != nil {
		return err
	}
	// Notify websocket clients that this conversation has a new inbound message.
	s.publishChatEvent(newMessageEvent(
		"direct",
		msg.ConversationID,
		msg.MsgID,
		MessageTypeChatFile,
	))
	_ = s.store.UpsertPeer(msg.FromPeerID, "", "")
	_ = s.store.UpdateRecvCounter(msg.ConversationID, msg.Counter+1)
	return s.sendDeliveryAck(conv, msg.MsgID, msg.FromPeerID)
}

func (s *Service) checkIncomingDirectCounter(conv Conversation, sess sessionState, msgID string, counter uint64) (bool, error) {
	expected := sess.RecvCounter
	switch {
	case counter < expected:
		return true, s.sendDeliveryAck(conv, msgID, conv.PeerID)
	case counter > expected:
		s.triggerConversationSync(conv.ConversationID, conv.PeerID, expected)
		return false, fmt.Errorf("chat message counter gap: expected=%d got=%d", expected, counter)
	default:
		return false, nil
	}
}

func (s *Service) sendDeliveryAck(conv Conversation, msgID, toPeerID string) error {
	ack := DeliveryAck{
		Type:           MessageTypeDeliveryAck,
		ConversationID: conv.ConversationID,
		MsgID:          msgID,
		FromPeerID:     s.localPeer,
		ToPeerID:       toPeerID,
		AckedAtUnix:    time.Now().UTC().UnixMilli(),
	}
	return s.sendEnvelope(conv.PeerID, ack)
}

func (s *Service) handleIncomingDeliveryAck(ack DeliveryAck) error {
	if err := s.store.MarkMessageDelivered(ack.MsgID, time.UnixMilli(ack.AckedAtUnix)); err != nil {
		return err
	}
	return s.store.DeleteOutboxJob(ack.MsgID)
}

func (s *Service) sendStoredDirectMessage(msg Message, storedBlob []byte, cachedCiphertext []byte) error {
	wire, err := s.buildDirectEnvelope(msg, storedBlob, cachedCiphertext)
	if err != nil {
		return err
	}
	return s.sendEnvelope(msg.ReceiverPeerID, wire)
}

func (s *Service) buildDirectEnvelope(msg Message, storedBlob []byte, cachedCiphertext []byte) (any, error) {
	switch msg.MsgType {
	case MessageTypeChatText, MessageTypeGroupInviteNote:
		ciphertext := cachedCiphertext
		if len(ciphertext) == 0 {
			ciphertext = storedBlob
		}
		return ChatText{
			Type:           msg.MsgType,
			ConversationID: msg.ConversationID,
			MsgID:          msg.MsgID,
			FromPeerID:     msg.SenderPeerID,
			ToPeerID:       msg.ReceiverPeerID,
			Ciphertext:     ciphertext,
			Counter:        msg.Counter,
			SentAtUnix:     msg.CreatedAt.UnixMilli(),
		}, nil
	case MessageTypeChatFile:
		sess, err := s.store.GetSessionState(msg.ConversationID)
		if err != nil {
			return nil, err
		}
		nonce := protocol.BuildAEADNonce("fwd", msg.Counter)
		aad := []byte(msg.ConversationID + "\x00chat_file")
		ciphertext, err := protocol.AEADSeal(sess.SendKey, nonce, storedBlob, aad)
		if err != nil {
			return nil, err
		}
		return ChatFile{
			Type:           MessageTypeChatFile,
			ConversationID: msg.ConversationID,
			MsgID:          msg.MsgID,
			FromPeerID:     msg.SenderPeerID,
			ToPeerID:       msg.ReceiverPeerID,
			FileName:       msg.FileName,
			MIMEType:       msg.MIMEType,
			FileSize:       msg.FileSize,
			Ciphertext:     ciphertext,
			Counter:        msg.Counter,
			SentAtUnix:     msg.CreatedAt.UnixMilli(),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported direct msg_type %q", msg.MsgType)
	}
}

func (s *Service) retryOutboxJob(item outboxRetryItem) error {
	msg, err := s.store.GetMessage(item.MsgID)
	if err != nil {
		return err
	}
	if err := s.sendStoredDirectMessage(msg, item.CiphertextBlob, nil); err != nil {
		_ = s.store.UpdateMessageState(item.MsgID, MessageStateQueuedForRetry)
		return s.scheduleOutboxRetry(item.MsgID, item.PeerID, item.RetryCount+1)
	}
	if err := s.store.UpdateMessageState(item.MsgID, MessageStateSentToTransport); err != nil {
		return err
	}
	return s.markOutboxSentToTransport(item.MsgID, item.PeerID, item.RetryCount, time.Now().UTC())
}

func (s *Service) scheduleOutboxRetry(msgID, peerID string, retryCount int) error {
	now := time.Now().UTC()
	return s.store.UpsertOutboxJob(msgID, peerID, MessageStateQueuedForRetry, retryCount, now, now)
}

func (s *Service) markOutboxSentToTransport(msgID, peerID string, retryCount int, attemptedAt time.Time) error {
	return s.store.UpsertOutboxJob(msgID, peerID, MessageStateSentToTransport, retryCount, attemptedAt.Add(directAckWait), attemptedAt)
}

func (s *Service) triggerConversationSync(conversationID, peerID string, nextCounter uint64) {
	safe.Go("chat.syncOnGap", func() {
		req := ChatSyncRequest{
			Type:           MessageTypeChatSyncRequest,
			ConversationID: conversationID,
			FromPeerID:     s.localPeer,
			ToPeerID:       peerID,
			NextCounter:    nextCounter,
			SentAtUnix:     time.Now().UTC().UnixMilli(),
		}
		resp, err := s.requestChatSync(peerID, req)
		if err != nil {
			log.Printf("[chat] sync on gap failed conversation=%s peer=%s: %v", conversationID, peerID, err)
			return
		}
		if err := s.applyChatSyncResponse(conversationID, resp); err != nil {
			log.Printf("[chat] apply synced response failed conversation=%s: %v", conversationID, err)
			return
		}
	})
}

func (s *Service) applyChatSyncResponse(conversationID string, resp ChatSyncResponse) error {
	sess, err := s.store.GetSessionState(conversationID)
	if err != nil {
		return err
	}
	expected := sess.RecvCounter
	if len(resp.Messages) == 0 && len(resp.Files) == 0 && resp.RemoteSendCounter > expected {
		if err := s.store.UpdateRecvCounter(conversationID, resp.RemoteSendCounter); err != nil {
			return err
		}
		return nil
	}
	textIdx := 0
	fileIdx := 0
	for textIdx < len(resp.Messages) || fileIdx < len(resp.Files) {
		useText := fileIdx >= len(resp.Files)
		if textIdx < len(resp.Messages) && fileIdx < len(resp.Files) {
			useText = resp.Messages[textIdx].Counter <= resp.Files[fileIdx].Counter
		}
		if useText {
			msg := resp.Messages[textIdx]
			if msg.Counter > expected {
				if err := s.store.UpdateRecvCounter(conversationID, msg.Counter); err != nil {
					return err
				}
				expected = msg.Counter
			}
			if err := s.handleIncomingChatText(msg, TransportModeDirect); err != nil {
				return fmt.Errorf("msg=%s counter=%d type=%s: %w", msg.MsgID, msg.Counter, msg.Type, err)
			}
			expected = msg.Counter + 1
			textIdx++
			continue
		}
		msg := resp.Files[fileIdx]
		if msg.Counter > expected {
			if err := s.store.UpdateRecvCounter(conversationID, msg.Counter); err != nil {
				return err
			}
			expected = msg.Counter
		}
		if err := s.handleIncomingChatFile(msg, TransportModeDirect); err != nil {
			return fmt.Errorf("msg=%s counter=%d type=%s: %w", msg.MsgID, msg.Counter, msg.Type, err)
		}
		expected = msg.Counter + 1
		fileIdx++
	}
	return nil
}

func (s *Service) handleChatSyncRequest(req ChatSyncRequest, remotePeerID string) (ChatSyncResponse, error) {
	if req.Type != MessageTypeChatSyncRequest {
		return ChatSyncResponse{}, errors.New("invalid chat sync request type")
	}
	if req.FromPeerID != remotePeerID {
		return ChatSyncResponse{}, errors.New("chat sync requester mismatch")
	}
	conv, err := s.store.GetConversation(req.ConversationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Best-effort: return empty response instead of failing relay/direct processing.
			return ChatSyncResponse{
				Type:              MessageTypeChatSyncResponse,
				ConversationID:    req.ConversationID,
				RemoteSendCounter: 0,
			}, nil
		}
		return ChatSyncResponse{}, err
	}
	if conv.PeerID != remotePeerID || req.ToPeerID != s.localPeer {
		return ChatSyncResponse{}, errors.New("chat sync conversation mismatch")
	}
	items, err := s.store.ListOutgoingMessagesForSync(req.ConversationID, s.localPeer, req.NextCounter, directSyncBatchSize)
	if err != nil {
		return ChatSyncResponse{}, err
	}
	resp := ChatSyncResponse{
		Type:           MessageTypeChatSyncResponse,
		ConversationID: req.ConversationID,
	}
	if sess, err := s.store.GetSessionState(req.ConversationID); err == nil {
		resp.RemoteSendCounter = sess.SendCounter
	}
	for _, item := range items {
		msg := Message{
			MsgID:          item.MsgID,
			ConversationID: item.ConversationID,
			SenderPeerID:   item.SenderPeerID,
			ReceiverPeerID: item.ReceiverPeerID,
			MsgType:        item.MsgType,
			FileName:       item.FileName,
			MIMEType:       item.MIMEType,
			FileSize:       item.FileSize,
			Counter:        item.Counter,
			CreatedAt:      time.UnixMilli(item.SentAtUnix).UTC(),
		}
		wire, err := s.buildDirectEnvelope(msg, item.CiphertextBlob, nil)
		if err != nil {
			return ChatSyncResponse{}, err
		}
		switch typed := wire.(type) {
		case ChatText:
			resp.Messages = append(resp.Messages, typed)
		case ChatFile:
			resp.Files = append(resp.Files, typed)
		default:
			return ChatSyncResponse{}, fmt.Errorf("unsupported chat sync wire %T", wire)
		}
	}
	return resp, nil
}

func (s *Service) requestChatSync(peerID string, req ChatSyncRequest) (ChatSyncResponse, error) {
	if err := s.ensurePeerConnected(peerID); err != nil {
		// Fallback to relay when direct connection is unavailable.
		return s.requestChatSyncViaRelay(peerID, req)
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return ChatSyncResponse{}, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	str, err := s.host.NewStream(ctx, pid, p2p.ProtocolChatSync)
	if err != nil {
		return ChatSyncResponse{}, err
	}
	defer str.Close()
	if err := tunnel.WriteJSONFrame(str, req); err != nil {
		return ChatSyncResponse{}, err
	}
	var resp ChatSyncResponse
	if err := tunnel.ReadJSONFrame(str, &resp); err != nil {
		// Direct sync stream failed (e.g. EOF): fallback to relay.
		return s.requestChatSyncViaRelay(peerID, req)
	}
	return resp, nil
}

func (s *Service) requestChatSyncViaRelay(peerID string, req ChatSyncRequest) (ChatSyncResponse, error) {
	const relaySyncTimeout = 20 * time.Second
	waitCh := make(chan ChatSyncResponse, 1)
	s.chatSyncPending.Store(req.ConversationID, waitCh)
	defer s.chatSyncPending.Delete(req.ConversationID)

	if err := s.sendViaRelay(peerID, req); err != nil {
		return ChatSyncResponse{}, err
	}

	select {
	case <-s.ctx.Done():
		return ChatSyncResponse{}, s.ctx.Err()
	case resp := <-waitCh:
		return resp, nil
	case <-time.After(relaySyncTimeout):
		return ChatSyncResponse{}, errors.New("chat sync relay timeout")
	}
}

func (s *Service) resolvePendingChatSync(resp ChatSyncResponse) {
	if s == nil || resp.ConversationID == "" {
		return
	}
	chAny, ok := s.chatSyncPending.Load(resp.ConversationID)
	if !ok {
		return
	}
	ch, ok := chAny.(chan ChatSyncResponse)
	if !ok {
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

func (s *Service) recoverMissingOutboxJobs() error {
	items, err := s.store.ListMessagesMissingOutbox(directRetryBatchSize)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, item := range items {
		nextRetryAt := now
		status := item.State
		if status == "" || status == MessageStateLocalOnly {
			status = MessageStateQueuedForRetry
		}
		if status == MessageStateSentToTransport {
			nextRetryAt = now
		}
		if err := s.store.UpsertOutboxJob(item.MsgID, item.ReceiverPeerID, status, 0, nextRetryAt, time.Time{}); err != nil {
			return err
		}
		if item.State == MessageStateLocalOnly {
			if err := s.store.UpdateMessageState(item.MsgID, MessageStateQueuedForRetry); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) runOutboxRetryLoop() {
	ticker := time.NewTicker(outboxRetryTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			items, err := s.store.ListOutboxJobsForRetry(now.UTC(), outboxRetryBatchSize)
			if err != nil {
				log.Printf("[chat] outbox retry list failed err=%v", err)
				continue
			}
			for _, item := range items {
				if err := s.retryOutboxJob(item); err != nil {
					log.Printf("[chat] outbox retry failed job=%s msg=%s peer=%s err=%v", item.JobID, item.MsgID, item.PeerID, err)
				}
			}
		}
	}
}

func (s *Service) runFriendRequestRetryLoop() {
	ticker := time.NewTicker(friendRequestRetryTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			if err := s.processFriendRequestRetries(now.UTC()); err != nil {
				log.Printf("[chat] friend request retry loop failed: %v", err)
			}
		}
	}
}

func (s *Service) processFriendRequestRetries(now time.Time) error {
	items, err := s.store.ListFriendRequestJobsForRetry(now, friendRequestRetryBatchSize)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.retryFriendRequestJob(item, now); err != nil {
			log.Printf("[chat] friend request retry failed request=%s peer=%s retry_count=%d err=%v", item.RequestID, item.PeerID, item.RetryCount, err)
		}
	}
	return nil
}

func (s *Service) retryFriendRequestJob(item friendRequestRetryItem, now time.Time) error {
	req, err := s.store.GetRequest(item.RequestID)
	if err != nil {
		_ = s.store.DeleteFriendRequestJob(item.RequestID)
		return err
	}
	// Abandon after 1 week, regardless of state.
	if !req.CreatedAt.IsZero() && now.After(req.CreatedAt.Add(friendRequestRetryDeadline)) {
		if req.State == RequestStatePending {
			if err := s.store.UpdateRequestState(req.RequestID, RequestStateRejected, ""); err != nil {
				return err
			}
		}
		return s.store.DeleteFriendRequestJob(item.RequestID)
	}

	// If request is no longer pending or accepted, stop retrying.
	if req.State != RequestStatePending && req.State != RequestStateAccepted {
		return s.store.DeleteFriendRequestJob(item.RequestID)
	}

	if contact, err := s.store.GetPeer(item.PeerID); err == nil && contact.Blocked {
		_ = s.store.UpdateRequestState(req.RequestID, RequestStateBlocked, "")
		return s.store.DeleteFriendRequestJob(item.RequestID)
	}

	profile, _, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		return err
	}

	// Pending -> re-send SessionRequest (outgoing friend request).
	if req.State == RequestStatePending {
		wire := SessionRequest{
			Type:             MessageTypeSessionRequest,
			RequestID:        req.RequestID,
			FromPeerID:       s.localPeer,
			ToPeerID:         req.ToPeerID,
			Nickname:         req.Nickname,
			Bio:              req.Bio,
			AvatarName:       profile.Avatar,
			RetentionMinutes: req.RetentionMinutes,
			IntroText:        req.IntroText,
			ChatKexPub:       req.RemoteChatKexPub,
			SentAtUnix:       now.UnixMilli(),
		}

		if err := s.sendEnvelope(item.PeerID, wire); err == nil {
			return s.store.DeleteFriendRequestJob(item.RequestID)
		} else {
			if scheduleErr := s.scheduleFriendRequestRetry(req.RequestID, item.PeerID, item.RetryCount+1); scheduleErr != nil {
				return fmt.Errorf("schedule retry failed after send error: send=%v schedule=%v", err, scheduleErr)
			}
			return err
		}
	}

	// Accepted -> re-send SessionAccept to finalize the friendship.
	if req.State == RequestStateAccepted {
		if req.ConversationID == "" {
			// Nothing meaningful to retry; drop the job.
			return s.store.DeleteFriendRequestJob(item.RequestID)
		}
		conv, err := s.store.GetConversation(req.ConversationID)
		if err != nil {
			_ = s.store.DeleteFriendRequestJob(item.RequestID)
			return err
		}
		retentionMinutes := req.RetentionMinutes
		if conv.RetentionMinutes > retentionMinutes {
			retentionMinutes = conv.RetentionMinutes
		}
		wire := SessionAccept{
			Type:             MessageTypeSessionAccept,
			RequestID:        req.RequestID,
			ConversationID:   conv.ConversationID,
			FromPeerID:       s.localPeer,
			ToPeerID:         item.PeerID,
			Bio:              profile.Bio,
			AvatarName:       profile.Avatar,
			RetentionMinutes: retentionMinutes,
			ChatKexPub:       profile.ChatKexPub,
			SentAtUnix:       now.UnixMilli(),
		}
		if err := s.sendEnvelope(item.PeerID, wire); err == nil {
			// Ack may still be in-flight; keep retry job alive until we receive SessionAcceptAck.
			if scheduleErr := s.scheduleFriendRequestRetry(req.RequestID, item.PeerID, item.RetryCount+1); scheduleErr != nil {
				return fmt.Errorf("schedule retry (accept wait) failed: schedule=%v", scheduleErr)
			}
			return nil
		} else {
			// Transport send failed: also schedule retry (still waiting for ack).
			if scheduleErr := s.scheduleFriendRequestRetry(req.RequestID, item.PeerID, item.RetryCount+1); scheduleErr != nil {
				return fmt.Errorf("schedule retry (accept) failed after send error: send=%v schedule=%v", err, scheduleErr)
			}
			return err
		}
	}

	// Should not reach here, but be safe.
	return s.store.DeleteFriendRequestJob(item.RequestID)
}

func (s *Service) scheduleFriendRequestRetry(requestID, peerID string, retryCount int) error {
	now := time.Now().UTC()
	if retryCount < 0 {
		retryCount = 0
	}

	exp := retryCount
	if exp > 10 {
		exp = 10 // prevent overflow and keep backoff bounded.
	}
	mult := time.Duration(1 << uint(exp))
	delay := friendRequestRetryBaseDelay * mult
	if delay > friendRequestRetryMaxDelay {
		delay = friendRequestRetryMaxDelay
	}
	jitterMax := delay / 10
	var jitter time.Duration
	if jitterMax > 0 {
		jitter = time.Duration(rand.Int63n(int64(jitterMax)))
	}
	nextRetryAt := now.Add(delay + jitter)
	return s.store.UpsertFriendRequestJob(requestID, peerID, MessageStateQueuedForRetry, retryCount, nextRetryAt, now)
}

func (s *Service) runRetentionLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			s.runRetentionSweep(now.UTC())
		}
	}
}

func (s *Service) runRetentionSweep(now time.Time) {
	if err := s.store.CleanupExpiredMessages(now); err != nil {
		log.Printf("[chat] retention cleanup failed: %v", err)
	}
	if err := s.store.CleanupExpiredGroupMessages(now); err != nil {
		log.Printf("[group] retention cleanup failed: %v", err)
	}
	if err := s.store.CleanupArchivedGroups(now); err != nil {
		log.Printf("[group] archived cleanup failed: %v", err)
	}
}
