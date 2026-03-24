package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	peer "github.com/libp2p/go-libp2p/core/peer"
)

// 中繼路徑無法綁定 libp2p RemotePeer，因此對「聲稱的發件人」用 libp2p 身份密鑰簽名；簽名域與訊息類型綁定以防跨類型重放。

func relaySignMessage(kind string, canonical []byte) []byte {
	return append(append([]byte("mesh-proxy/chat/relay/v1:"+kind+"\n"), canonical...))
}

func (s *Service) relaySignerMustBeLocal(fromPeerID string) error {
	if strings.TrimSpace(fromPeerID) != strings.TrimSpace(s.localPeer) {
		return fmt.Errorf("relay envelope signer must be local peer: want %q got %q", s.localPeer, fromPeerID)
	}
	return nil
}

func (s *Service) signRelayEnvelopeCanonical(kind string, canonical []byte) ([]byte, error) {
	msg := relaySignMessage(kind, canonical)
	priv := s.host.Peerstore().PrivKey(s.host.ID())
	if priv == nil {
		return nil, errors.New("local signing key is unavailable")
	}
	return priv.Sign(msg)
}

func (s *Service) verifyRelayPeerSignature(peerID string, message []byte, sig []byte) error {
	if len(sig) == 0 {
		return errors.New("missing relay envelope signature")
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return err
	}
	pub := s.host.Peerstore().PubKey(pid)
	if pub == nil {
		pub, err = pid.ExtractPublicKey()
		if err != nil {
			return fmt.Errorf("resolve peer public key: %w", err)
		}
	}
	ok, err := pub.Verify(message, sig)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("relay envelope signature verification failed")
	}
	return nil
}

func (s *Service) verifyRelayEnvelopeCanonical(peerID, kind string, canonical []byte, sig []byte) error {
	return s.verifyRelayPeerSignature(peerID, relaySignMessage(kind, canonical), sig)
}

func marshalChatTextForRelaySigning(m ChatText) ([]byte, error) {
	return json.Marshal(struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		MsgID          string `json:"msg_id"`
		FromPeerID     string `json:"from_peer_id"`
		ToPeerID       string `json:"to_peer_id"`
		Ciphertext     []byte `json:"ciphertext"`
		Counter        uint64 `json:"counter"`
		SentAtUnix     int64  `json:"sent_at_unix"`
	}{
		Type:           m.Type,
		ConversationID: m.ConversationID,
		MsgID:          m.MsgID,
		FromPeerID:     m.FromPeerID,
		ToPeerID:       m.ToPeerID,
		Ciphertext:     m.Ciphertext,
		Counter:        m.Counter,
		SentAtUnix:     m.SentAtUnix,
	})
}

func marshalChatFileForRelaySigning(m ChatFile) ([]byte, error) {
	return json.Marshal(struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		MsgID          string `json:"msg_id"`
		FromPeerID     string `json:"from_peer_id"`
		ToPeerID       string `json:"to_peer_id"`
		FileName       string `json:"file_name"`
		MIMEType       string `json:"mime_type"`
		FileSize       int64  `json:"file_size"`
		FileCID        string `json:"file_cid,omitempty"`
		Ciphertext     []byte `json:"ciphertext"`
		Counter        uint64 `json:"counter"`
		SentAtUnix     int64  `json:"sent_at_unix"`
	}{
		Type:           m.Type,
		ConversationID: m.ConversationID,
		MsgID:          m.MsgID,
		FromPeerID:     m.FromPeerID,
		ToPeerID:       m.ToPeerID,
		FileName:       m.FileName,
		MIMEType:       m.MIMEType,
		FileSize:       m.FileSize,
		FileCID:        m.FileCID,
		Ciphertext:     m.Ciphertext,
		Counter:        m.Counter,
		SentAtUnix:     m.SentAtUnix,
	})
}

