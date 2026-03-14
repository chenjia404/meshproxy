package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	peerstore "github.com/libp2p/go-libp2p/core/peerstore"
	multiaddr "github.com/multiformats/go-multiaddr"

	"github.com/chenjia404/meshproxy/internal/api"
	"github.com/chenjia404/meshproxy/internal/client"
	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/discovery"
	"github.com/chenjia404/meshproxy/internal/exit"
	"github.com/chenjia404/meshproxy/internal/geoip"
	"github.com/chenjia404/meshproxy/internal/identity"
	"github.com/chenjia404/meshproxy/internal/p2p"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/relay"
	"github.com/chenjia404/meshproxy/internal/relaycache"
	"github.com/chenjia404/meshproxy/internal/store"
)

// App is the main meshproxy application wiring together all components.
type App struct {
	ctx       context.Context
	cfg       config.Config
	startTime time.Time

	idMgr *identity.Manager
	store *store.MemoryStore

	host   *p2p.Host
	dht    *p2p.DHT
	gossip *p2p.Gossip

	discovery *discovery.Manager

	socks5   *client.Socks5Server
	localAPI *api.LocalAPI

	circuitStore   *store.CircuitStore
	pathSelector   *client.PathSelector
	circuitManager *client.CircuitManager
	streamMgr      *client.StreamManager
	recentErrors   *api.RecentErrorsStore
	relayCache     *relaycache.Cache
	selfDesc       *discovery.NodeDescriptor

	peerExchangeOnce sync.Map
}

const (
	peerExchangeInterval      = time.Minute
	peerExchangeTTL           = 2 * time.Minute
	peerExchangeMaxEntries    = 16
	peerExchangeMaxAddrs      = 4
	peerExchangeDialTimeout   = 8 * time.Second
	peerExchangeStreamTimeout = 10 * time.Second
)

