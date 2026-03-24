package chat

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// 與 meshchat-store store-node internal/protocol 及 internal/auth 對齊（離線 store 驗簽）。

const (
	offlineDefaultTTLSec = int64(2592000) // 30d，與 store-node 預設 default_ttl_sec 一致
	offlineDeviceID      = "mesh-proxy"

	// OfflineFriendAlgoECIES 好友控制离线载荷：ECIES（临时 X25519 + 对端 libp2p Ed25519 身份映射到 Curve25519）+ ChaCha20-Poly1305；store 仅见密文与临时公钥。
	OfflineFriendAlgoECIES = "mesh-friend-ecies-v1"

	// offlineFriendMaxAgeSec 好友控制离线消息：按 CreatedAt 起算最长接受时长（与产品策略一致，不依赖 store）。
	offlineFriendMaxAgeSec = int64(30 * 24 * 3600)

	// offlineEnvelopeFutureSkewSec 允许 created_at 略晚于本地时钟（秒），防止轻微时钟不同步误拒。
	offlineEnvelopeFutureSkewSec = int64(300)
)

// OfflineCipherPayload 對應 store OfflineMessageEnvelope.cipher
type OfflineCipherPayload struct {
	Algorithm      string `json:"algorithm"`
	RecipientKeyID string `json:"recipient_key_id"`
	Nonce          string `json:"nonce"`
	Ciphertext     string `json:"ciphertext"`
}

// OfflineSignature 對應 store 簽名欄位
type OfflineSignature struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// OfflineMessageEnvelope 對應 store-node protocol.OfflineMessageEnvelope（JSON 欄位名一致）
type OfflineMessageEnvelope struct {
	Version        int                  `json:"version"`
	MsgID          string               `json:"msg_id"`
	SenderID       string               `json:"sender_id"`
	RecipientID    string               `json:"recipient_id"`
	ConversationID string               `json:"conversation_id"`
	CreatedAt      int64                `json:"created_at"`
	TTLSec         *int64               `json:"ttl_sec,omitempty"`
	Cipher         OfflineCipherPayload `json:"cipher"`
	Signature      OfflineSignature     `json:"signature"`
}

func (m *OfflineMessageEnvelope) effectiveTTL(defaultTTL int64) int64 {
	if m == nil || m.TTLSec == nil {
		return defaultTTL
	}
	return *m.TTLSec
}

// StoredMessageWire fetch 響應 items[] 單條結構（與 store-node protocol.StoredMessage 一致）
type StoredMessageWire struct {
	StoreSeq   uint64                  `json:"store_seq"`
	ReceivedAt int64                   `json:"received_at"`
	ExpireAt   int64                   `json:"expire_at"`
	Message    *OfflineMessageEnvelope `json:"message"`
}

// canonicalMessageEnvelope 與 store-node auth.CanonicalMessageEnvelope 字節級一致
func canonicalMessageEnvelope(msg *OfflineMessageEnvelope, defaultTTL int64) ([]byte, error) {
	if msg == nil {
		return nil, errors.New("nil envelope")
	}
	ttl := msg.effectiveTTL(defaultTTL)
	return json.Marshal([]any{
		msg.Version,
		msg.MsgID,
		msg.SenderID,
		msg.RecipientID,
		msg.ConversationID,
		msg.CreatedAt,
		ttl,
		msg.Cipher.Algorithm,
		msg.Cipher.RecipientKeyID,
		msg.Cipher.Nonce,
		msg.Cipher.Ciphertext,
	})
}

func canonicalAckPayload(version int, recipientID, deviceID string, ackSeq uint64, ackedAt int64) ([]byte, error) {
	return json.Marshal([]any{
		version,
		recipientID,
		deviceID,
		ackSeq,
		ackedAt,
	})
}

