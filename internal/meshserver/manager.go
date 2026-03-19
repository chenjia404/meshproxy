package meshserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	corerouting "github.com/libp2p/go-libp2p/core/routing"
	routingdiscovery "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	multiaddr "github.com/multiformats/go-multiaddr"
)

const defaultDiscoveryNamespace = "meshserver"

type ConnectionConfig struct {
	Name        string
	PeerID      string
	ClientAgent string
	ProtocolID  string
}

type ConnectionInfo struct {
	Name          string `json:"name"`
	PeerID        string `json:"peer_id"`
	ClientAgent   string `json:"client_agent"`
	ProtocolID    string `json:"protocol_id"`
	Connected     bool   `json:"connected"`
	Authenticated bool   `json:"authenticated"`
	SessionID     string `json:"session_id,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
}

type Manager struct {
	mu      sync.RWMutex
	host    host.Host
	routing corerouting.Routing
	privKey crypto.PrivKey
	clients map[string]*Client
	order   []string
}

func NewManager(ctx context.Context, h host.Host, routing corerouting.Routing, privKey crypto.PrivKey, configs []ConnectionConfig) (*Manager, error) {
	m := &Manager{
		host:    h,
		routing: routing,
		privKey: privKey,
		clients: make(map[string]*Client),
	}
	for i, cfg := range configs {
		if _, err := m.Add(ctx, cfg); err != nil {
			_ = m.Close()
			return nil, fmt.Errorf("init meshserver %d: %w", i+1, err)
		}
	}
	return m, nil
}

func (m *Manager) Add(ctx context.Context, cfg ConnectionConfig) (ConnectionInfo, error) {
	if m == nil {
		return ConnectionInfo{}, ErrNotConnected
	}
	pid, initialAddrs, err := resolveMeshServerTarget(cfg.PeerID)
	if err != nil {
		return ConnectionInfo{}, err
	}
	if m.host != nil && pid == m.host.ID() {
		return ConnectionInfo{}, fmt.Errorf("meshserver peer %q is the local node; please use a different peer id or multiaddr", cfg.PeerID)
	}
	// NewClient 只接受 peer id（而不是 multiaddr）。
	// resolveMeshServerTarget 允許傳入 multiaddr，但這裡要把解析後的 peer id 丟給 NewClient。
	serverPeerID := pid.String()
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = pid.String()
	}
	if len(initialAddrs) > 0 {
		m.host.Peerstore().AddAddrs(pid, initialAddrs, time.Hour)
	}
	// If user provided multiaddr (=> initialAddrs is non-empty), connect directly using it.
	// Only peer_id-only inputs should fall back to DHT/rendezvous discovery.
	if len(initialAddrs) > 0 {
		if m.host.Network().Connectedness(pid) != network.Connected {
			connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := m.host.Connect(connectCtx, peer.AddrInfo{ID: pid, Addrs: initialAddrs}); err != nil {
				return ConnectionInfo{}, fmt.Errorf("connect meshserver peer %s (provided multiaddr): %w", serverPeerID, err)
			}
		}
	} else {
		if err := m.ensurePeerReachable(ctx, pid); err != nil {
			return ConnectionInfo{}, fmt.Errorf("resolve meshserver peer %s: %w", cfg.PeerID, err)
		}
	}
	m.mu.Lock()
	existing, exists := m.clients[name]
	m.mu.Unlock()
	if exists {
		// Make Add idempotent for the same server peer.
		// If callers repeat "add" with the same name+peer_id, do not fail.
		if existing != nil && existing.serverPeerID == pid {
			info := ConnectionInfo{
				Name:          name,
				PeerID:        existing.serverPeerID.String(),
				ClientAgent:   existing.clientAgent,
				ProtocolID:    string(existing.protocolID),
				Connected:     existing.currentStream() != nil,
				Authenticated: existing.Authenticated(),
			}
			if auth := existing.AuthResult(); auth != nil {
				info.SessionID = auth.SessionId
				info.UserID = auth.UserId
				info.DisplayName = auth.DisplayName
			}
			return info, nil
		}
		return ConnectionInfo{}, fmt.Errorf("duplicate meshserver connection name %q", name)
	}
	client, err := NewClient(ctx, m.host, m.privKey, serverPeerID, Options{
		ClientAgent: cfg.ClientAgent,
		ProtocolID:  cfg.ProtocolID,
	})
	if err != nil {
		return ConnectionInfo{}, err
	}
	info := ConnectionInfo{
		Name:          name,
		PeerID:        serverPeerID,
		ClientAgent:   client.clientAgent,
		ProtocolID:    string(client.protocolID),
		Connected:     client.currentStream() != nil,
		Authenticated: client.Authenticated(),
	}
	if auth := client.AuthResult(); auth != nil {
		info.SessionID = auth.SessionId
		info.UserID = auth.UserId
		info.DisplayName = auth.DisplayName
	}
	m.mu.Lock()
	m.clients[name] = client
	m.order = append(m.order, name)
	m.mu.Unlock()
	return info, nil
}

func resolveMeshServerTarget(raw string) (peer.ID, []multiaddr.Multiaddr, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil, fmt.Errorf("peer id is required")
	}
	if addr, err := multiaddr.NewMultiaddr(value); err == nil {
		info, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			return "", nil, fmt.Errorf("parse meshserver multiaddr: %w", err)
		}
		return info.ID, info.Addrs, nil
	}
	pid, err := peer.Decode(value)
	if err != nil {
		return "", nil, fmt.Errorf("decode peer id: %w", err)
	}
	return pid, nil, nil
}

func (m *Manager) ensurePeerReachable(ctx context.Context, pid peer.ID) error {
	if m == nil || m.host == nil {
		return ErrNotConnected
	}
	if m.host.Network().Connectedness(pid) == network.Connected {
		return nil
	}
	addrs := m.host.Peerstore().Addrs(pid)
	if len(addrs) == 0 && m.routing != nil {
		if pr, ok := m.routing.(corerouting.PeerRouting); ok {
			lookupCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if info, err := pr.FindPeer(lookupCtx, pid); err == nil {
				addrs = info.Addrs
				if len(addrs) > 0 {
					m.host.Peerstore().AddAddrs(pid, addrs, time.Hour)
				}
			}
		}
	}
	if len(addrs) == 0 && m.routing != nil {
		if found, err := m.discoverPeerAddrs(ctx, pid); err == nil && len(found) > 0 {
			addrs = found
			m.host.Peerstore().AddAddrs(pid, addrs, time.Hour)
		}
	}
	if len(addrs) == 0 {
		return fmt.Errorf("no good addresses")
	}
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := m.host.Connect(connectCtx, peer.AddrInfo{ID: pid, Addrs: addrs}); err == nil {
		return nil
	} else if m.routing == nil {
		return err
	} else {
		// If the initially available addrs are stale/unreachable (common when user provides a
		// direct multiaddr), try to refresh peer addrs and reconnect.
		var refreshed []multiaddr.Multiaddr

		if pr, ok := m.routing.(corerouting.PeerRouting); ok {
			lookupCtx, cancel2 := context.WithTimeout(ctx, 20*time.Second)
			if info, lookupErr := pr.FindPeer(lookupCtx, pid); lookupErr == nil {
				refreshed = info.Addrs
			}
			cancel2()
		}
		if len(refreshed) == 0 {
			if found, derr := m.discoverPeerAddrs(ctx, pid); derr == nil {
				refreshed = found
			}
		}

		if len(refreshed) == 0 {
			return err
		}
		m.host.Peerstore().AddAddrs(pid, refreshed, time.Hour)
		reconnectCtx, cancel3 := context.WithTimeout(ctx, 10*time.Second)
		defer cancel3()
		if reconnectErr := m.host.Connect(reconnectCtx, peer.AddrInfo{ID: pid, Addrs: refreshed}); reconnectErr == nil {
			return nil
		}
		return err
	}
}

func (m *Manager) discoverPeerAddrs(ctx context.Context, pid peer.ID) ([]multiaddr.Multiaddr, error) {
	if m == nil || m.routing == nil {
		return nil, ErrNotConnected
	}
	rd := routingdiscovery.NewRoutingDiscovery(m.routing)
	findCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	peerCh, err := rd.FindPeers(findCtx, defaultDiscoveryNamespace)
	if err != nil {
		return nil, err
	}
	addrsByString := make(map[string]multiaddr.Multiaddr)
	for found := range peerCh {
		if found.ID != pid {
			continue
		}
		for _, addr := range found.Addrs {
			if addr == nil {
				continue
			}
			addrsByString[addr.String()] = addr
		}
		if len(addrsByString) > 0 {
			break
		}
	}
	out := make([]multiaddr.Multiaddr, 0, len(addrsByString))
	for _, addr := range addrsByString {
		out = append(out, addr)
	}
	return out, nil
}

func (m *Manager) Remove(name string) error {
	if m == nil {
		return ErrNotConnected
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	client, ok := m.clients[name]
	if !ok {
		return fmt.Errorf("meshserver connection %q not found", name)
	}
	_ = client.Close()
	delete(m.clients, name)
	for i, item := range m.order {
		if item == name {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	return nil
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, client := range m.clients {
		_ = client.Close()
		delete(m.clients, name)
	}
	m.order = nil
	return nil
}

func (m *Manager) HasAny() bool {
	return m != nil && len(m.order) > 0
}

func (m *Manager) DefaultName() string {
	if m == nil || len(m.order) == 0 {
		return ""
	}
	return m.order[0]
}

func (m *Manager) Names() []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, len(m.order))
	copy(out, m.order)
	return out
}

func (m *Manager) Get(name string) (*Client, error) {
	if m == nil {
		return nil, ErrNotConnected
	}
	m.mu.RLock()
	if name == "" {
		if len(m.order) == 1 {
			name = m.order[0]
		} else if len(m.order) == 0 {
			m.mu.RUnlock()
			return nil, ErrNotConnected
		} else {
			m.mu.RUnlock()
			return nil, fmt.Errorf("meshserver connection name required")
		}
	}
	client, ok := m.clients[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("meshserver connection %q not found", name)
	}
	if client == nil {
		return nil, ErrNotConnected
	}
	// If the stream was closed (e.g. server reset), reconnect on demand.
	if client.currentStream() == nil {
		// Short timeout to avoid hanging request handlers.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		newClient, err := NewClient(ctx, m.host, m.privKey, client.serverPeerID.String(), Options{
			ClientAgent: client.clientAgent,
			ProtocolID:  string(client.protocolID),
		})
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		m.clients[name] = newClient
		m.mu.Unlock()
		return newClient, nil
	}
	return client, nil
}

func (m *Manager) List() []ConnectionInfo {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ConnectionInfo, 0, len(m.order))
	for _, name := range m.order {
		client := m.clients[name]
		if client == nil {
			continue
		}
		info := ConnectionInfo{
			Name:          name,
			PeerID:        client.serverPeerID.String(),
			ClientAgent:   client.clientAgent,
			ProtocolID:    string(client.protocolID),
			Connected:     client.currentStream() != nil,
			Authenticated: client.Authenticated(),
		}
		if auth := client.AuthResult(); auth != nil {
			info.SessionID = auth.SessionId
			info.UserID = auth.UserId
			info.DisplayName = auth.DisplayName
		}
		out = append(out, info)
	}
	return out
}