// New creates and initializes a new App instance.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	cachePath := filepath.Join(cfg.DataDir, "relays.json")
	relayCache, err := relaycache.New(cachePath)
	if err != nil {
		return nil, fmt.Errorf("init relay cache: %w", err)
	}

	a := &App{
		ctx:          ctx,
		cfg:          cfg,
		startTime:    time.Now(),
		store:        store.NewMemoryStore(),
		circuitStore: store.NewCircuitStore(),
		relayCache:   relayCache,
	}

	// Identity
	idMgr, err := identity.NewManager(cfg.IdentityKeyPath)
	if err != nil {
		return nil, fmt.Errorf("init identity: %w", err)
	}
	a.idMgr = idMgr

	// P2P host
	h, err := p2p.NewHost(ctx, idMgr.PrivateKey(), cfg.P2P.ListenAddrs)
	if err != nil {
		return nil, fmt.Errorf("init p2p host: %w", err)
	}
	a.host = h

	// DHT
	dhtComp, err := p2p.NewDHT(ctx, h.Host)
	if err != nil {
		return nil, fmt.Errorf("init dht: %w", err)
	}
	a.dht = dhtComp

	// DHT-based peer discovery: all nodes sharing the same discovery tag
	// will try to find and connect to each other automatically.
	p2p.StartDiscovery(ctx, h.Host, dhtComp.Routing(), cfg.P2P.DiscoveryTag)

	// Gossip
	gossipComp, err := p2p.NewGossip(ctx, h.Host)
	if err != nil {
		return nil, fmt.Errorf("init gossip: %w", err)
	}
	a.gossip = gossipComp

	// Discovery manager
	selfDesc, err := discovery.BuildSelfDescriptor("v0.1.0", idMgr.PrivateKey(), a.P2PListenAddrs(), true, cfg.Mode == config.ModeRelayExit, time.Minute*2)
	if err != nil {
		return nil, fmt.Errorf("build self descriptor: %w", err)
	}
	if err := discovery.SignDescriptor(idMgr.PrivateKey(), selfDesc); err != nil {
		return nil, fmt.Errorf("sign self descriptor: %w", err)
	}
	a.selfDesc = selfDesc
	discoveryStore := discovery.NewStore()
	discoveryMgr := discovery.NewManager(ctx, h.Host, gossipComp, discoveryStore, selfDesc)
	discoveryMgr.Start()
	a.discovery = discoveryMgr
	a.installDirectPeerExchange()
	if a.relayCache != nil {
		cfg.P2P.BootstrapPeers = appendUniquePeers(cfg.P2P.BootstrapPeers, a.cachedBootstrapAddrs())
		a.cfg.P2P.BootstrapPeers = cfg.P2P.BootstrapPeers
	}
	go a.runRelayCache(ctx)
	go a.runPeerExchange(ctx)

	// Learn exit peer addrs (DHT FindPeer + Connect) so GeoIP gets real IP soon after discovery.
	go runPeerAddrLearner(ctx, h.Host, dhtComp.Routing(), discoveryStore, 25*time.Second)

	// Bootstrap
	p2p.Bootstrap(ctx, h.Host, cfg.P2P.BootstrapPeers)

	// Local SOCKS5 and observability
	streamMgr := client.NewStreamManager()
	a.streamMgr = streamMgr
	a.recentErrors = api.NewRecentErrorsStore(100)

	// Path selector and circuit manager
	localPeerID := a.PeerID()
	selector := client.NewPathSelector(discoveryStore, localPeerID, client.DefaultRoutePolicy())
	selector.SetExitSelection(&cfg.Client.ExitSelection)
	// GeoIP: resolve exit node country from IP when descriptor has no ExitInfo.Country
	switch g := cfg.Client.GeoIP.Provider; g {
	case "ip-api", "ip_api":
		ttl := time.Duration(cfg.Client.GeoIP.CacheTTLMinutes) * time.Minute
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		selector.SetGeoIPResolver(geoip.NewCachedResolver(geoip.NewIPAPIResolver(), ttl))
	case "geolite2", "maxmind":
		inner, err := geoip.NewGeoLite2Resolver(cfg.DataDir)
		if err != nil {
			return nil, fmt.Errorf("geoip geolite2: %w", err)
		}
		ttl := time.Duration(cfg.Client.GeoIP.CacheTTLMinutes) * time.Minute
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		selector.SetGeoIPResolver(geoip.NewCachedResolver(inner, ttl))
	}
	// Prefer peerstore/observed addrs for GeoIP so we get the peer's real public IP (not NAT/local from descriptor).
	selector.SetPeerAddrsProvider(func(peerID string) []string {
		pid, err := peer.Decode(peerID)
		if err != nil {
			return nil
		}
		addrs := a.Host().Peerstore().Addrs(pid)
		out := make([]string, 0, len(addrs))
		for _, m := range addrs {
			out = append(out, m.String())
		}
		return out
	})
	a.pathSelector = selector
	cm := client.NewCircuitManager(ctx, a.Host(), a.circuitStore, selector, streamMgr)
	a.circuitManager = cm
	cm.SetBuildRetries(cfg.Client.BuildRetries)
	cm.SetBeginTCPRetries(cfg.Client.BeginTCPRetries)
	cm.SetBeginConnectTimeout(time.Duration(cfg.Client.BeginConnectTimeoutSeconds) * time.Second)
	cm.SetHeartbeatConfig(client.HeartbeatConfig{
		Enabled:           cfg.Client.HeartbeatEnabled,
		Interval:          time.Duration(cfg.Client.HeartbeatIntervalSeconds) * time.Second,
		Timeout:           time.Duration(cfg.Client.HeartbeatTimeoutSeconds) * time.Second,
		FailureThreshold:  cfg.Client.HeartbeatFailureThreshold,
		SkipWhenActiveFor: time.Duration(cfg.Client.SkipHeartbeatWhenActiveSeconds) * time.Second,
	})
	cm.StartHeartbeatLoop(ctx)

	// Circuit pool: pre-build circuits, replenish on failure, idle timeout
	pool := client.NewCircuitPool(cfg.CircuitPool, cm)
	cm.SetPool(pool)
	pool.StartMaintenance(ctx)

	// Relay service when this node relays (relay or relay+exit).
	if cfg.Mode == config.ModeRelay || cfg.Mode == config.ModeRelayExit {
		_ = relay.NewService(a.Host())
	}
	// Exit service when this node is relay+exit（帶出口策略檢查）。始終創建 Policy 以便出口政策 API 可註冊。
	var exitSvc *exit.Service
	if cfg.Mode == config.ModeRelayExit {
		policy := exit.NewPolicyChecker(cfg.Exit) // cfg.Exit 為 nil 時使用預設策略
		exitSvc = exit.NewService(a.Host(), policy)
	}

	socks := client.NewSocks5Server(cfg.Socks5.Listen, cm, selector, streamMgr, &errorRecorderAdapter{store: a.recentErrors})
	socks.SetAllowUDPAssociate(cfg.Socks5.AllowUDPAssociate)
	if err := socks.Start(); err != nil {
		return nil, fmt.Errorf("start socks5: %w", err)
	}
	a.socks5 = socks

	// Local API (full observability: status, nodes, relays, exits, circuits, streams, pool, scores, errors, metrics)
	opts := &api.LocalAPIOpts{
		Relays:              discoveryStore,
		Exits:               discoveryStore,
		Streams:             &streamsAPIAdapter{sm: streamMgr, circuits: a.circuitStore},
		Pool:                &poolStatusAPIAdapter{cm: cm},
		CircuitPoolConfig:   &poolConfigAPIAdapter{cm: cm},
		Scores:              &scoresPlaceholder{},
		Errors:              a.recentErrors,
		Metrics:             &metricsSummaryAdapter{app: a},
		ExitSelection:       selector,
		ExitCandidates:      selector,
		ExitCountryResolver: selector,
		ConfigPath:          cfg.ConfigFilePath,
		ExitService:         exitSvc,
	}
	localAPI := api.NewLocalAPI(cfg.API.Listen, a, a.discovery, a, opts)
	localAPI.Start()
	a.localAPI = localAPI

	return a, nil
}

