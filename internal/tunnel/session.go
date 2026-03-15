package tunnel

import (
	"fmt"
	"time"

	"github.com/chenjia404/meshproxy/internal/protocol"
)

type Session struct {
	TunnelID  string
	txKey     []byte
	rxKey     []byte
	txCounter uint64
	rxCounter uint64
}

func ClientHandshake(rw interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
}, tunnelID string) (*Session, error) {
	priv, pub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		return nil, err
	}
	hello := ClientHello{
		Type:      FrameTypeClientHello,
		TunnelID:  tunnelID,
		PublicKey: pub,
		Timestamp: time.Now().UnixMilli(),
	}
	if err := WriteJSONFrame(rw, hello); err != nil {
		return nil, err
	}
	var resp ServerHello
	if err := ReadJSONFrame(rw, &resp); err != nil {
		return nil, err
	}
	if resp.Type != FrameTypeServerHello || resp.TunnelID != tunnelID || len(resp.PublicKey) != protocol.X25519KeySize {
		return nil, fmt.Errorf("invalid server hello")
	}
	return newSessionFromKeys(tunnelID, priv, resp.PublicKey, true)
}

func ServerHandshake(rw interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
}, tunnelID string) (*Session, error) {
	var hello ClientHello
	if err := ReadJSONFrame(rw, &hello); err != nil {
		return nil, err
	}
	if hello.Type != FrameTypeClientHello || hello.TunnelID != tunnelID || len(hello.PublicKey) != protocol.X25519KeySize {
		return nil, fmt.Errorf("invalid client hello")
	}
	priv, pub, err := protocol.GenerateEphemeralKeyPair()
	if err != nil {
		return nil, err
	}
	resp := ServerHello{
		Type:      FrameTypeServerHello,
		TunnelID:  tunnelID,
		PublicKey: pub,
		Timestamp: time.Now().UnixMilli(),
	}
	if err := WriteJSONFrame(rw, resp); err != nil {
		return nil, err
	}
	return newSessionFromKeys(tunnelID, priv, hello.PublicKey, false)
}

func newSessionFromKeys(tunnelID string, localPriv, remotePub []byte, clientSide bool) (*Session, error) {
	shared, err := protocol.X25519SharedSecret(localPriv, remotePub)
	if err != nil {
		return nil, err
	}
	fwd, bwd, err := protocol.DeriveHopKeys(shared)
	if err != nil {
		return nil, err
	}
	sess := &Session{TunnelID: tunnelID}
	if clientSide {
		sess.txKey = fwd
		sess.rxKey = bwd
	} else {
		sess.txKey = bwd
		sess.rxKey = fwd
	}
	return sess, nil
}

func (s *Session) Seal(frameType string, plaintext []byte) (EncryptedFrame, error) {
	nonce := protocol.BuildAEADNonce("fwd", s.txCounter)
	aad := buildAAD(s.TunnelID, frameType, s.txCounter)
	ct, err := protocol.AEADSeal(s.txKey, nonce, plaintext, aad)
	if err != nil {
		return EncryptedFrame{}, err
	}
	frame := EncryptedFrame{
		Type:       frameType,
		Seq:        s.txCounter,
		Ciphertext: ct,
		SentAtUnix: time.Now().UnixMilli(),
	}
	s.txCounter++
	return frame, nil
}

func (s *Session) Open(frame EncryptedFrame) ([]byte, error) {
	nonce := protocol.BuildAEADNonce("fwd", frame.Seq)
	aad := buildAAD(s.TunnelID, frame.Type, frame.Seq)
	plain, err := protocol.AEADOpen(s.rxKey, nonce, frame.Ciphertext, aad)
	if err != nil {
		return nil, err
	}
	s.rxCounter = frame.Seq + 1
	return plain, nil
}

func buildAAD(tunnelID, frameType string, seq uint64) []byte {
	return []byte(fmt.Sprintf("%s\x00%s\x00%d", tunnelID, frameType, seq))
}
