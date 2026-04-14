package discovery

import (
	"context"
	"encoding/json"
	"log"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"

	"github.com/chenjia404/meshproxy/internal/p2p"
)

// Manager manages node discovery through gossip and local cache.
type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc

	host   host.Host
	gossip *p2p.Gossip
	store  *Store

	self           *NodeDescriptor
	muteAnnounce   bool // 为 true 时不广播自身 descriptor（server_mode 省流量）
}

// NewManager creates a new discovery manager.
func NewManager(parent context.Context, h host.Host, gossip *p2p.Gossip, store *Store, self *NodeDescriptor) *Manager {
	ctx, cancel := context.WithCancel(parent)
	return &Manager{
		ctx:    ctx,
		cancel: cancel,
		host:   h,
		gossip: gossip,
		store:  store,
		self:   self,
	}
}

// Start begins periodic self announcement and gossip subscriptions.
func (m *Manager) Start() {
	// store self descriptor
	if m.self != nil {
		m.store.Upsert(m.self)
	}

	go m.runAnnouncements()
	go m.runSubscriber(TopicAnnounceRelay)
	go m.runSubscriber(TopicAnnounceExit)
}

// SetMuteAnnounce 禁止广播自身 descriptor（server_mode 下不需要被其他节点发现）。
func (m *Manager) SetMuteAnnounce(mute bool) {
	m.muteAnnounce = mute
}

// Stop stops the manager.
func (m *Manager) Stop() {
	m.cancel()
}

// Store returns the underlying discovery store.
func (m *Manager) Store() *Store {
	return m.store
}

// GetAll returns all known node descriptors.
func (m *Manager) GetAll() []*NodeDescriptor {
	return m.store.GetAll()
}

func (m *Manager) runAnnouncements() {
	ps := m.gossip.PubSub()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if m.self == nil || m.muteAnnounce {
				continue
			}
			payload, err := json.Marshal(m.self)
			if err != nil {
				log.Printf("[discovery] marshal self descriptor: %v", err)
				continue
			}

			if m.self.Relay {
				if err := ps.Publish(TopicAnnounceRelay, payload); err != nil {
					log.Printf("[discovery] publish relay announce: %v", err)
				}
			}
			if m.self.Exit {
				if err := ps.Publish(TopicAnnounceExit, payload); err != nil {
					log.Printf("[discovery] publish exit announce: %v", err)
				}
			}
		}
	}
}

func (m *Manager) runSubscriber(topic string) {
	ps := m.gossip.PubSub()
	sub, err := ps.Subscribe(topic)
	if err != nil {
		log.Printf("[discovery] subscribe %s failed: %v", topic, err)
		return
	}

	for {
		msg, err := sub.Next(m.ctx)
		if err != nil {
			return
		}

		var desc NodeDescriptor
		if err := json.Unmarshal(msg.Data, &desc); err != nil {
			log.Printf("[discovery] unmarshal descriptor: %v", err)
			continue
		}

		ok, err := VerifyDescriptor(&desc)
		if err != nil || !ok {
			log.Printf("[discovery] invalid descriptor from %s: %v", desc.PeerID, err)
			continue
		}

		m.store.Upsert(&desc)
	}
}