func marshalChatFileFetchRequestForRelaySigning(m ChatFileFetchRequest) ([]byte, error) {
	return json.Marshal(struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		MsgID          string `json:"msg_id"`
		FromPeerID     string `json:"from_peer_id"`
		ToPeerID       string `json:"to_peer_id"`
		Offset         uint64 `json:"offset"`
		ChunkSize      int    `json:"chunk_size"`
		SentAtUnix     int64  `json:"sent_at_unix"`
	}{
		Type:           m.Type,
		ConversationID: m.ConversationID,
		MsgID:          m.MsgID,
		FromPeerID:     m.FromPeerID,
		ToPeerID:       m.ToPeerID,
		Offset:         m.Offset,
		ChunkSize:      m.ChunkSize,
		SentAtUnix:     m.SentAtUnix,
	})
}

func marshalChatFileFetchResponseForRelaySigning(m ChatFileFetchResponse) ([]byte, error) {
	return json.Marshal(struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		MsgID          string `json:"msg_id"`
		FromPeerID     string `json:"from_peer_id"`
		ToPeerID       string `json:"to_peer_id"`
		Offset         uint64 `json:"offset"`
		Eof            bool   `json:"eof"`
		FileSize       int64  `json:"file_size"`
		FileCID        string `json:"file_cid,omitempty"`
		Ciphertext     []byte `json:"ciphertext,omitempty"`
		Error          string `json:"error,omitempty"`
		SentAtUnix     int64  `json:"sent_at_unix"`
	}{
		Type:           m.Type,
		ConversationID: m.ConversationID,
		MsgID:          m.MsgID,
		FromPeerID:     m.FromPeerID,
		ToPeerID:       m.ToPeerID,
		Offset:         m.Offset,
		Eof:            m.Eof,
		FileSize:       m.FileSize,
		FileCID:        m.FileCID,
		Ciphertext:     m.Ciphertext,
		Error:          m.Error,
		SentAtUnix:     m.SentAtUnix,
	})
}

func relayKindForChatText(m ChatText) string {
	if m.Type != "" {
		return m.Type
	}
	return MessageTypeChatText
}

func marshalDeliveryAckForRelaySigning(m DeliveryAck) ([]byte, error) {
	return json.Marshal(struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		MsgID          string `json:"msg_id"`
		FromPeerID     string `json:"from_peer_id"`
		ToPeerID       string `json:"to_peer_id"`
		AckedAtUnix    int64  `json:"acked_at_unix"`
	}{
		Type:           m.Type,
		ConversationID: m.ConversationID,
		MsgID:          m.MsgID,
		FromPeerID:     m.FromPeerID,
		ToPeerID:       m.ToPeerID,
		AckedAtUnix:    m.AckedAtUnix,
	})
}

// verifyRelayDeliveryAckSignature 驗證中繼路徑上的 delivery_ack：聲稱發送方 FromPeerID 須以 libp2p 身份密鑰簽名（與 signRelayDeliveryAck 對應）。
// 直連流不依賴此函數，而以 RemotePeer 綁定 FromPeerID。
func (s *Service) verifyRelayDeliveryAckSignature(ack DeliveryAck) error {
	canon, err := marshalDeliveryAckForRelaySigning(ack)
	if err != nil {
		return err
	}
	return s.verifyRelayEnvelopeCanonical(ack.FromPeerID, MessageTypeDeliveryAck, canon, ack.Signature)
}

func marshalMessageRevokeForRelaySigning(m MessageRevoke) ([]byte, error) {
	return json.Marshal(struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		MsgID          string `json:"msg_id"`
		FromPeerID     string `json:"from_peer_id"`
		ToPeerID       string `json:"to_peer_id"`
		RevokedAtUnix  int64  `json:"revoked_at_unix"`
	}{
		Type:           m.Type,
		ConversationID: m.ConversationID,
		MsgID:          m.MsgID,
		FromPeerID:     m.FromPeerID,
		ToPeerID:       m.ToPeerID,
		RevokedAtUnix:  m.RevokedAtUnix,
	})
}

