package storenode

import (
	"github.com/chenjia404/meshproxy/internal/binding"
	crypto "github.com/libp2p/go-libp2p/core/crypto"
)

// SignProfile 使用 store 规范 canonical 字节签名（非 mesh-proxy 带前缀格式）。
func SignProfile(priv crypto.PrivKey, p *ChannelProfile) error {
	payload, err := CanonicalProfile(p)
	if err != nil {
		return err
	}
	sig, err := binding.SignWithPrivKey(priv, payload)
	if err != nil {
		return err
	}
	p.Signature = sig
	return nil
}

// SignHead 签名频道头。
func SignHead(priv crypto.PrivKey, h *ChannelHead) error {
	payload, err := CanonicalHead(h)
	if err != nil {
		return err
	}
	sig, err := binding.SignWithPrivKey(priv, payload)
	if err != nil {
		return err
	}
	h.Signature = sig
	return nil
}

// SignMessage 签名消息（owner 代签）。
func SignMessage(priv crypto.PrivKey, m *ChannelMessage) error {
	payload, err := CanonicalMessage(m)
	if err != nil {
		return err
	}
	sig, err := binding.SignWithPrivKey(priv, payload)
	if err != nil {
		return err
	}
	m.Signature = sig
	return nil
}