// Run blocks until the context is cancelled and then shuts down components.
func (a *App) Run() error {
	<-a.ctx.Done()
	log.Printf("shutting down meshproxy...")

	var firstErr error

	if err := a.localAPI.Shutdown(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := a.socks5.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if a.discovery != nil {
		a.discovery.Stop()
	}
	if err := a.dht.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := a.host.Close(); err != nil && firstErr == nil {
		firstErr = err
	}

	return firstErr
}

// PeerID returns the string representation of the local peer ID.
func (a *App) PeerID() string {
	if a.host == nil {
		return ""
	}
	id, err := peer.IDFromPrivateKey(a.idMgr.PrivateKey())
	if err != nil {
		log.Printf("derive peer id failed: %v", err)
		return ""
	}
	return id.String()
}

// Mode returns the configured node mode.
func (a *App) Mode() string {
	return a.cfg.Mode
}

// Socks5Listen returns the listen address of the local SOCKS5 server.
func (a *App) Socks5Listen() string {
	if a.socks5 == nil {
		return ""
	}
	return a.socks5.Addr()
}

// P2PListenAddrs returns the p2p listen addresses as strings.
func (a *App) P2PListenAddrs() []string {
	if a.host == nil {
		return nil
	}
	addrs := a.host.Host.Addrs()
	res := make([]string, 0, len(addrs))
	for _, m := range addrs {
		res = append(res, m.String())
	}
	return res
}

// StartTime returns when the application instance was created.
func (a *App) StartTime() time.Time {
	return a.startTime
}

// Host returns the underlying libp2p host, mainly for internal usage.
func (a *App) Host() host.Host {
	if a.host == nil {
		return nil
	}
	return a.host.Host
}

// ListCircuits implements api.CircuitProvider.
func (a *App) ListCircuits() []protocol.CircuitInfo {
	if a.circuitStore == nil {
		return nil
	}
	return a.circuitStore.GetAll()
}

// streamsAPIAdapter adapts StreamManager to api.StreamsProvider.
type streamsAPIAdapter struct {
	sm       *client.StreamManager
	circuits *store.CircuitStore
}

func (s *streamsAPIAdapter) ListStreams() []api.StreamInfoResponse {
	list := s.sm.ListStreams()
	out := make([]api.StreamInfoResponse, len(list))
	for i := range list {
		var relayPeerID, exitPeerID string
		var hopCount int
		if s.circuits != nil {
			if rec, ok := s.circuits.Get(list[i].CircuitID); ok && rec != nil {
				hopCount = len(rec.Plan.Hops)
				if hopCount > 0 && rec.Plan.Hops[0].IsRelay {
					relayPeerID = rec.Plan.Hops[0].PeerID
				}
				if rec.Plan.ExitHopIndex >= 0 && rec.Plan.ExitHopIndex < hopCount {
					exitHop := rec.Plan.Hops[rec.Plan.ExitHopIndex]
					if exitHop.IsExit {
						exitPeerID = exitHop.PeerID
					}
				}
			}
		}
		out[i] = api.StreamInfoResponse{
			ID:          list[i].ID,
			CircuitID:   list[i].CircuitID,
			TargetHost:  list[i].TargetHost,
			TargetPort:  list[i].TargetPort,
			State:       list[i].State,
			RelayPeerID: relayPeerID,
			ExitPeerID:  exitPeerID,
			HopCount:    hopCount,
		}
	}
	return out
}

// poolStatusAPIAdapter adapts CircuitManager to api.PoolStatusProvider.
type poolStatusAPIAdapter struct {
	cm *client.CircuitManager
}

func (p *poolStatusAPIAdapter) GetPoolStatus() *api.PoolStatusResponse {
	s := p.cm.GetPoolStatus()
	if s == nil {
		return nil
	}
	r := &api.PoolStatusResponse{Kinds: make(map[string]api.PoolKindStatusResponse)}
	for k, v := range s.Kinds {
		r.Kinds[k] = api.PoolKindStatusResponse{
			IdleCount:  v.IdleCount,
			InUseCount: v.InUseCount,
			TotalCount: v.TotalCount,
		}
	}
	return r
}

type poolConfigAPIAdapter struct {
	cm *client.CircuitManager
}

func (p *poolConfigAPIAdapter) GetPoolConfig() *config.CircuitPoolConfig {
	return p.cm.GetPoolConfig()
}

func (p *poolConfigAPIAdapter) SetPoolTotalLimits(minTotal, maxTotal int) bool {
	return p.cm.SetPoolTotalLimits(minTotal, maxTotal)
}

// errorRecorderAdapter adapts api.RecentErrorsStore to client.ErrorRecorder.
type errorRecorderAdapter struct {
	store *api.RecentErrorsStore
}

func (e *errorRecorderAdapter) Record(category, message string) {
	if e.store != nil {
		e.store.Add(category, message)
	}
}

// scoresPlaceholder implements api.ScoresProvider (peer score not implemented yet).
type scoresPlaceholder struct{}

func (scoresPlaceholder) GetScores() any {
	return map[string]any{"peers": []any{}}
}

// metricsSummaryAdapter aggregates status for api.MetricsSummaryProvider.
type metricsSummaryAdapter struct {
	app *App
}

func (m *metricsSummaryAdapter) GetSummary() map[string]any {
	a := m.app
	streams := a.streamMgr.ListStreams()
	relays := a.discovery.Store().ListRelays()
	exits := a.discovery.Store().ListExits()
	errorsCount := 0
	if a.recentErrors != nil {
		errorsCount = len(a.recentErrors.GetRecent())
	}
	poolStatus := (*api.PoolStatusResponse)(nil)
	if a.circuitManager != nil {
		if s := a.circuitManager.GetPoolStatus(); s != nil {
			poolStatus = &api.PoolStatusResponse{Kinds: make(map[string]api.PoolKindStatusResponse)}
			for k, v := range s.Kinds {
				poolStatus.Kinds[k] = api.PoolKindStatusResponse{
					IdleCount:  v.IdleCount,
					InUseCount: v.InUseCount,
					TotalCount: v.TotalCount,
				}
			}
		}
	}
	return map[string]any{
		"mode":                a.Mode(),
		"peer_id":             a.PeerID(),
		"uptime_seconds":      int64(time.Since(a.StartTime()).Seconds()),
		"circuits_total":      a.circuitStore.Count(),
		"streams_active":      len(streams),
		"relays_known":        len(relays),
		"exits_known":         len(exits),
		"errors_recent_count": errorsCount,
		"pool_status":         poolStatus,
	}
}

func appendUniquePeers(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base))
	for _, addr := range base {
		seen[addr] = struct{}{}
	}
	for _, addr := range extra {
		if addr == "" {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		base = append(base, addr)
		seen[addr] = struct{}{}
	}
	return base
}

