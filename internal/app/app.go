package app

import (
	"context"
	"fmt"
	"log"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	peer "github.com/libp2p/go-libp2p/core/peer"

	"meshproxy/internal/api"
	"meshproxy/internal/client"
	"meshproxy/internal/config"
	"meshproxy/internal/discovery"
	"meshproxy/internal/exit"
	"meshproxy/internal/identity"
	"meshproxy/internal/relay"
	"meshproxy/internal/protocol"
	"meshproxy/internal/p2p"
	"meshproxy/internal/store"
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

	circuitStore    *store.CircuitStore
	pathSelector    *client.PathSelector
	circuitManager  *client.CircuitManager
	streamMgr       *client.StreamManager
	recentErrors   *api.RecentErrorsStore
}

// New creates and initializes a new App instance.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	a := &App{
		ctx:       ctx,
		cfg:       cfg,
		startTime: time.Now(),
		store:     store.NewMemoryStore(),
		circuitStore: store.NewCircuitStore(),
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
	discoveryStore := discovery.NewStore()
	discoveryMgr := discovery.NewManager(ctx, h.Host, gossipComp, discoveryStore, selfDesc)
	discoveryMgr.Start()
	a.discovery = discoveryMgr

	// Bootstrap
	p2p.Bootstrap(ctx, h.Host, cfg.P2P.BootstrapPeers)

	// Local SOCKS5 and observability
	streamMgr := client.NewStreamManager()
	a.streamMgr = streamMgr
	a.recentErrors = api.NewRecentErrorsStore(100)

	// Path selector and circuit manager
	localPeerID := a.PeerID()
	selector := client.NewPathSelector(discoveryStore, localPeerID, client.DefaultRoutePolicy())
	a.pathSelector = selector
	cm := client.NewCircuitManager(ctx, a.Host(), a.circuitStore, selector, streamMgr)
	a.circuitManager = cm
	cm.SetBuildRetries(cfg.Client.BuildRetries)
	cm.SetBeginTCPRetries(cfg.Client.BeginTCPRetries)

	// Circuit pool: pre-build circuits, replenish on failure, idle timeout
	pool := client.NewCircuitPool(cfg.CircuitPool, cm)
	cm.SetPool(pool)
	pool.StartMaintenance(ctx)

	// Relay service when this node relays (relay or relay+exit).
	if cfg.Mode == config.ModeRelay || cfg.Mode == config.ModeRelayExit {
		_ = relay.NewService(a.Host())
	}
	// Exit service when this node is relay+exit.
	if cfg.Mode == config.ModeRelayExit {
		_ = exit.NewService(a.Host())
	}

	socks := client.NewSocks5Server(cfg.Socks5.Listen, cm, selector, streamMgr, &errorRecorderAdapter{store: a.recentErrors})
	if err := socks.Start(); err != nil {
		return nil, fmt.Errorf("start socks5: %w", err)
	}
	a.socks5 = socks

	// Local API (full observability: status, nodes, relays, exits, circuits, streams, pool, scores, errors, metrics)
	opts := &api.LocalAPIOpts{
		Relays:  discoveryStore,
		Exits:   discoveryStore,
		Streams: &streamsAPIAdapter{sm: streamMgr, circuits: a.circuitStore},
		Pool:    &poolStatusAPIAdapter{cm: cm},
		Scores:  &scoresPlaceholder{},
		Errors:  a.recentErrors,
		Metrics: &metricsSummaryAdapter{app: a},
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
	circuits := a.circuitStore.GetAll()
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
		"mode":               a.Mode(),
		"peer_id":            a.PeerID(),
		"uptime_seconds":     int64(time.Since(a.StartTime()).Seconds()),
		"circuits_total":     len(circuits),
		"streams_active":     len(streams),
		"relays_known":       len(relays),
		"exits_known":        len(exits),
		"errors_recent_count": errorsCount,
		"pool_status":       poolStatus,
	}
}

