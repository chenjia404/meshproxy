package chat

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
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
}

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
	safe.Go("chat.retentionLoop", func() { s.runRetentionLoop() })
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

func (s *Service) UpdateProfile(nickname string) (Profile, error) {
	return s.store.UpdateProfileNickname(s.localPeer, nickname)
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

func (s *Service) SendRequest(toPeerID, introText string) (Request, error) {
	if contact, err := s.store.GetPeer(toPeerID); err == nil && contact.Blocked {
		return Request{}, errors.New("peer is blocked")
	}
	profile, _, err := s.store.GetProfile(s.localPeer)
	if err != nil {
		return Request{}, err
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
		LastTransportMode: TransportModeDirect,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	wire := SessionRequest{
		Type:       MessageTypeSessionRequest,
		RequestID:  req.RequestID,
		FromPeerID: req.FromPeerID,
		ToPeerID:   req.ToPeerID,
		Nickname:   profile.Nickname,
		IntroText:  req.IntroText,
		ChatKexPub: profile.ChatKexPub,
		SentAtUnix: time.Now().UnixMilli(),
	}
	req.RemoteChatKexPub = profile.ChatKexPub
	if err := s.store.SaveOutgoingRequest(req); err != nil {
		return Request{}, err
	}
	_ = s.store.UpsertPeer(toPeerID, "")
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
	if err := s.store.UpdateRequestState(requestID, RequestStateAccepted, convID); err != nil {
		return Conversation{}, err
	}
	_ = s.store.UpsertPeer(req.FromPeerID, req.Nickname)
	if err := s.ensurePeerConnected(req.FromPeerID); err != nil {
		return conv, nil
	}
	wire := SessionAccept{
		Type:           MessageTypeSessionAccept,
		RequestID:      req.RequestID,
		ConversationID: convID,
		FromPeerID:     s.localPeer,
		ToPeerID:       req.FromPeerID,
		ChatKexPub:     profile.ChatKexPub,
		SentAtUnix:     time.Now().UnixMilli(),
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
	if err := s.ensurePeerConnected(conv.PeerID); err != nil {
		return Message{}, err
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
		CreatedAt:      time.Now(),
	}
	msg, err = s.store.AddMessage(msg, ciphertext)
	if err != nil {
		return Message{}, err
	}
	wire := ChatText{
		Type:           MessageTypeChatText,
		ConversationID: conversationID,
		MsgID:          msg.MsgID,
		FromPeerID:     s.localPeer,
		ToPeerID:       conv.PeerID,
		Ciphertext:     ciphertext,
		Counter:        counter,
		SentAtUnix:     msg.CreatedAt.UnixMilli(),
	}
	if err := s.sendEnvelope(conv.PeerID, wire); err != nil {
		return Message{}, err
	}
	if err := s.store.UpdateSendCounter(conversationID, counter+1); err != nil {
		return Message{}, err
	}
	msg.State = MessageStateSentToTransport
	return s.store.AddMessage(msg, ciphertext)
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
	return s.ensurePeerConnected(peerID)
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
			RemoteChatKexPub:  req.ChatKexPub,
			LastTransportMode: TransportModeDirect,
			CreatedAt:         time.UnixMilli(req.SentAtUnix),
			UpdatedAt:         time.Now(),
		}
		_ = s.store.UpsertPeer(req.FromPeerID, req.Nickname)
		if err := s.store.UpsertIncomingRequest(stored); err != nil {
			log.Printf("[chat] save request failed: %v", err)
		}
	case MessageTypeSessionAccept:
		var accept SessionAccept
		if err := remarshal(env, &accept); err != nil {
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
		_ = s.store.UpsertPeer(accept.FromPeerID, "")
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
	_ = s.store.MarkMessageDelivered(ack.MsgID, time.UnixMilli(ack.AckedAtUnix))
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
			RemoteChatKexPub:  req.ChatKexPub,
			LastTransportMode: "relay",
			CreatedAt:         time.UnixMilli(req.SentAtUnix),
			UpdatedAt:         time.Now(),
		}
		_ = s.store.UpsertPeer(req.FromPeerID, req.Nickname)
		return s.store.UpsertIncomingRequest(stored)
	case MessageTypeSessionAccept:
		var accept SessionAccept
		if err := remarshal(env, &accept); err != nil {
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
		_ = s.store.UpsertPeer(accept.FromPeerID, "")
		return s.store.UpdateRequestState(accept.RequestID, RequestStateAccepted, accept.ConversationID)
	case MessageTypeSessionReject:
		var reject SessionReject
		if err := remarshal(env, &reject); err != nil {
			return err
		}
		return s.store.UpdateRequestState(reject.RequestID, RequestStateRejected, "")
	case MessageTypeChatText:
		var msg ChatText
		if err := remarshal(env, &msg); err != nil {
			return err
		}
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
		nonce := protocol.BuildAEADNonce("fwd", msg.Counter)
		aad := []byte(msg.ConversationID + "\x00chat_text")
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
			MsgType:        MessageTypeChatText,
			Plaintext:      string(plain),
			TransportMode:  "relay",
			State:          MessageStateReceived,
			Counter:        msg.Counter,
			CreatedAt:      time.UnixMilli(msg.SentAtUnix),
		}
		if _, err := s.store.AddMessage(incoming, msg.Ciphertext); err != nil {
			return err
		}
		_ = s.store.UpsertPeer(msg.FromPeerID, "")
		_ = s.store.UpdateRecvCounter(msg.ConversationID, msg.Counter+1)
		ack := DeliveryAck{
			Type:           MessageTypeDeliveryAck,
			ConversationID: msg.ConversationID,
			MsgID:          msg.MsgID,
			FromPeerID:     s.localPeer,
			ToPeerID:       msg.FromPeerID,
			AckedAtUnix:    time.Now().UnixMilli(),
		}
		return s.sendEnvelope(conv.PeerID, ack)
	case MessageTypeDeliveryAck:
		var ack DeliveryAck
		if err := remarshal(env, &ack); err != nil {
			return err
		}
		return s.store.MarkMessageDelivered(ack.MsgID, time.UnixMilli(ack.AckedAtUnix))
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
	case MessageTypeChatText:
		var msg ChatText
		if err := remarshal(env, &msg); err != nil {
			return err
		}
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
		nonce := protocol.BuildAEADNonce("fwd", msg.Counter)
		aad := []byte(msg.ConversationID + "\x00chat_text")
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
			MsgType:        MessageTypeChatText,
			Plaintext:      string(plain),
			TransportMode:  TransportModeDirect,
			State:          MessageStateReceived,
			Counter:        msg.Counter,
			CreatedAt:      time.UnixMilli(msg.SentAtUnix),
		}
		if _, err := s.store.AddMessage(incoming, msg.Ciphertext); err != nil {
			return err
		}
		_ = s.store.UpsertPeer(msg.FromPeerID, "")
		_ = s.store.UpdateRecvCounter(msg.ConversationID, msg.Counter+1)
		ack := DeliveryAck{
			Type:           MessageTypeDeliveryAck,
			ConversationID: msg.ConversationID,
			MsgID:          msg.MsgID,
			FromPeerID:     s.localPeer,
			ToPeerID:       msg.FromPeerID,
			AckedAtUnix:    time.Now().UnixMilli(),
		}
		return s.sendEnvelope(conv.PeerID, ack)
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
		return s.store.MarkMessageDelivered(ack.MsgID, time.UnixMilli(ack.AckedAtUnix))
	default:
		return nil
	}
}

func (s *Service) runRetentionLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			if err := s.store.CleanupExpiredMessages(now.UTC()); err != nil {
				log.Printf("[chat] retention cleanup failed: %v", err)
			}
		}
	}
}