func marshalRetentionUpdateForRelaySigning(m RetentionUpdate) ([]byte, error) {
	return json.Marshal(struct {
		Type             string `json:"type"`
		ConversationID   string `json:"conversation_id"`
		FromPeerID       string `json:"from_peer_id"`
		ToPeerID         string `json:"to_peer_id"`
		RetentionMinutes int    `json:"retention_minutes"`
		UpdatedAtUnix    int64  `json:"updated_at_unix"`
	}{
		Type:             m.Type,
		ConversationID:   m.ConversationID,
		FromPeerID:       m.FromPeerID,
		ToPeerID:         m.ToPeerID,
		RetentionMinutes: m.RetentionMinutes,
		UpdatedAtUnix:    m.UpdatedAtUnix,
	})
}

func marshalRetentionAckForRelaySigning(m RetentionAck) ([]byte, error) {
	return json.Marshal(struct {
		Type             string `json:"type"`
		ConversationID   string `json:"conversation_id"`
		FromPeerID       string `json:"from_peer_id"`
		ToPeerID         string `json:"to_peer_id"`
		RetentionMinutes int    `json:"retention_minutes"`
		AckedAtUnix      int64  `json:"acked_at_unix"`
	}{
		Type:             m.Type,
		ConversationID:   m.ConversationID,
		FromPeerID:       m.FromPeerID,
		ToPeerID:         m.ToPeerID,
		RetentionMinutes: m.RetentionMinutes,
		AckedAtUnix:      m.AckedAtUnix,
	})
}

func marshalChatSyncRequestForRelaySigning(m ChatSyncRequest) ([]byte, error) {
	return json.Marshal(struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		FromPeerID     string `json:"from_peer_id"`
		ToPeerID       string `json:"to_peer_id"`
		NextCounter    uint64 `json:"next_counter"`
		SentAtUnix     int64  `json:"sent_at_unix"`
	}{
		Type:           m.Type,
		ConversationID: m.ConversationID,
		FromPeerID:     m.FromPeerID,
		ToPeerID:       m.ToPeerID,
		NextCounter:    m.NextCounter,
		SentAtUnix:     m.SentAtUnix,
	})
}

func marshalChatSyncResponseForRelaySigning(m ChatSyncResponse) ([]byte, error) {
	type textSig struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		MsgID          string `json:"msg_id"`
		FromPeerID     string `json:"from_peer_id"`
		ToPeerID       string `json:"to_peer_id"`
		Ciphertext     []byte `json:"ciphertext"`
		Counter        uint64 `json:"counter"`
		SentAtUnix     int64  `json:"sent_at_unix"`
	}
	type fileSig struct {
		Type           string `json:"type"`
		ConversationID string `json:"conversation_id"`
		MsgID          string `json:"msg_id"`
		FromPeerID     string `json:"from_peer_id"`
		ToPeerID       string `json:"to_peer_id"`
		FileName       string `json:"file_name"`
		MIMEType       string `json:"mime_type"`
		FileSize       int64  `json:"file_size"`
		FileCID        string `json:"file_cid,omitempty"`
		Ciphertext     []byte `json:"ciphertext"`
		Counter        uint64 `json:"counter"`
		SentAtUnix     int64  `json:"sent_at_unix"`
	}
	msgs := make([]textSig, len(m.Messages))
	for i, x := range m.Messages {
		msgs[i] = textSig{
			Type:           x.Type,
			ConversationID: x.ConversationID,
			MsgID:          x.MsgID,
			FromPeerID:     x.FromPeerID,
			ToPeerID:       x.ToPeerID,
			Ciphertext:     x.Ciphertext,
			Counter:        x.Counter,
			SentAtUnix:     x.SentAtUnix,
		}
	}
	files := make([]fileSig, len(m.Files))
	for i, x := range m.Files {
		files[i] = fileSig{
			Type:           x.Type,
			ConversationID: x.ConversationID,
			MsgID:          x.MsgID,
			FromPeerID:     x.FromPeerID,
			ToPeerID:       x.ToPeerID,
			FileName:       x.FileName,
			MIMEType:       x.MIMEType,
			FileSize:       x.FileSize,
			FileCID:        x.FileCID,
			Ciphertext:     x.Ciphertext,
			Counter:        x.Counter,
			SentAtUnix:     x.SentAtUnix,
		}
	}
	return json.Marshal(struct {
		Type              string    `json:"type"`
		ConversationID    string    `json:"conversation_id"`
		FromPeerID        string    `json:"from_peer_id"`
		RemoteSendCounter uint64    `json:"remote_send_counter,omitempty"`
		Messages          []textSig `json:"messages,omitempty"`
		Files             []fileSig `json:"files,omitempty"`
	}{
		Type:              m.Type,
		ConversationID:    m.ConversationID,
		FromPeerID:        m.FromPeerID,
		RemoteSendCounter: m.RemoteSendCounter,
		Messages:          msgs,
		Files:             files,
	})
}

