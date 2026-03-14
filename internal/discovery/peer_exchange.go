package discovery

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
	peer "github.com/libp2p/go-libp2p/core/peer"
)

const (
	// TopicPeerExchange carries signed snapshots of known relay descriptors and addrs.
	TopicPeerExchange = "meshproxy.node.peer_exchange"
)

// PeerExchangeEntry describes one known relay plus the sender's observed real-IP addrs for it.
type PeerExchangeEntry struct {
	Descriptor    *NodeDescriptor `json:"descriptor"`
	ObservedAddrs []string        `json:"observed_addrs,omitempty"`
}

// PeerExchangeMessage is a signed snapshot published over gossip.
type PeerExchangeMessage struct {
	Version   string              `json:"version"`
	Sender    *NodeDescriptor     `json:"sender"`
	Entries   []PeerExchangeEntry `json:"entries"`
	SentAt    int64               `json:"sent_at"`
	ExpiresAt int64               `json:"expires_at"`
	Signature string              `json:"signature"`
}

// UnsignedCopy returns a copy of the message without its signature.
func (m *PeerExchangeMessage) UnsignedCopy() *PeerExchangeMessage {
	cp := *m
	cp.Signature = ""
	return &cp
}

// MarshalUnsignedJSON marshals the message without its signature.
func (m *PeerExchangeMessage) MarshalUnsignedJSON() ([]byte, error) {
	return json.Marshal(m.UnsignedCopy())
}

// SignPeerExchange signs the message using the sender's identity key.
func SignPeerExchange(priv crypto.PrivKey, msg *PeerExchangeMessage) error {
	data, err := msg.MarshalUnsignedJSON()
	if err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	sig, err := priv.Sign(hash[:])
	if err != nil {
		return err
	}
	msg.Signature = base64.StdEncoding.EncodeToString(sig)
	return nil
}

// VerifyPeerExchange verifies the sender descriptor and the message signature.
func VerifyPeerExchange(msg *PeerExchangeMessage) (bool, error) {
	if msg == nil || msg.Sender == nil {
		return false, fmt.Errorf("missing sender")
	}
	ok, err := VerifyDescriptor(msg.Sender)
	if err != nil || !ok {
		return false, fmt.Errorf("verify sender descriptor: %w", err)
	}

	rawPub, err := base64.StdEncoding.DecodeString(msg.Sender.PubKey)
	if err != nil {
		return false, err
	}
	pub, err := crypto.UnmarshalEd25519PublicKey(rawPub)
	if err != nil {
		return false, err
	}
	pid, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return false, err
	}
	if pid.String() != msg.Sender.PeerID {
		return false, fmt.Errorf("sender peer id mismatch")
	}

	data, err := msg.MarshalUnsignedJSON()
	if err != nil {
		return false, err
	}
	hash := sha256.Sum256(data)
	sig, err := base64.StdEncoding.DecodeString(msg.Signature)
	if err != nil {
		return false, err
	}
	return pub.Verify(hash[:], sig)
}
