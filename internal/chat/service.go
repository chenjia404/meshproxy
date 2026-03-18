package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

	autoConnectSeen sync.Map
}

const (
	directRetryBatchSize = 64
	directAckWait        = 20 * time.Second
	directSyncBatchSize  = 256
	directAutoConnectTTL = 30 * time.Second
)

func NewService(ctx context.Context, dbPath string, h host.Host, routing corerouting.Routing, ds *discovery.Store) (*Service, error) {
	s := &Service{
		ctx:       ctx,
		host:      h,
		routing:   routing,
		discovery: ds,
		localPeer: h.ID().String(),
	}
	store, err := NewStore(dbPath, s.localPeer)
	if err != nil {
		return nil, err
	}
	s.store = store
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
	safe.Go("chat.groupRetryLoop", func() { s.runGroupRetryLoop() })
	return s, nil
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
	return s.store.GetConversation(updated.ConversationID)
}

func (s *Service) ListContacts() ([]Contact, error) {
	return s.store.ListContacts()
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
	if err := s.sendEnvelope(msg.ReceiverPeerID, revoke); err != nil {
		return err
	}
	return s.store.DeleteMessage(conversationID, msgID)
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
	if contact, err := s.store.GetPeer(toPeerID); err == nil && contact.Blocked {
		return Request{}, errors.New("peer is blocked")
	}
	profile, _, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		return Request{}, err
	}
	retentionMinutes := 0
	if conv, err := s.store.GetConversationByPeer(toPeerID); err == nil {
		retentionMinutes = conv.RetentionMinutes
	}
	if err := s.ensurePeerConnected(toPeerID); err != nil {
		return Request{}, err
	}
	req := Request{
		RequestID:         uuid.NewString(),
		FromPeerID:        s.localPeer,
		ToPeerID:          toPeerID,
		State:             RequestStatePending,
		IntroText:         strings.TrimSpace(introText),
		Nickname:          profile.Nickname,
		Bio:               profile.Bio,
		RetentionMinutes:  retentionMinutes,
		LastTransportMode: TransportModeDirect,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	wire := SessionRequest{
		Type:             MessageTypeSessionRequest,
		RequestID:        req.RequestID,
		FromPeerID:       req.FromPeerID,
		ToPeerID:         req.ToPeerID,
		Nickname:         profile.Nickname,
		Bio:              profile.Bio,
		RetentionMinutes: retentionMinutes,
		IntroText:        req.IntroText,
		ChatKexPub:       profile.ChatKexPub,
		SentAtUnix:       time.Now().UnixMilli(),
	}
	req.RemoteChatKexPub = profile.ChatKexPub
	if err := s.store.SaveOutgoingRequest(req); err != nil {
		return Request{}, err
	}
	_ = s.store.UpsertPeer(toPeerID, "", "")
	if err := s.sendEnvelope(toPeerID, wire); err != nil {
		return Request{}, err
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
	retentionMinutes := req.RetentionMinutes
	if existingConv, err := s.store.GetConversationByPeer(req.FromPeerID); err == nil {
		if existingConv.RetentionMinutes > retentionMinutes {
			retentionMinutes = existingConv.RetentionMinutes
		}
		if err := s.store.UpdateRequestState(requestID, RequestStateAccepted, existingConv.ConversationID); err != nil {
			return Conversation{}, err
		}
		_ = s.store.UpsertPeer(req.FromPeerID, req.Nickname, req.Bio)
		if retentionMinutes != existingConv.RetentionMinutes {
			if updatedConv, err := s.store.UpdateConversationRetention(existingConv.ConversationID, retentionMinutes); err == nil {
				existingConv = updatedConv
			}
		}
		if err := s.ensurePeerConnected(req.FromPeerID); err == nil {
			wire := SessionAccept{
				Type:             MessageTypeSessionAccept,
				RequestID:        req.RequestID,
				ConversationID:   existingConv.ConversationID,
				FromPeerID:       s.localPeer,
				ToPeerID:         req.FromPeerID,
				Bio:              profile.Bio,
				RetentionMinutes: retentionMinutes,
				ChatKexPub:       profile.ChatKexPub,
				SentAtUnix:       time.Now().UnixMilli(),
			}
			if err := s.sendEnvelope(req.FromPeerID, wire); err != nil {
				log.Printf("[chat] send accept for existing conversation failed request=%s err=%v", requestID, err)
			}
		}
		return existingConv, nil
	} else if err != sql.ErrNoRows {
		return Conversation{}, err
	}
	convID := deriveConversationID(s.localPeer, req.FromPeerID, req.RequestID)
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
		return conv, nil
	}
	wire := SessionAccept{
		Type:             MessageTypeSessionAccept,
		RequestID:        req.RequestID,
		ConversationID:   convID,
		FromPeerID:       s.localPeer,
		ToPeerID:         req.FromPeerID,
		Bio:              profile.Bio,
		RetentionMinutes: retentionMinutes,
		ChatKexPub:       profile.ChatKexPub,
		SentAtUnix:       time.Now().UnixMilli(),
	}
	if err := s.sendEnvelope(req.FromPeerID, wire); err != nil {
		log.Printf("[chat] send accept failed request=%s err=%v", requestID, err)
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
	return map[string]any{
		"local_peer_id":   s.localPeer,
		"connected_peers": len(s.host.Network().Peers()),
	}
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
		conv, err := s.store.GetConversationByPeer(peerID)
		if err == nil {
			if err := s.SyncConversation(conv.ConversationID); err != nil {
				log.Printf("[chat] sync on connect conversation=%s peer=%s failed: %v", conv.ConversationID, peerID, err)
			}
		}
	})
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
		if contact, err := s.store.GetPeer(req.FromPeerID); err == nil && contact.Blocked {
			return
		}
		stored := Request{
			RequestID:         req.RequestID,
			FromPeerID:        req.FromPeerID,
			ToPeerID:          req.ToPeerID,
			State:             RequestStatePending,
			IntroText:         req.IntroText,
			Nickname:          req.Nickname,
			Bio:               req.Bio,
			RetentionMinutes:  req.RetentionMinutes,
			RemoteChatKexPub:  req.ChatKexPub,
			LastTransportMode: TransportModeDirect,
			CreatedAt:         time.UnixMilli(req.SentAtUnix),
			UpdatedAt:         time.Now(),
		}
		_ = s.store.UpsertPeer(req.FromPeerID, req.Nickname, req.Bio)
		if err := s.store.UpsertIncomingRequest(stored); err != nil {
			log.Printf("[chat] save request failed: %v", err)
		}
	case MessageTypeSessionAccept:
		var accept SessionAccept
		if err := remarshal(env, &accept); err != nil {
			return
		}
		if existingConv, err := s.store.GetConversationByPeer(accept.FromPeerID); err == nil {
			_ = s.store.UpsertPeer(accept.FromPeerID, "", accept.Bio)
			targetRetention := accept.RetentionMinutes
			if existingConv.RetentionMinutes > targetRetention {
				targetRetention = existingConv.RetentionMinutes
			}
			if targetRetention != existingConv.RetentionMinutes {
				if updatedConv, err := s.store.UpdateConversationRetention(existingConv.ConversationID, targetRetention); err == nil {
					existingConv = updatedConv
				}
			}
			_ = s.store.UpdateRequestState(accept.RequestID, RequestStateAccepted, existingConv.ConversationID)
			return
		} else if err != sql.ErrNoRows {
			log.Printf("[chat] lookup existing accepted conversation failed: %v", err)
			return
		}
		_, priv, err := s.store.GetProfile(s.localPeer)
		if err != nil {
			return
		}
		sess, err := deriveSessionState(accept.ConversationID, s.localPeer, accept.FromPeerID, priv, accept.ChatKexPub)
		if err != nil {
			return
		}
		conv := Conversation{
			ConversationID:    accept.ConversationID,
			PeerID:            accept.FromPeerID,
			State:             ConversationStateActive,
			LastTransportMode: TransportModeDirect,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		}
		if _, err := s.store.CreateConversation(conv, sess); err != nil {
			log.Printf("[chat] create accepted conversation failed: %v", err)
			return
		}
		_ = s.store.UpsertPeer(accept.FromPeerID, "", accept.Bio)
		if accept.RetentionMinutes > 0 {
			if _, err := s.store.UpdateConversationRetention(accept.ConversationID, accept.RetentionMinutes); err != nil {
				log.Printf("[chat] apply accepted retention failed: %v", err)
			}
		}
		_ = s.store.UpdateRequestState(accept.RequestID, RequestStateAccepted, accept.ConversationID)
	case MessageTypeSessionReject:
		var reject SessionReject
		if err := remarshal(env, &reject); err != nil {
			return
		}
		_ = s.store.UpdateRequestState(reject.RequestID, RequestStateRejected, "")
	}
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