func marshalSessionRequestForRelaySigning(m SessionRequest) ([]byte, error) {
	return json.Marshal(struct {
		Type             string `json:"type"`
		RequestID        string `json:"request_id"`
		FromPeerID       string `json:"from_peer_id"`
		ToPeerID         string `json:"to_peer_id"`
		Nickname         string `json:"nickname"`
		Bio              string `json:"bio"`
		AvatarName       string `json:"avatar_name"`
		RetentionMinutes int    `json:"retention_minutes"`
		IntroText        string `json:"intro_text"`
		ChatKexPub       string `json:"chat_kex_pub"`
		SentAtUnix       int64  `json:"sent_at_unix"`
	}{
		Type:             m.Type,
		RequestID:        m.RequestID,
		FromPeerID:       m.FromPeerID,
		ToPeerID:         m.ToPeerID,
		Nickname:         m.Nickname,
		Bio:              m.Bio,
		AvatarName:       m.AvatarName,
		RetentionMinutes: m.RetentionMinutes,
		IntroText:        m.IntroText,
		ChatKexPub:       m.ChatKexPub,
		SentAtUnix:       m.SentAtUnix,
	})
}

func marshalSessionAcceptForRelaySigning(m SessionAccept) ([]byte, error) {
	return json.Marshal(struct {
		Type             string `json:"type"`
		RequestID        string `json:"request_id"`
		ConversationID   string `json:"conversation_id"`
		FromPeerID       string `json:"from_peer_id"`
		ToPeerID         string `json:"to_peer_id"`
		Bio              string `json:"bio"`
		AvatarName       string `json:"avatar_name"`
		RetentionMinutes int    `json:"retention_minutes"`
		ChatKexPub       string `json:"chat_kex_pub"`
		SentAtUnix       int64  `json:"sent_at_unix"`
	}{
		Type:             m.Type,
		RequestID:        m.RequestID,
		ConversationID:   m.ConversationID,
		FromPeerID:       m.FromPeerID,
		ToPeerID:         m.ToPeerID,
		Bio:              m.Bio,
		AvatarName:       m.AvatarName,
		RetentionMinutes: m.RetentionMinutes,
		ChatKexPub:       m.ChatKexPub,
		SentAtUnix:       m.SentAtUnix,
	})
}

func marshalSessionAcceptAckForRelaySigning(m SessionAcceptAck) ([]byte, error) {
	return json.Marshal(struct {
		Type           string `json:"type"`
		RequestID      string `json:"request_id"`
		ConversationID string `json:"conversation_id"`
		FromPeerID     string `json:"from_peer_id"`
		ToPeerID       string `json:"to_peer_id"`
		SentAtUnix     int64  `json:"sent_at_unix"`
	}{
		Type:           m.Type,
		RequestID:      m.RequestID,
		ConversationID: m.ConversationID,
		FromPeerID:     m.FromPeerID,
		ToPeerID:       m.ToPeerID,
		SentAtUnix:     m.SentAtUnix,
	})
}

