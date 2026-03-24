package chat

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"filippo.io/edwards25519"
	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/chenjia404/meshproxy/internal/protocol"
)

// offlineFriendECIESAAD 绑定信封元数据与 cipher 公开字段，防止密文被挪用到其他 envelope。
func offlineFriendECIESAAD(env *OfflineMessageEnvelope, defaultTTL int64) []byte {
	if env == nil {
		return nil
	}
	ttlVal := env.effectiveTTL(defaultTTL)
	if env.TTLSec != nil {
		ttlVal = *env.TTLSec
	}
	buf := make([]byte, 0, 256)
	buf = append(buf, []byte("mesh-proxy/offline_friend/ecies-aad/v1\x00")...)
	buf = binary.BigEndian.AppendUint64(buf, uint64(env.Version))
	buf = append(buf, env.MsgID...)
	buf = append(buf, 0)
	buf = append(buf, env.SenderID...)
	buf = append(buf, 0)
	buf = append(buf, env.RecipientID...)
	buf = append(buf, 0)
	buf = append(buf, env.ConversationID...)
	buf = append(buf, 0)
	buf = binary.BigEndian.AppendUint64(buf, uint64(env.CreatedAt))
	buf = binary.BigEndian.AppendUint64(buf, uint64(ttlVal))
	buf = append(buf, env.Cipher.Algorithm...)
	buf = append(buf, 0)
	buf = append(buf, env.Cipher.RecipientKeyID...)
	buf = append(buf, 0)
	buf = append(buf, env.Cipher.Nonce...)
	return buf
}

func peerIDToEd25519Pub32(peerID string) ([]byte, error) {
	pid, err := peer.Decode(strings.TrimSpace(peerID))
	if err != nil {
		return nil, err
	}
	pk, err := pid.ExtractPublicKey()
	if err != nil {
		return nil, err
	}
	raw, err := pk.Raw()
	if err != nil {
		return nil, err
	}
	if len(raw) == 32 {
		return raw, nil
	}
	if len(raw) == 36 {
		return raw[len(raw)-32:], nil
	}
	return nil, fmt.Errorf("offline friend ecies: unexpected ed25519 public key length %d", len(raw))
}

func ed25519PublicKeyToMontgomery(pub32 []byte) ([]byte, error) {
	if len(pub32) != 32 {
		return nil, errors.New("offline friend ecies: ed25519 public key must be 32 bytes")
	}
	var p edwards25519.Point
	if _, err := p.SetBytes(pub32); err != nil {
		return nil, err
	}
	return p.BytesMontgomery(), nil
}

func ed25519PrivateKeyToMontgomery(priv libp2pcrypto.PrivKey) ([]byte, error) {
	if priv == nil {
		return nil, errors.New("offline friend ecies: nil private key")
	}
	raw, err := priv.Raw()
	if err != nil {
		return nil, err
	}
	var seed []byte
	switch len(raw) {
	case 32:
		seed = raw
	case 64:
		seed = raw[:32]
	default:
		return nil, fmt.Errorf("offline friend ecies: unexpected ed25519 private raw length %d", len(raw))
	}
	h := sha512.Sum512(seed)
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64
	return h[:32], nil
}

func encryptOfflineFriendECIES(recipientPeerID string, plaintext, aad, nonce []byte) ([]byte, error) {
	if len(nonce) != protocol.AEADNonceSize {
		return nil, fmt.Errorf("offline friend ecies: nonce must be %d bytes", protocol.AEADNonceSize)
	}
	pub32, err := peerIDToEd25519Pub32(recipientPeerID)
	if err != nil {
		return nil, err
	}
	montPub, err := ed25519PublicKeyToMontgomery(pub32)
	if err != nil {
		return nil, err
	}
	ephPriv, ephPub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		return nil, err
	}
	shared, err := protocol.X25519SharedSecret(ephPriv, montPub)
	if err != nil {
		return nil, err
	}
	key, err := protocol.DeriveOfflineFriendAEADKey(shared)
	if err != nil {
		return nil, err
	}
	sealed, err := protocol.AEADSeal(key, nonce, plaintext, aad)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(ephPub)+len(sealed))
	out = append(out, ephPub...)
	out = append(out, sealed...)
	return out, nil
}

func decryptOfflineFriendECIES(localPriv libp2pcrypto.PrivKey, nonce, ciphertextWithEph, aad []byte) ([]byte, error) {
	if len(nonce) != protocol.AEADNonceSize {
		return nil, fmt.Errorf("offline friend ecies: nonce must be %d bytes", protocol.AEADNonceSize)
	}
	if len(ciphertextWithEph) < protocol.X25519KeySize+16 { // 临时公钥 + 最短 AEAD（仅认证标签）
		return nil, errors.New("offline friend ecies: ciphertext too short")
	}
	ephPub := ciphertextWithEph[:protocol.X25519KeySize]
	sealed := ciphertextWithEph[protocol.X25519KeySize:]
	montPriv, err := ed25519PrivateKeyToMontgomery(localPriv)
	if err != nil {
		return nil, err
	}
	shared, err := protocol.X25519SharedSecret(montPriv, ephPub)
	if err != nil {
		return nil, err
	}
	key, err := protocol.DeriveOfflineFriendAEADKey(shared)
	if err != nil {
		return nil, err
	}
	return protocol.AEADOpen(key, nonce, sealed, aad)
}

func (s *Service) decryptOfflineFriendPayloadBytes(env *OfflineMessageEnvelope) ([]byte, error) {
	if s == nil || s.nodePriv == nil {
		return nil, errors.New("offline friend: nil service or node key")
	}
	if env.Cipher.Algorithm != OfflineFriendAlgoECIES {
		return nil, fmt.Errorf("offline friend: unsupported algorithm %q", env.Cipher.Algorithm)
	}
	ct, err := base64.StdEncoding.DecodeString(env.Cipher.Ciphertext)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Cipher.Nonce)
	if err != nil {
		return nil, err
	}
	aad := offlineFriendECIESAAD(env, offlineDefaultTTLSec)
	return decryptOfflineFriendECIES(s.nodePriv, nonce, ct, aad)
}
