package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
	peer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/mr-tron/base58"
)

// Manager handles loading and persisting the node identity.
type Manager struct {
	privateKey crypto.PrivKey
	path       string
}

// NewManager creates a new Manager and ensures the identity key exists on disk.
func NewManager(path string) (*Manager, error) {
	if path == "" {
		return nil, errors.New("identity path must not be empty")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create identity dir: %w", err)
	}

	priv, err := loadOrCreate(path)
	if err != nil {
		return nil, err
	}

	return &Manager{
		privateKey: priv,
		path:       path,
	}, nil
}

// PrivateKey returns the libp2p private key.
func (m *Manager) PrivateKey() crypto.PrivKey {
	return m.privateKey
}

// ExportPrivateKeyBase58 exports the current in-memory Ed25519 private key as base58.
func (m *Manager) ExportPrivateKeyBase58() (string, error) {
	if m == nil || m.privateKey == nil {
		return "", errors.New("identity not available")
	}
	raw, err := m.privateKey.Raw()
	if err != nil {
		return "", fmt.Errorf("read private key bytes: %w", err)
	}
	return base58.Encode(raw), nil
}

// ImportPrivateKeyBase58 stages a new private key on disk and returns its peer ID.
// The running process must restart before the new identity becomes active.
func (m *Manager) ImportPrivateKeyBase58(encoded string) (string, error) {
	if m == nil {
		return "", errors.New("identity manager not available")
	}
	raw, err := base58.Decode(encoded)
	if err != nil {
		return "", fmt.Errorf("decode base58 private key: %w", err)
	}
	priv, err := crypto.UnmarshalEd25519PrivateKey(raw)
	if err != nil {
		return "", fmt.Errorf("unmarshal ed25519 private key: %w", err)
	}
	pid, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		return "", fmt.Errorf("derive peer id: %w", err)
	}
	if err := savePrivateKeyToFile(m.path, priv); err != nil {
		return "", err
	}
	return pid.String(), nil
}

// loadOrCreate loads an existing Ed25519 key or creates and persists a new one.
func loadOrCreate(path string) (crypto.PrivKey, error) {
	if _, err := os.Stat(path); err == nil {
		return loadFromFile(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat identity file: %w", err)
	}

	return createAndSave(path)
}

func loadFromFile(path string) (crypto.PrivKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read identity file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil || block.Type != "ED25519 PRIVATE KEY" {
		return nil, errors.New("invalid identity pem file")
	}

	raw := block.Bytes
	if l := len(raw); l != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("unexpected ed25519 key size: %d", l)
	}

	libp2pKey, err := crypto.UnmarshalEd25519PrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal ed25519 key: %w", err)
	}
	return libp2pKey, nil
}

func createAndSave(path string) (crypto.PrivKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	block := &pem.Block{
		Type:  "ED25519 PRIVATE KEY",
		Bytes: priv,
	}

	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("write identity file: %w", err)
	}

	libp2pKey, err := crypto.UnmarshalEd25519PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("unmarshal generated ed25519 key: %w", err)
	}
	return libp2pKey, nil
}

func savePrivateKeyToFile(path string, priv crypto.PrivKey) error {
	raw, err := priv.Raw()
	if err != nil {
		return fmt.Errorf("read private key bytes: %w", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{
		Type:  "ED25519 PRIVATE KEY",
		Bytes: raw,
	}), 0o600); err != nil {
		return fmt.Errorf("write identity file: %w", err)
	}
	return nil
}