func (a *App) cachedBootstrapAddrs() []string {
	if a.relayCache == nil {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, rec := range a.relayCache.Records() {
		if rec == nil {
			continue
		}
		for _, addr := range rec.Addrs {
			norm := ensurePeerMultiaddr(addr, rec.PeerID)
			if norm == "" {
				continue
			}
			if _, ok := seen[norm]; ok {
				continue
			}
			seen[norm] = struct{}{}
			out = append(out, norm)
		}
	}
	return out
}

func ensurePeerMultiaddr(addr, peerID string) string {
	if addr == "" || peerID == "" {
		return ""
	}
	m, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return ""
	}
	if _, err := m.ValueForProtocol(multiaddr.P_P2P); err == nil {
		return m.String()
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return ""
	}
	p2pSeg, err := multiaddr.NewMultiaddr("/p2p/" + pid.String())
	if err != nil {
		return ""
	}
	return m.Encapsulate(p2pSeg).String()
}

func (a *App) runRelayCache(ctx context.Context) {
	if a.relayCache == nil || a.discovery == nil {
		return
	}
	a.syncRelayCache()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.syncRelayCache()
		}
	}
}

func (a *App) syncRelayCache() {
	if a.relayCache == nil || a.discovery == nil {
		return
	}
	for _, desc := range a.discovery.Store().ListRelays() {
		addrs := a.relayAddrs(desc)
		if len(addrs) == 0 {
			continue
		}
		if err := a.relayCache.Add(desc.PeerID, addrs); err != nil {
			log.Printf("[relaycache] persist relay %s: %v", desc.PeerID, err)
		}
	}
}

