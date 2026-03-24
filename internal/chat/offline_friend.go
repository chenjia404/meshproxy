package chat

import (
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/chenjia404/meshproxy/internal/protocol"
)

// buildOfflineFriendEnvelope 构建 OfflineMessageEnvelope；内层 JSON 经 ECIES（mesh-friend-ecies-v1）加密，store 不可读业务内容。
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
	nonce := make([]byte, protocol.AEADNonceSize)
	if _, err := crand.Read(nonce); err != nil {
		return nil, err
	}
	env := &OfflineMessageEnvelope{
		Version:        1,
		MsgID:          msgID,
		SenderID:       s.localPeer,
		RecipientID:    recipientPeer,
		ConversationID: convID,
		CreatedAt:      now.Unix(),
		TTLSec:         &ttl,
		Cipher: OfflineCipherPayload{
			Algorithm:      OfflineFriendAlgoECIES,
			RecipientKeyID: encodeRecipientKeyID(0, kind),
			Nonce:          base64.StdEncoding.EncodeToString(nonce),
			Ciphertext:     "",
		},
		Signature: OfflineSignature{},
	}
	aad := offlineFriendECIESAAD(env, offlineDefaultTTLSec)
	ct, err := encryptOfflineFriendECIES(recipientPeer, payloadJSON, aad, nonce)
	if err != nil {
		return nil, err
	}
	env.Cipher.Ciphertext = base64.StdEncoding.EncodeToString(ct)
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

// sendFriendControlEnvelope 直连失败则中继；中继成功或直连失败时最佳努力写入离线 store（内层 ECIES + 外层签名）。
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

// offlineFriendInnerSenderMustMatchEnv 解密后的内层 JSON 中 from_peer_id 须与外層已验签的 SenderID 一致。
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
	payload, err := s.decryptOfflineFriendPayloadBytes(env)
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
