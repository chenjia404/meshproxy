package binding

import (
	"encoding/base64"
	"errors"
	"strings"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// SignWithPrivKey 使用節點 libp2p 私鑰對 canonical 位元組簽名，回傳 Base64。
func SignWithPrivKey(priv crypto.PrivKey, canonical []byte) (string, error) {
	if priv == nil {
		return "", errors.New("binding: nil private key")
	}
	if len(canonical) == 0 {
		return "", errors.New("binding: empty canonical message")
	}
	sig, err := priv.Sign(canonical)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifyPeerSignature 以 PeerID 對應之公鑰驗證 Base64 簽名。
func VerifyPeerSignature(peerID string, canonical []byte, peerSigB64 string) error {
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return errors.New("binding: empty peer_id")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(peerSigB64))
	if err != nil {
		return err
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return err
	}
	pub, err := pid.ExtractPublicKey()
	if err != nil {
		return err
	}
	ok, err := pub.Verify(canonical, sig)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("binding: invalid peer signature")
	}
	return nil
}
