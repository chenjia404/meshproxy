// Package protocol 提供多跳分層加密所需的密碼學原語：X25519 密鑰交換、HKDF 派生、ChaCha20-Poly1305 AEAD。
package protocol

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/sha3"
)

const (
	// X25519KeySize 是 X25519 公鑰/私鑰字節長度。
	X25519KeySize = 32
	// AEADKeySize 是 ChaCha20-Poly1305 密鑰長度。
	AEADKeySize = chacha20poly1305.KeySize
	// AEADNonceSize 是 ChaCha20-Poly1305 nonce 長度。
	AEADNonceSize = chacha20poly1305.NonceSize
)

// HKDF labels for per-hop key derivation (document requirement).
const (
	HKDFLabelForward = "meshproxy-hop-fwd"
	HKDFLabelBackward = "meshproxy-hop-bwd"
)

// GenerateEphemeralKeyPair 生成一對 X25519 臨時密鑰。返回 (priv, pub, error)。
func GenerateEphemeralKeyPair() (priv, pub []byte, err error) {
	priv = make([]byte, X25519KeySize)
	if _, err := io.ReadFull(rand.Reader, priv); err != nil {
		return nil, nil, err
	}
	// X25519 要求 clamp 私鑰
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err = curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, nil, err
	}
	return priv, pub, nil
}

// X25519SharedSecret 計算 X25519 共享密鑰：localPriv + remotePub -> sharedSecret。
func X25519SharedSecret(localPriv, remotePub []byte) ([]byte, error) {
	if len(localPriv) != X25519KeySize || len(remotePub) != X25519KeySize {
		return nil, fmt.Errorf("x25519: invalid key size")
	}
	return curve25519.X25519(localPriv, remotePub)
}

// DeriveHopKeys 從共享密鑰用 HKDF 派生 forward 和 backward 密鑰（各 AEADKeySize 字節）。
func DeriveHopKeys(sharedSecret []byte) (forwardKey, backwardKey []byte, err error) {
	forwardKey = make([]byte, AEADKeySize)
	backwardKey = make([]byte, AEADKeySize)
	rd := hkdf.New(sha3.New256, sharedSecret, nil, []byte(HKDFLabelForward))
	if _, err := io.ReadFull(rd, forwardKey); err != nil {
		return nil, nil, err
	}
	rd = hkdf.New(sha3.New256, sharedSecret, nil, []byte(HKDFLabelBackward))
	if _, err := io.ReadFull(rd, backwardKey); err != nil {
		return nil, nil, err
	}
	return forwardKey, backwardKey, nil
}

// BuildAEADNonce 根據 direction 和 counter 構建 12 字節 nonce，保證不重複。
// direction: "fwd" 或 "bwd"；counter 遞增不重複。
func BuildAEADNonce(direction string, counter uint64) []byte {
	nonce := make([]byte, AEADNonceSize)
	binary.BigEndian.PutUint64(nonce[0:8], counter)
	if direction == "bwd" {
		nonce[8] = 1
	}
	// nonce[9:12] 可留空或備用，counter 已保證唯一
	return nonce
}

// AEADSeal 使用 ChaCha20-Poly1305 加密 plaintext，AAD 綁定 circuit_id, stream_id, direction。
// nonce 必須 12 字節且從未重複使用。
func AEADSeal(key, nonce, plaintext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonce, plaintext, aad), nil
}

// AEADOpen 使用 ChaCha20-Poly1305 解密 ciphertext。
func AEADOpen(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, aad)
}

// BuildAAD 構建 AEAD AAD：綁定 circuit_id, stream_id, direction（可選 layer_index）。
func BuildAAD(circuitID, streamID, direction string, layerIndex uint8) []byte {
	buf := make([]byte, 0, 64)
	buf = append(buf, circuitID...)
	buf = append(buf, 0)
	buf = append(buf, streamID...)
	buf = append(buf, 0)
	buf = append(buf, direction...)
	buf = append(buf, 0, layerIndex)
	return buf
}

// NewHopSessionFromKeyExchange 在客戶端完成與一 hop 的 X25519 協商後，構造 HopSession。
// localPriv 為本端臨時私鑰，remotePub 為該 hop 的臨時公鑰（32 字節）。
func NewHopSessionFromKeyExchange(peerID string, role HopRole, localPriv, remotePub []byte, createdAt int64) (*HopSession, error) {
	shared, err := X25519SharedSecret(localPriv, remotePub)
	if err != nil {
		return nil, err
	}
	fwd, bwd, err := DeriveHopKeys(shared)
	if err != nil {
		return nil, err
	}
	return &HopSession{
		PeerID:      peerID,
		Role:        role,
		SharedSecret: shared,
		ForwardKey:   fwd,
		BackwardKey:  bwd,
		CreatedAt:    createdAt,
	}, nil
}