func marshalSessionRejectForRelaySigning(m SessionReject) ([]byte, error) {
	return json.Marshal(struct {
		Type       string `json:"type"`
		RequestID  string `json:"request_id"`
		FromPeerID string `json:"from_peer_id"`
		ToPeerID   string `json:"to_peer_id"`
		SentAtUnix int64  `json:"sent_at_unix"`
	}{
		Type:       m.Type,
		RequestID:  m.RequestID,
		FromPeerID: m.FromPeerID,
		ToPeerID:   m.ToPeerID,
		SentAtUnix: m.SentAtUnix,
	})
}

func marshalProfileSyncForRelaySigning(m ProfileSync) ([]byte, error) {
	return json.Marshal(struct {
		Type              string `json:"type"`
		FromPeerID        string `json:"from_peer_id"`
		ToPeerID          string `json:"to_peer_id"`
		Nickname          string `json:"nickname"`
		Bio               string `json:"bio"`
		AvatarName        string `json:"avatar_name"`
		BindingEthAddress string `json:"binding_eth_address"`
		SentAtUnix        int64  `json:"sent_at_unix"`
	}{
		Type:              m.Type,
		FromPeerID:        m.FromPeerID,
		ToPeerID:          m.ToPeerID,
		Nickname:          m.Nickname,
		Bio:               m.Bio,
		AvatarName:        m.AvatarName,
		BindingEthAddress: strings.TrimSpace(m.BindingEthAddress),
		SentAtUnix:        m.SentAtUnix,
	})
}

func marshalAvatarRequestForRelaySigning(m AvatarRequest) ([]byte, error) {
	return json.Marshal(struct {
		Type       string `json:"type"`
		FromPeerID string `json:"from_peer_id"`
		ToPeerID   string `json:"to_peer_id"`
		AvatarName string `json:"avatar_name"`
		SentAtUnix int64  `json:"sent_at_unix"`
	}{
		Type:       m.Type,
		FromPeerID: m.FromPeerID,
		ToPeerID:   m.ToPeerID,
		AvatarName: m.AvatarName,
		SentAtUnix: m.SentAtUnix,
	})
}

func marshalAvatarResponseForRelaySigning(m AvatarResponse) ([]byte, error) {
	return json.Marshal(struct {
		Type       string `json:"type"`
		FromPeerID string `json:"from_peer_id"`
		ToPeerID   string `json:"to_peer_id"`
		AvatarName string `json:"avatar_name"`
		AvatarData []byte `json:"avatar_data,omitempty"`
		SentAtUnix int64  `json:"sent_at_unix"`
	}{
		Type:       m.Type,
		FromPeerID: m.FromPeerID,
		ToPeerID:   m.ToPeerID,
		AvatarName: m.AvatarName,
		AvatarData: m.AvatarData,
		SentAtUnix: m.SentAtUnix,
	})
}

