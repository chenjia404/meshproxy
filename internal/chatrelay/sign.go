package chatrelay

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/crypto"
)

const signPrefixConnect = "mesh-proxy/chatrelay/v1/connect\n"
const signPrefixConnectResp = "mesh-proxy/chatrelay/v1/connect-response\n"
const signPrefixHandshake = "mesh-proxy/chatrelay/v1/handshake\n"
const signPrefixHeartbeat = "mesh-proxy/chatrelay/v1/heartbeat\n"

// 簽名前綴（供 chat / relay 套件引用）。
const (
	SignPrefixConnect     = signPrefixConnect
	SignPrefixConnectResp = signPrefixConnectResp
	SignPrefixHandshake   = signPrefixHandshake
	SignPrefixHeartbeat   = signPrefixHeartbeat
)

// CanonicalConnect 簽名覆蓋：version, session_id, src_id, dst_id, timestamp, ttl_sec
func CanonicalConnect(r *RelayConnectRequest) []byte {
	if r == nil {
		return nil
	}
	return []byte(fmt.Sprintf("%d\x1e%s\x1e%s\x1e%s\x1e%d\x1e%d",
		r.Version, r.SessionID, r.SrcID, r.DstID, r.Timestamp, r.TTLSec))
}

// CanonicalConnectResponse 簽名覆蓋：version, session_id, src_id, dst_id, relay_id, accepted, expire_at
func CanonicalConnectResponse(r *RelayConnectResponse) []byte {
	if r == nil {
		return nil
	}
	return []byte(fmt.Sprintf("%d\x1e%s\x1e%s\x1e%s\x1e%s\x1e%t\x1e%d",
		r.Version, r.SessionID, r.SrcID, r.DstID, r.RelayID, r.Accepted, r.ExpireAt))
}

// CanonicalHandshake 簽名覆蓋：version, session_id, src_id, dst_id, eph_pub, cipher_suite, timestamp
func CanonicalHandshake(version int, sessionID, srcID, dstID, ephPubB64, cipherSuite string, ts int64) []byte {
	return []byte(fmt.Sprintf("%d\x1e%s\x1e%s\x1e%s\x1e%s\x1e%s\x1e%d",
		version, sessionID, srcID, dstID, ephPubB64, cipherSuite, ts))
}

// CanonicalHeartbeat 簽名覆蓋：version, session_id, src_id, dst_id, relay_id, ping_id, sent_at
func CanonicalHeartbeat(r *RelayHeartbeat) []byte {
	if r == nil {
		return nil
	}
	return []byte(fmt.Sprintf("%d\x1e%s\x1e%s\x1e%s\x1e%s\x1e%s\x1e%d",
		r.Version, r.SessionID, r.SrcID, r.DstID, r.RelayID, r.PingID, r.SentAt))
}

// SignBytes 使用 libp2p ed25519 私鑰簽名（呼叫方傳入 Sign）。
func SignBytes(priv crypto.PrivKey, prefix string, canonical []byte) (SignatureBlock, error) {
	if priv == nil {
		return SignatureBlock{}, fmt.Errorf("nil private key")
	}
	msg := append(append([]byte{}, prefix...), canonical...)
	sig, err := priv.Sign(msg)
	if err != nil {
		return SignatureBlock{}, err
	}
	return SignatureBlock{Algorithm: "ed25519", Value: base64.StdEncoding.EncodeToString(sig)}, nil
}

// VerifyEd25519 驗證 peer 的 ed25519 簽名。
func VerifyEd25519(pub crypto.PubKey, prefix string, canonical []byte, sigB64 string) error {
	if pub == nil {
		return fmt.Errorf("nil public key")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil || len(sig) == 0 {
		return fmt.Errorf("invalid signature encoding")
	}
	msg := append(append([]byte{}, prefix...), canonical...)
	ok, err := pub.Verify(msg, sig)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}
