package chat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// buildOfflineFriendEnvelope 構建與 store-node 兼容的 OfflineMessageEnvelope；cipher 內為明文 JSON（mesh-friend-plain-json）。
func (s *Service) buildOfflineFriendEnvelope(kind, recipientPeer, msgID string, payloadJSON []byte) (*OfflineMessageEnvelope, error) {
	if s == nil {
		return nil, errors.New("nil service")
	}
	recipientPeer = strings.TrimSpace(recipientPeer)
	if recipientPeer == "" || msgID == "" {
		return nil, errors.New("offline friend: empty peer or msg id")
	}
	convID := deriveStableConversationID(s.localPeer, recipientPeer)
	ttl := offlineDefaultTTLSec
	now := time.Now().UTC()
	nonce := make([]byte, 12)
	env := &OfflineMessageEnvelope{
		Version:        1,
		MsgID:          msgID,
		SenderID:       s.localPeer,
		RecipientID:    recipientPeer,
		ConversationID: convID,
		CreatedAt:      now.Unix(),
		TTLSec:         &ttl,
		Cipher: OfflineCipherPayload{
			Algorithm:      OfflineFriendAlgoPlain,
			RecipientKeyID: encodeRecipientKeyID(0, kind),
			Nonce:          base64.StdEncoding.EncodeToString(nonce),
			Ciphertext:     base64.StdEncoding.EncodeToString(payloadJSON),
		},
		Signature: OfflineSignature{},
	}
	return env, nil
}

func (s *Service) tryOfflineFriendStoreSubmit(kind, recipientPeer, msgID string, payload any, quiet bool) error {
	if s == nil || s.nodePriv == nil || s.host == nil {
		return errors.New("offline friend store: not configured")
	}
	nodes := s.mergeOfflineStoreNodes(recipientPeer)
	if len(nodes) == 0 {
		return errors.New("offline friend store: no store nodes")
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	env, err := s.buildOfflineFriendEnvelope(kind, recipientPeer, msgID, b)
	if err != nil {
		return err
	}
	if err := signOfflineEnvelope(s.nodePriv, env, offlineDefaultTTLSec); err != nil {
		return err
	}
	return s.submitSignedOfflineEnvelope(env, quiet)
}

// sendFriendControlEnvelope 直連失敗則中繼；中繼成功或直連失敗時最佳努力寫入離線 store（明文 JSON + 外層簽名）。
func (s *Service) sendFriendControlEnvelope(peerID string, v any, kind string, msgID string) error {
	usedRelay, err := s.sendEnvelopeDirectOrRelay(peerID, v)
	if err != nil {
		if subErr := s.tryOfflineFriendStoreSubmit(kind, peerID, msgID, v, false); subErr != nil {
			log.Printf("[chat] offline friend store after send fail kind=%s id=%s: %v", kind, msgID, subErr)
		}
		return err
	}
	if usedRelay {
		if subErr := s.tryOfflineFriendStoreSubmit(kind, peerID, msgID, v, true); subErr != nil {
			log.Printf("[chat] offline friend store after relay kind=%s id=%s: %v", kind, msgID, subErr)
		}
	}
	return nil
}

// offlineFriendInnerSenderMustMatchEnv 離線好友載荷為明文 JSON，須與外層 OfflineMessageEnvelope.SenderID（已驗簽）一致，防止用自己的 key 簽封包卻在內層冒充他人。
func offlineFriendInnerSenderMustMatchEnv(env *OfflineMessageEnvelope, innerFromPeerID string) error {
	if env == nil {
		return errors.New("offline friend: nil envelope")
	}
	if strings.TrimSpace(innerFromPeerID) != strings.TrimSpace(env.SenderID) {
		return fmt.Errorf("offline friend: inner from_peer_id does not match envelope sender_id")
	}
	return nil
}

func (s *Service) processOfflineFriendPayload(env *OfflineMessageEnvelope, storeSeq uint64) (uint64, error) {
	payload, err := base64.StdEncoding.DecodeString(env.Cipher.Ciphertext)
	if err != nil {
		return 0, err
	}
	_, kind, err := decodeRecipientKeyID(env.Cipher.RecipientKeyID)
	if err != nil {
		return 0, err
	}
	switch kind {
	case MessageTypeSessionRequest:
		var req SessionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return 0, err
		}
		if err := offlineFriendInnerSenderMustMatchEnv(env, req.FromPeerID); err != nil {
			return 0, err
		}
		if err := s.handleIncomingSessionRequestEnvelope(req, TransportModeDirect, true); err != nil {
			return 0, err
		}
	case MessageTypeSessionAccept:
		var acc SessionAccept
		if err := json.Unmarshal(payload, &acc); err != nil {
			return 0, err
		}
		if err := offlineFriendInnerSenderMustMatchEnv(env, acc.FromPeerID); err != nil {
			return 0, err
		}
		if err := s.handleIncomingSessionAcceptEnvelope(acc, true, true); err != nil {
			return 0, err
		}
	case MessageTypeSessionReject:
		var rej SessionReject
		if err := json.Unmarshal(payload, &rej); err != nil {
			return 0, err
		}
		if err := offlineFriendInnerSenderMustMatchEnv(env, rej.FromPeerID); err != nil {
			return 0, err
		}
		if err := s.handleIncomingSessionRejectEnvelope(rej, true); err != nil {
			return 0, err
		}
	case MessageTypeSessionAcceptAck:
		var ack SessionAcceptAck
		if err := json.Unmarshal(payload, &ack); err != nil {
			return 0, err
		}
		if err := offlineFriendInnerSenderMustMatchEnv(env, ack.FromPeerID); err != nil {
			return 0, err
		}
		if err := s.handleIncomingSessionAcceptAckEnvelope(ack); err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("unknown offline friend kind %q", kind)
	}
	return storeSeq, nil
}
