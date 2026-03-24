package chatrelay

import (
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"

	"github.com/chenjia404/meshproxy/internal/protocol"
)

// CipherSuiteX25519Chacha 文檔約定的套件名。
const CipherSuiteX25519Chacha = "x25519-chacha20poly1305"

// DeriveRelaySessionKeys 從 X25519 共享密鑰派生 K_session，綁定 session_id（文檔七.4）。
// initiator=true 表示 A 端（先發 connect 的一方）：tx 用於 A→B 加密，rx 用於 B→A 解密。
func DeriveRelaySessionKeys(sharedSecret []byte, sessionID string, initiator bool) (tx, rx []byte, err error) {
	if len(sharedSecret) != protocol.X25519KeySize {
		return nil, nil, fmt.Errorf("invalid shared secret length")
	}
	if sessionID == "" {
		return nil, nil, fmt.Errorf("empty session_id")
	}
	salt := sha256.Sum256([]byte("chatrelay/v1/session\x00" + sessionID))
	fwd := make([]byte, protocol.AEADKeySize)
	bwd := make([]byte, protocol.AEADKeySize)
	rd := hkdf.New(sha256.New, sharedSecret, salt[:], []byte("chatrelay/v1/fwd"))
	if _, err := io.ReadFull(rd, fwd); err != nil {
		return nil, nil, err
	}
	rd = hkdf.New(sha256.New, sharedSecret, salt[:], []byte("chatrelay/v1/bwd"))
	if _, err := io.ReadFull(rd, bwd); err != nil {
		return nil, nil, err
	}
	if initiator {
		return fwd, bwd, nil
	}
	return bwd, fwd, nil
}

// SealDataFrame 使用 K_session 加密內層業務字節（文檔六 RelayDataFrame）。
func SealDataFrame(txKey []byte, sessionID string, packetSeq uint64, plaintext []byte) (nonce []byte, ciphertext []byte, err error) {
	if len(txKey) != protocol.AEADKeySize {
		return nil, nil, fmt.Errorf("invalid tx key")
	}
	aead, err := chacha20poly1305.New(txKey)
	if err != nil {
		return nil, nil, err
	}
	nonce = protocol.BuildAEADNonce("relay-v1", packetSeq)
	aad := []byte("chatrelay/v1/data\x00" + sessionID)
	ct := aead.Seal(nil, nonce, plaintext, aad)
	return nonce, ct, nil
}

// OpenDataFrame 解密。
func OpenDataFrame(rxKey []byte, sessionID string, packetSeq uint64, nonce []byte, ciphertext []byte) ([]byte, error) {
	if len(rxKey) != protocol.AEADKeySize {
		return nil, fmt.Errorf("invalid rx key")
	}
	aead, err := chacha20poly1305.New(rxKey)
	if err != nil {
		return nil, err
	}
	aad := []byte("chatrelay/v1/data\x00" + sessionID)
	return aead.Open(nil, nonce, ciphertext, aad)
}