func (a *App) runPeerExchange(ctx context.Context) {
	if a.gossip == nil || a.discovery == nil || a.idMgr == nil || a.selfDesc == nil {
		return
	}
	go a.runPeerExchangeSubscriber(ctx)
	a.publishPeerExchange()

	ticker := time.NewTicker(peerExchangeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.publishPeerExchange()
		}
	}
}

func (a *App) runPeerExchangeSubscriber(ctx context.Context) {
	sub, err := a.gossip.PubSub().Subscribe(discovery.TopicPeerExchange)
	if err != nil {
		log.Printf("[peerx] subscribe failed: %v", err)
		return
	}
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			return
		}
		var px discovery.PeerExchangeMessage
		if err := json.Unmarshal(msg.Data, &px); err != nil {
			continue
		}
		ok, err := discovery.VerifyPeerExchange(&px)
		if err != nil || !ok {
			continue
		}
		if px.Sender != nil && px.Sender.PeerID == a.PeerID() {
			continue
		}
		if px.ExpiresAt > 0 && time.Now().Unix() > px.ExpiresAt {
			continue
		}
		a.applyPeerExchange(&px)
	}
}

func (a *App) publishPeerExchange() {
	msg, err := a.buildPeerExchangeMessage(false)
	if err != nil || msg == nil || len(msg.Entries) == 0 {
		return
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if err := a.gossip.PubSub().Publish(discovery.TopicPeerExchange, payload); err != nil {
		log.Printf("[peerx] publish failed: %v", err)
	}
}

func (a *App) buildPeerExchangeMessage(allowEmpty bool) (*discovery.PeerExchangeMessage, error) {
	if a.discovery == nil || a.selfDesc == nil || a.idMgr == nil {
		return nil, nil
	}
	descs := a.discovery.Store().ListRelays()
	sort.Slice(descs, func(i, j int) bool {
		return descs[i].PeerID < descs[j].PeerID
	})
	entries := make([]discovery.PeerExchangeEntry, 0, minInt(len(descs), peerExchangeMaxEntries))
	for _, desc := range descs {
		if desc == nil || !desc.Relay {
			continue
		}
		addrs := a.relayAddrs(desc)
		if len(addrs) == 0 {
			continue
		}
		if len(addrs) > peerExchangeMaxAddrs {
			addrs = addrs[:peerExchangeMaxAddrs]
		}
		descCopy := *desc
		entries = append(entries, discovery.PeerExchangeEntry{
			Descriptor:    &descCopy,
			ObservedAddrs: append([]string(nil), addrs...),
		})
		if len(entries) >= peerExchangeMaxEntries {
			break
		}
	}
	if len(entries) == 0 && !allowEmpty {
		return nil, nil
	}
	senderCopy := *a.selfDesc
	now := time.Now()
	msg := &discovery.PeerExchangeMessage{
		Version:   "v1",
		Sender:    &senderCopy,
		Entries:   entries,
		SentAt:    now.Unix(),
		ExpiresAt: now.Add(peerExchangeTTL).Unix(),
	}
	if err := discovery.SignPeerExchange(a.idMgr.PrivateKey(), msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func (a *App) installDirectPeerExchange() {
	if a.host == nil {
		return
	}
	a.Host().SetStreamHandler(p2p.ProtocolPeerX, a.handlePeerExchangeStream)
	a.Host().Network().Notify(&network.NotifyBundle{
		ConnectedF: func(_ network.Network, conn network.Conn) {
			a.onPeerConnected(conn.RemotePeer())
		},
	})
}

func (a *App) onPeerConnected(pid peer.ID) {
	if a == nil || a.host == nil || pid == "" || pid == a.Host().ID() {
		return
	}
	if _, loaded := a.peerExchangeOnce.LoadOrStore(pid.String(), struct{}{}); loaded {
		return
	}
	go a.exchangePeerSnapshot(pid)
}

func (a *App) exchangePeerSnapshot(pid peer.ID) {
	if a.host == nil || pid == "" {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, peerExchangeDialTimeout)
	defer cancel()
	stream, err := a.Host().NewStream(ctx, pid, p2p.ProtocolPeerX)
	if err != nil {
		return
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(peerExchangeStreamTimeout))

	req, err := a.buildPeerExchangeMessage(true)
	if err != nil || req == nil {
		return
	}
	if err := json.NewEncoder(stream).Encode(req); err != nil {
		_ = stream.Reset()
		return
	}

	var resp discovery.PeerExchangeMessage
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		_ = stream.Reset()
		return
	}
	if resp.Sender != nil && resp.Sender.PeerID == a.PeerID() {
		return
	}
	if resp.ExpiresAt > 0 && time.Now().Unix() > resp.ExpiresAt {
		return
	}
	ok, err := discovery.VerifyPeerExchange(&resp)
	if err != nil || !ok {
		return
	}
	a.applyPeerExchange(&resp)
}

func (a *App) handlePeerExchangeStream(stream network.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(peerExchangeStreamTimeout))

	var req discovery.PeerExchangeMessage
	if err := json.NewDecoder(stream).Decode(&req); err != nil {
		_ = stream.Reset()
		return
	}
	if req.Sender != nil && req.Sender.PeerID != a.PeerID() {
		if req.ExpiresAt <= 0 || time.Now().Unix() <= req.ExpiresAt {
			if ok, err := discovery.VerifyPeerExchange(&req); err == nil && ok {
				a.applyPeerExchange(&req)
			}
		}
	}

	resp, err := a.buildPeerExchangeMessage(true)
	if err != nil || resp == nil {
		return
	}
	if err := json.NewEncoder(stream).Encode(resp); err != nil {
		_ = stream.Reset()
		return
	}
}

func (a *App) applyPeerExchange(msg *discovery.PeerExchangeMessage) {
	if msg == nil || a.discovery == nil {
		return
	}
	for _, entry := range msg.Entries {
		desc := entry.Descriptor
		if desc == nil || !desc.Relay {
			continue
		}
		ok, err := discovery.VerifyDescriptor(desc)
		if err != nil || !ok {
			continue
		}
		if desc.PeerID == a.PeerID() {
			continue
		}
		if _, ok := a.discovery.Store().Get(desc.PeerID); ok {
			continue
		}

		addrs := filterAddrStrings(entry.ObservedAddrs)
		if len(addrs) == 0 {
			addrs = filterAddrStrings(desc.ListenAddrs)
		}
		if len(addrs) == 0 {
			continue
		}
		a.tryConnectPeerExchangeRelay(desc.PeerID, addrs)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *App) tryConnectPeerExchangeRelay(peerID string, addrs []string) {
	if a.host == nil || peerID == "" || len(addrs) == 0 {
		return
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return
	}
	if a.Host().Network().Connectedness(pid) == network.Connected {
		return
	}
	maddrs := make([]multiaddr.Multiaddr, 0, len(addrs))
	for _, addr := range addrs {
		maddr, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			continue
		}
		maddrs = append(maddrs, maddr)
	}
	if len(maddrs) == 0 {
		return
	}
	a.Host().Peerstore().AddAddrs(pid, maddrs, peerstore.TempAddrTTL)
	ctx, cancel := context.WithTimeout(a.ctx, peerExchangeDialTimeout)
	defer cancel()
	if err := a.Host().Connect(ctx, peer.AddrInfo{ID: pid, Addrs: maddrs}); err != nil {
		log.Printf("[peerx] connect relay %s failed: %v", peerID, err)
	}
}

func (a *App) relayAddrs(desc *discovery.NodeDescriptor) []string {
	if desc == nil {
		return nil
	}
	if a.host != nil {
		if pid, err := peer.Decode(desc.PeerID); err == nil {
			if addrs := filterMultiaddrStrings(a.Host().Peerstore().Addrs(pid)); len(addrs) > 0 {
				return addrs
			}
		}
	}
	return filterAddrStrings(desc.ListenAddrs)
}

func filterMultiaddrStrings(maddrs []multiaddr.Multiaddr) []string {
	if len(maddrs) == 0 {
		return nil
	}
	strs := make([]string, 0, len(maddrs))
	for _, m := range maddrs {
		strs = append(strs, m.String())
	}
	return filterAddrStrings(strs)
}

func filterAddrStrings(addrs []string) []string {
	if len(addrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(addrs))
	seen := make(map[string]struct{}, len(addrs))
	for _, addr := range addrs {
		if addr == "" {
			continue
		}
		maddr, err := multiaddr.NewMultiaddr(addr)
		if err != nil || !hasRealIP(maddr) {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out
}

func hasRealIP(m multiaddr.Multiaddr) bool {
	if m == nil {
		return false
	}
	if v, err := m.ValueForProtocol(multiaddr.P_IP4); err == nil {
		if v != "" && v != "0.0.0.0" {
			return true
		}
	}
	if v, err := m.ValueForProtocol(multiaddr.P_IP6); err == nil {
		if v != "" && v != "::" {
			return true
		}
	}
	return false
}