// attachRelaySignature 在走中繼隧道前為信封附加簽名；群組訊息已有獨立簽名則原樣透傳。
func (s *Service) attachRelaySignature(v any) (any, error) {
	switch m := v.(type) {
	case GroupControlEnvelope, *GroupControlEnvelope,
		GroupJoinRequest, *GroupJoinRequest,
		GroupLeaveRequest, *GroupLeaveRequest,
		GroupChatText, *GroupChatText,
		GroupChatFile, *GroupChatFile,
		GroupDeliveryAck, *GroupDeliveryAck:
		return v, nil

	case ChatText:
		return s.signRelayChatText(m)
	case *ChatText:
		return s.signRelayChatText(*m)

	case ChatFile:
		return s.signRelayChatFile(m)
	case *ChatFile:
		return s.signRelayChatFile(*m)

	case ChatFileFetchRequest:
		return s.signRelayChatFileFetchRequest(m)
	case *ChatFileFetchRequest:
		return s.signRelayChatFileFetchRequest(*m)

	case ChatFileFetchResponse:
		return s.signRelayChatFileFetchResponse(m)
	case *ChatFileFetchResponse:
		return s.signRelayChatFileFetchResponse(*m)

	case DeliveryAck:
		return s.signRelayDeliveryAck(m)
	case *DeliveryAck:
		return s.signRelayDeliveryAck(*m)

	case MessageRevoke:
		return s.signRelayMessageRevoke(m)
	case *MessageRevoke:
		return s.signRelayMessageRevoke(*m)

	case RetentionUpdate:
		return s.signRelayRetentionUpdate(m)
	case *RetentionUpdate:
		return s.signRelayRetentionUpdate(*m)

	case RetentionAck:
		return s.signRelayRetentionAck(m)
	case *RetentionAck:
		return s.signRelayRetentionAck(*m)

	case ChatSyncRequest:
		return s.signRelayChatSyncRequest(m)
	case *ChatSyncRequest:
		return s.signRelayChatSyncRequest(*m)

	case ChatSyncResponse:
		return s.signRelayChatSyncResponse(m)
	case *ChatSyncResponse:
		return s.signRelayChatSyncResponse(*m)

	case SessionRequest:
		return s.signRelaySessionRequest(m)
	case *SessionRequest:
		return s.signRelaySessionRequest(*m)

	case SessionAccept:
		return s.signRelaySessionAccept(m)
	case *SessionAccept:
		return s.signRelaySessionAccept(*m)

	case SessionAcceptAck:
		return s.signRelaySessionAcceptAck(m)
	case *SessionAcceptAck:
		return s.signRelaySessionAcceptAck(*m)

	case SessionReject:
		return s.signRelaySessionReject(m)
	case *SessionReject:
		return s.signRelaySessionReject(*m)

	case ProfileSync:
		return s.signRelayProfileSync(m)
	case *ProfileSync:
		return s.signRelayProfileSync(*m)

	case AvatarRequest:
		return s.signRelayAvatarRequest(m)
	case *AvatarRequest:
		return s.signRelayAvatarRequest(*m)

	case AvatarResponse:
		return s.signRelayAvatarResponse(m)
	case *AvatarResponse:
		return s.signRelayAvatarResponse(*m)

	default:
		return nil, fmt.Errorf("relay signing not implemented for %T", v)
	}
}