func encodeRecipientKeyID(counter uint64, msgType string) string {
	raw := fmt.Sprintf("%d:%s", counter, msgType)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func decodeRecipientKeyID(s string) (counter uint64, msgType string, err error) {
	if s == "" {
		return 0, "", errors.New("empty recipient_key_id")
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return 0, "", err
	}
	idx := bytes.IndexByte(b, ':')
	if idx <= 0 {
		return 0, "", errors.New("invalid recipient_key_id")
	}
	counter, err = strconv.ParseUint(string(b[:idx]), 10, 64)
	if err != nil {
		return 0, "", err
	}
	msgType = string(b[idx+1:])
	if msgType == "" {
		return 0, "", errors.New("missing msg_type in recipient_key_id")
	}
	return counter, msgType, nil
}

func signOfflineEnvelope(priv crypto.PrivKey, msg *OfflineMessageEnvelope, defaultTTL int64) error {
	if priv == nil || msg == nil {
		return errors.New("nil key or envelope")
	}
	payload, err := canonicalMessageEnvelope(msg, defaultTTL)
	if err != nil {
		return err
	}
	sig, err := priv.Sign(payload)
	if err != nil {
		return err
	}
	msg.Signature.Algorithm = "ed25519"
	msg.Signature.Value = base64.StdEncoding.EncodeToString(sig)
	return nil
}

func verifyOfflineEnvelope(senderPeer string, msg *OfflineMessageEnvelope, defaultTTL int64) error {
	if msg == nil {
		return errors.New("nil message")
	}
	if msg.Signature.Algorithm != "ed25519" {
		return errors.New("unsupported signature algorithm")
	}
	pid, err := peer.Decode(senderPeer)
	if err != nil {
		return fmt.Errorf("sender_id: %w", err)
	}
	pubKey, err := pid.ExtractPublicKey()
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(msg.Signature.Value)
	if err != nil {
		return err
	}
	payload, err := canonicalMessageEnvelope(msg, defaultTTL)
	if err != nil {
		return err
	}
	ok, err := pubKey.Verify(payload, sig)
	if err != nil || !ok {
		return errors.New("signature verification failed")
	}
	return nil
}

// offlineEnvelopeCreatedUnix 将 envelope.created_at 规范为 Unix 秒（兼容毫秒时间戳）。
func offlineEnvelopeCreatedUnix(env *OfflineMessageEnvelope) int64 {
	if env == nil {
		return 0
	}
	if env.CreatedAt > 1_000_000_000_000 {
		return env.CreatedAt / 1000
	}
	return env.CreatedAt
}

// validateOfflineEnvelopeTiming 客户端侧时效：不信任 store 重放历史条目；需在验签通过后调用。
// - wire.ExpireAt：若 store 提供且已过期则拒收。
// - created_at + ttl_sec：不得超过签名人声明的 TTL（canonical 已含 ttl）。
// - 好友控制：额外将有效窗口上限封顶为 offlineFriendMaxAgeSec（30 天）。
func validateOfflineEnvelopeTiming(env *OfflineMessageEnvelope, wire *StoredMessageWire, defaultTTL int64, now time.Time) error {
	if env == nil {
		return errors.New("nil envelope")
	}
	nowUnix := now.Unix()
	if wire != nil && wire.ExpireAt > 0 && nowUnix > wire.ExpireAt {
		return errors.New("offline message: past store expire_at")
	}
	created := offlineEnvelopeCreatedUnix(env)
	if created <= 0 {
		return errors.New("offline envelope: invalid created_at")
	}
	if nowUnix+offlineEnvelopeFutureSkewSec < created {
		return errors.New("offline envelope: created_at in the future")
	}
	ttl := env.effectiveTTL(defaultTTL)
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if env.Cipher.Algorithm == OfflineFriendAlgoECIES {
		if ttl > offlineFriendMaxAgeSec {
			ttl = offlineFriendMaxAgeSec
		}
	}
	if nowUnix > created+ttl {
		return errors.New("offline message: past signed ttl window")
	}
	return nil
}