func (s *Service) sendEnvelope(peerID string, v any) error {
	protoID := protocolForEnvelope(v)
	if err := s.sendJSON(peerID, protoID, v); err == nil {
		return nil
	}
	return s.sendViaRelay(peerID, v)
}

func (s *Service) sendViaRelay(peerID string, v any) error {
	path, firstHop, tunnelID, err := s.buildRelayPath(peerID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	str, err := s.host.NewStream(ctx, firstHop, p2p.ProtocolChatRelayE2E)
	if err != nil {
		return err
	}
	defer str.Close()
	header := tunnel.RouteHeader{
		Version:    1,
		TunnelID:   tunnelID,
		Path:       path,
		HopIndex:   0,
		TargetExit: peerID,
	}
	if err := tunnel.WriteJSONFrame(str, header); err != nil {
		return err
	}
	sess, err := tunnel.ClientHandshake(str, tunnelID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	frame, err := sess.Seal(tunnel.FrameTypeData, payload)
	if err != nil {
		return err
	}
	if err := tunnel.WriteJSONFrame(str, frame); err != nil {
		return err
	}
	closeFrame, err := sess.Seal(tunnel.FrameTypeClose, nil)
	if err == nil {
		_ = tunnel.WriteJSONFrame(str, closeFrame)
	}
	return nil
}

func (s *Service) buildRelayPath(targetPeerID string) ([]string, peer.ID, string, error) {
	if s.discovery == nil {
		return nil, "", "", errors.New("discovery store not available")
	}
	relays := s.discovery.ListRelays()
	for _, relayDesc := range relays {
		if relayDesc == nil || relayDesc.PeerID == "" || relayDesc.PeerID == s.localPeer || relayDesc.PeerID == targetPeerID {
			continue
		}
		pid, err := peer.Decode(relayDesc.PeerID)
		if err != nil {
			continue
		}
		return []string{relayDesc.PeerID, targetPeerID}, pid, uuid.NewString(), nil
	}
	return nil, "", "", errors.New("no relay path available")
}

func protocolForEnvelope(v any) coreprotocol.ID {
	switch v.(type) {
	case SessionRequest, *SessionRequest, SessionAccept, *SessionAccept, SessionReject, *SessionReject:
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
		if contact, err := s.store.GetPeer(req.FromPeerID); err == nil && contact.Blocked {
			return nil
		}
		stored := Request{
			RequestID:         req.RequestID,
			FromPeerID:        req.FromPeerID,
			ToPeerID:          req.ToPeerID,
			State:             RequestStatePending,
			IntroText:         req.IntroText,
			Nickname:          req.Nickname,
			Bio:               req.Bio,
			RemoteChatKexPub:  req.ChatKexPub,
			LastTransportMode: "relay",
			CreatedAt:         time.UnixMilli(req.SentAtUnix),
			UpdatedAt:         time.Now(),
		}
		_ = s.store.UpsertPeer(req.FromPeerID, req.Nickname, req.Bio)
		return s.store.UpsertIncomingRequest(stored)
	case MessageTypeSessionAccept:
		var accept SessionAccept
		if err := remarshal(env, &accept); err != nil {
			return err
		}
		if existingConv, err := s.store.GetConversationByPeer(accept.FromPeerID); err == nil {
			_ = s.store.UpsertPeer(accept.FromPeerID, "", accept.Bio)
			targetRetention := accept.RetentionMinutes
			if existingConv.RetentionMinutes > targetRetention {
				targetRetention = existingConv.RetentionMinutes
			}
			if targetRetention != existingConv.RetentionMinutes {
				var err error
				existingConv, err = s.store.UpdateConversationRetention(existingConv.ConversationID, targetRetention)
				if err != nil {
					return err
				}
			}
			return s.store.UpdateRequestState(accept.RequestID, RequestStateAccepted, existingConv.ConversationID)
		} else if err != sql.ErrNoRows {
			return err
		}
		_, priv, err := s.store.GetProfile(s.localPeer)
		if err != nil {
			return err
		}
		sess, err := deriveSessionState(accept.ConversationID, s.localPeer, accept.FromPeerID, priv, accept.ChatKexPub)
		if err != nil {
			return err
		}
		conv := Conversation{
			ConversationID:    accept.ConversationID,
			PeerID:            accept.FromPeerID,
			State:             ConversationStateActive,
			LastTransportMode: "relay",
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		}
		if _, err := s.store.CreateConversation(conv, sess); err != nil {
			return err
		}
		_ = s.store.UpsertPeer(accept.FromPeerID, "", accept.Bio)
		if accept.RetentionMinutes > 0 {
			if _, err := s.store.UpdateConversationRetention(accept.ConversationID, accept.RetentionMinutes); err != nil {
				return err
			}
		}
		return s.store.UpdateRequestState(accept.RequestID, RequestStateAccepted, accept.ConversationID)
	case MessageTypeSessionReject:
		var reject SessionReject
		if err := remarshal(env, &reject); err != nil {
			return err
		}
		return s.store.UpdateRequestState(reject.RequestID, RequestStateRejected, "")
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
		conv, err := s.store.UpdateConversationRetention(update.ConversationID, update.RetentionMinutes)
		if err != nil {
			return err
		}
		if err := s.store.UpdateConversationRetentionSync(update.ConversationID, "synced", time.Now()); err != nil {
			return err
		}
		ack := RetentionAck{
			Type:             MessageTypeRetentionAck,
			ConversationID:   update.ConversationID,
			FromPeerID:       s.localPeer,
			ToPeerID:         update.FromPeerID,
			RetentionMinutes: conv.RetentionMinutes,
			AckedAtUnix:      time.Now().UnixMilli(),
		}
		return s.sendEnvelope(conv.PeerID, ack)
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
		conv, err := s.store.UpdateConversationRetention(update.ConversationID, update.RetentionMinutes)
		if err != nil {
			return err
		}
		if err := s.store.UpdateConversationRetentionSync(update.ConversationID, "synced", time.Now()); err != nil {
			return err
		}
		ack := RetentionAck{
			Type:             MessageTypeRetentionAck,
			ConversationID:   update.ConversationID,
			FromPeerID:       s.localPeer,
			ToPeerID:         update.FromPeerID,
			RetentionMinutes: conv.RetentionMinutes,
			AckedAtUnix:      time.Now().UnixMilli(),
		}
		return s.sendEnvelope(conv.PeerID, ack)
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
	default:
		return nil
	}
}

func (s *Service) handleIncomingChatText(msg ChatText, transportMode string) error {
	if contact, err := s.store.GetPeer(msg.FromPeerID); err == nil && contact.Blocked {
		return nil
	}
	conv, err := s.store.GetConversation(msg.ConversationID)
	if err != nil {
		return err
	}
	sess, err := s.store.GetSessionState(msg.ConversationID)
	if err != nil {
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
	conv, err := s.store.GetConversation(msg.ConversationID)
	if err != nil {
		return err
	}
	sess, err := s.store.GetSessionState(msg.ConversationID)
	if err != nil {
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
		return ChatSyncResponse{}, err
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
		return ChatSyncResponse{}, err
	}
	return resp, nil
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