func (s *Service) signRelayChatText(m ChatText) (ChatText, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return ChatText{}, err
	}
	canon, err := marshalChatTextForRelaySigning(m)
	if err != nil {
		return ChatText{}, err
	}
	kind := relayKindForChatText(m)
	sig, err := s.signRelayEnvelopeCanonical(kind, canon)
	if err != nil {
		return ChatText{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayChatFile(m ChatFile) (ChatFile, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return ChatFile{}, err
	}
	canon, err := marshalChatFileForRelaySigning(m)
	if err != nil {
		return ChatFile{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeChatFile, canon)
	if err != nil {
		return ChatFile{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayChatFileFetchRequest(m ChatFileFetchRequest) (ChatFileFetchRequest, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return ChatFileFetchRequest{}, err
	}
	canon, err := marshalChatFileFetchRequestForRelaySigning(m)
	if err != nil {
		return ChatFileFetchRequest{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeChatFileFetchRequest, canon)
	if err != nil {
		return ChatFileFetchRequest{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayChatFileFetchResponse(m ChatFileFetchResponse) (ChatFileFetchResponse, error) {
	if strings.TrimSpace(m.FromPeerID) == "" {
		m.FromPeerID = s.localPeer
	} else if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return ChatFileFetchResponse{}, err
	}
	canon, err := marshalChatFileFetchResponseForRelaySigning(m)
	if err != nil {
		return ChatFileFetchResponse{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeChatFileFetchResponse, canon)
	if err != nil {
		return ChatFileFetchResponse{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayDeliveryAck(m DeliveryAck) (DeliveryAck, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return DeliveryAck{}, err
	}
	canon, err := marshalDeliveryAckForRelaySigning(m)
	if err != nil {
		return DeliveryAck{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeDeliveryAck, canon)
	if err != nil {
		return DeliveryAck{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayMessageRevoke(m MessageRevoke) (MessageRevoke, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return MessageRevoke{}, err
	}
	canon, err := marshalMessageRevokeForRelaySigning(m)
	if err != nil {
		return MessageRevoke{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeMessageRevoke, canon)
	if err != nil {
		return MessageRevoke{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayRetentionUpdate(m RetentionUpdate) (RetentionUpdate, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return RetentionUpdate{}, err
	}
	canon, err := marshalRetentionUpdateForRelaySigning(m)
	if err != nil {
		return RetentionUpdate{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeRetentionUpdate, canon)
	if err != nil {
		return RetentionUpdate{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayRetentionAck(m RetentionAck) (RetentionAck, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return RetentionAck{}, err
	}
	canon, err := marshalRetentionAckForRelaySigning(m)
	if err != nil {
		return RetentionAck{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeRetentionAck, canon)
	if err != nil {
		return RetentionAck{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayChatSyncRequest(m ChatSyncRequest) (ChatSyncRequest, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return ChatSyncRequest{}, err
	}
	canon, err := marshalChatSyncRequestForRelaySigning(m)
	if err != nil {
		return ChatSyncRequest{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeChatSyncRequest, canon)
	if err != nil {
		return ChatSyncRequest{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayChatSyncResponse(m ChatSyncResponse) (ChatSyncResponse, error) {
	if strings.TrimSpace(m.FromPeerID) == "" {
		m.FromPeerID = s.localPeer
	} else if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return ChatSyncResponse{}, err
	}
	canon, err := marshalChatSyncResponseForRelaySigning(m)
	if err != nil {
		return ChatSyncResponse{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeChatSyncResponse, canon)
	if err != nil {
		return ChatSyncResponse{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelaySessionRequest(m SessionRequest) (SessionRequest, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return SessionRequest{}, err
	}
	canon, err := marshalSessionRequestForRelaySigning(m)
	if err != nil {
		return SessionRequest{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeSessionRequest, canon)
	if err != nil {
		return SessionRequest{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelaySessionAccept(m SessionAccept) (SessionAccept, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return SessionAccept{}, err
	}
	canon, err := marshalSessionAcceptForRelaySigning(m)
	if err != nil {
		return SessionAccept{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeSessionAccept, canon)
	if err != nil {
		return SessionAccept{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelaySessionAcceptAck(m SessionAcceptAck) (SessionAcceptAck, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return SessionAcceptAck{}, err
	}
	canon, err := marshalSessionAcceptAckForRelaySigning(m)
	if err != nil {
		return SessionAcceptAck{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeSessionAcceptAck, canon)
	if err != nil {
		return SessionAcceptAck{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelaySessionReject(m SessionReject) (SessionReject, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return SessionReject{}, err
	}
	canon, err := marshalSessionRejectForRelaySigning(m)
	if err != nil {
		return SessionReject{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeSessionReject, canon)
	if err != nil {
		return SessionReject{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayProfileSync(m ProfileSync) (ProfileSync, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return ProfileSync{}, err
	}
	canon, err := marshalProfileSyncForRelaySigning(m)
	if err != nil {
		return ProfileSync{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeProfileSync, canon)
	if err != nil {
		return ProfileSync{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayAvatarRequest(m AvatarRequest) (AvatarRequest, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return AvatarRequest{}, err
	}
	canon, err := marshalAvatarRequestForRelaySigning(m)
	if err != nil {
		return AvatarRequest{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeAvatarRequest, canon)
	if err != nil {
		return AvatarRequest{}, err
	}
	m.Signature = sig
	return m, nil
}

func (s *Service) signRelayAvatarResponse(m AvatarResponse) (AvatarResponse, error) {
	if err := s.relaySignerMustBeLocal(m.FromPeerID); err != nil {
		return AvatarResponse{}, err
	}
	canon, err := marshalAvatarResponseForRelaySigning(m)
	if err != nil {
		return AvatarResponse{}, err
	}
	sig, err := s.signRelayEnvelopeCanonical(MessageTypeAvatarResponse, canon)
	if err != nil {
		return AvatarResponse{}, err
	}
	m.Signature = sig
	return m, nil
}

// verifyRelayInboundEnvelope 僅用於中繼解包後的 JSON；直連流不依賴此路徑。
// delivery_ack 的簽名改在 handleIncomingDeliveryAck（relay）內驗證，見 verifyRelayDeliveryAckSignature。
func (s *Service) verifyRelayInboundEnvelope(env map[string]any) error {
	typ, ok := env["type"].(string)
	if !ok || typ == "" {
		return errors.New("relay envelope: missing type")
	}
	switch typ {
	case MessageTypeChatText, MessageTypeGroupInviteNote:
		var msg ChatText
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalChatTextForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, relayKindForChatText(msg), canon, msg.Signature)

	case MessageTypeChatFile:
		var msg ChatFile
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalChatFileForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeChatFile, canon, msg.Signature)

	case MessageTypeChatFileFetchRequest:
		var msg ChatFileFetchRequest
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalChatFileFetchRequestForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeChatFileFetchRequest, canon, msg.Signature)

	case MessageTypeChatFileFetchResponse:
		var msg ChatFileFetchResponse
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		if strings.TrimSpace(msg.FromPeerID) == "" {
			return errors.New("relay chat file fetch response missing from_peer_id")
		}
		canon, err := marshalChatFileFetchResponseForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeChatFileFetchResponse, canon, msg.Signature)

	// delivery_ack：簽名在 handleIncomingDeliveryAck（relay / streamPeerID 為空）內強制驗證，避免與業務邏輯脫節。

	case MessageTypeMessageRevoke:
		var msg MessageRevoke
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalMessageRevokeForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeMessageRevoke, canon, msg.Signature)

	case MessageTypeRetentionUpdate:
		var msg RetentionUpdate
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalRetentionUpdateForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeRetentionUpdate, canon, msg.Signature)

	case MessageTypeRetentionAck:
		var msg RetentionAck
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalRetentionAckForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeRetentionAck, canon, msg.Signature)

	case MessageTypeChatSyncRequest:
		var msg ChatSyncRequest
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalChatSyncRequestForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeChatSyncRequest, canon, msg.Signature)

	case MessageTypeChatSyncResponse:
		var msg ChatSyncResponse
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		if strings.TrimSpace(msg.FromPeerID) == "" {
			return errors.New("relay chat sync response missing from_peer_id")
		}
		canon, err := marshalChatSyncResponseForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeChatSyncResponse, canon, msg.Signature)

	case MessageTypeSessionRequest:
		var msg SessionRequest
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalSessionRequestForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeSessionRequest, canon, msg.Signature)

	case MessageTypeSessionAccept:
		var msg SessionAccept
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalSessionAcceptForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeSessionAccept, canon, msg.Signature)

	case MessageTypeSessionAcceptAck:
		var msg SessionAcceptAck
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalSessionAcceptAckForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeSessionAcceptAck, canon, msg.Signature)

	case MessageTypeSessionReject:
		var msg SessionReject
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalSessionRejectForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeSessionReject, canon, msg.Signature)

	case MessageTypeProfileSync:
		var msg ProfileSync
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalProfileSyncForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeProfileSync, canon, msg.Signature)

	case MessageTypeAvatarRequest:
		var msg AvatarRequest
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalAvatarRequestForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeAvatarRequest, canon, msg.Signature)

	case MessageTypeAvatarResponse:
		var msg AvatarResponse
		if err := remarshal(env, &msg); err != nil {
			return err
		}
		canon, err := marshalAvatarResponseForRelaySigning(msg)
		if err != nil {
			return err
		}
		return s.verifyRelayEnvelopeCanonical(msg.FromPeerID, MessageTypeAvatarResponse, canon, msg.Signature)

	default:
		return nil
	}
}
