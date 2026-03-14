package discovery

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
	peer "github.com/libp2p/go-libp2p/core/peer"
)

// Gossip topics for node announcements.
const (
	TopicAnnounceRelay = "meshproxy.node.announce.relay"
	TopicAnnounceExit  = "meshproxy.node.announce.exit"
	TopicRevoke        = "meshproxy.node.revoke"
)

// ExitDescriptor describes exit-specific capabilities (optional; used for exit selection).
type ExitDescriptor struct {
	Country     string `json:"country"`      // ISO country code e.g. "SG", "JP"
	City        string `json:"city"`         // optional
	AllowTCP    bool   `json:"allow_tcp"`    // supports TCP forwarding (default true if nil)
	AllowUDP    bool   `json:"allow_udp"`
	RemoteDNS   bool   `json:"remote_dns"`   // supports remote DNS resolution
	BandwidthMbps int  `json:"bandwidth_mbps"`
	Tags        []string `json:"tags"`
}

// NodeDescriptor describes a node's capabilities and basic metadata.
type NodeDescriptor struct {
	Version     string   `json:"version"`
	PeerID      string   `json:"peer_id"`
	PubKey      string   `json:"pubkey"`       // base64-encoded
	Roles       []string `json:"roles"`        // e.g. ["relay"], ["relay","exit"]
	ListenAddrs []string `json:"listen_addrs"` // p2p listen multiaddrs
	Relay       bool     `json:"relay"`
	Exit        bool     `json:"exit"`
	ExitInfo    *ExitDescriptor `json:"exit_info,omitempty"` // exit-only metadata for selection
	Pricing     string   `json:"pricing"` // free-form for now
	ExpiresAt   int64    `json:"expires_at"`
	Signature   string   `json:"signature"` // base64-encoded
}

// UnsignedCopy returns a copy of descriptor without signature.
func (d *NodeDescriptor) UnsignedCopy() *NodeDescriptor {
	cp := *d
	cp.Signature = ""
	return &cp
}

// MarshalUnsignedJSON marshals descriptor without signature to JSON.
func (d *NodeDescriptor) MarshalUnsignedJSON() ([]byte, error) {
	return json.Marshal(d.UnsignedCopy())
}

// BuildSelfDescriptor constructs a descriptor for the local node.
func BuildSelfDescriptor(version string, priv crypto.PrivKey, listenAddrs []string, relay, exit bool, ttl time.Duration) (*NodeDescriptor, error) {
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		return nil, err
	}

	pubBytes, err := priv.GetPublic().Raw()
	if err != nil {
		return nil, err
	}

	roles := []string{"relay"}
	if exit {
		roles = append(roles, "exit")
	}

	desc := &NodeDescriptor{
		Version:     version,
		PeerID:      pid.String(),
		PubKey:      base64.StdEncoding.EncodeToString(pubBytes),
		Roles:       roles,
		ListenAddrs: listenAddrs,
		Relay:       relay,
		Exit:        exit,
		Pricing:     "free",
		ExpiresAt:   time.Now().Add(ttl).Unix(),
	}
	return desc, nil
}

// SignDescriptor signs the descriptor using the given private key.
func SignDescriptor(priv crypto.PrivKey, desc *NodeDescriptor) error {
	data, err := desc.MarshalUnsignedJSON()
	if err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	sig, err := priv.Sign(hash[:])
	if err != nil {
		return err
	}
	desc.Signature = base64.StdEncoding.EncodeToString(sig)
	return nil
}

// VerifyDescriptor verifies the descriptor signature against the embedded public key.
func VerifyDescriptor(desc *NodeDescriptor) (bool, error) {
	rawPub, err := base64.StdEncoding.DecodeString(desc.PubKey)
	if err != nil {
		return false, err
	}
	pub, err := crypto.UnmarshalEd25519PublicKey(rawPub)
	if err != nil {
		return false, err
	}

	data, err := desc.MarshalUnsignedJSON()
	if err != nil {
		return false, err
	}
	hash := sha256.Sum256(data)

	sig, err := base64.StdEncoding.DecodeString(desc.Signature)
	if err != nil {
		return false, err
	}
	ok, err := pub.Verify(hash[:], sig)
	if err != nil {
		return false, err
	}
	return ok, nil
}

