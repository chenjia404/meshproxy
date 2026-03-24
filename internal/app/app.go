package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	host "github.com/libp2p/go-libp2p/core/host"
	network "github.com/libp2p/go-libp2p/core/network"
	peer "github.com/libp2p/go-libp2p/core/peer"
	peerstore "github.com/libp2p/go-libp2p/core/peerstore"
	corerouting "github.com/libp2p/go-libp2p/core/routing"
	multiaddr "github.com/multiformats/go-multiaddr"

	"github.com/chenjia404/meshproxy/internal/api"
	"github.com/chenjia404/meshproxy/internal/chat"
	"github.com/chenjia404/meshproxy/internal/chatrelay"
	"github.com/chenjia404/meshproxy/internal/client"
	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/discovery"
	"github.com/chenjia404/meshproxy/internal/exit"
	"github.com/chenjia404/meshproxy/internal/geoip"
	"github.com/chenjia404/meshproxy/internal/identity"
	"github.com/chenjia404/meshproxy/internal/ipfsnode"
	"github.com/chenjia404/meshproxy/internal/meshserver"
	sessionv1 "github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"
	"github.com/chenjia404/meshproxy/internal/p2p"
	"github.com/chenjia404/meshproxy/internal/protocol"
	"github.com/chenjia404/meshproxy/internal/relay"
	"github.com/chenjia404/meshproxy/internal/relaycache"
	"github.com/chenjia404/meshproxy/internal/safe"
	"github.com/chenjia404/meshproxy/internal/store"
	"github.com/chenjia404/meshproxy/internal/traffic"
	"github.com/chenjia404/meshproxy/internal/tunnel"
	"github.com/chenjia404/meshproxy/internal/update"
)

// App is the main meshproxy application wiring together all components.
type App struct {
	ctx       context.Context
	cfg       config.Config
	startTime time.Time

	idMgr *identity.Manager
	store *store.MemoryStore

	host   *p2p.Host
	gossip *p2p.Gossip

	discovery *discovery.Manager

	socks5     *client.Socks5Server
	exitSocks5 *exit.Socks5Server
	localAPI   *api.LocalAPI
	ipfsEmb    *ipfsnode.EmbeddedIPFS
	chat              *chat.Service
	chatRelayV1Table  *relay.ChatRelayV1Table // 《聊天中继.md》V1 中繼轉發表（僅 relay 模式寫入）
	meshServer        *meshserver.Manager

	circuitStore         *store.CircuitStore
	pathSelector         *client.PathSelector
	circuitManager       *client.CircuitManager
	streamMgr            *client.StreamManager
	recentErrors         *api.RecentErrorsStore
	relayCache           *relaycache.Cache
	bootstrapCache       *relaycache.Cache
	selfDesc             *discovery.NodeDescriptor
	selfPeerExchangeDesc *discovery.NodeDescriptor // same as selfDesc plus signed started_at_unix; used for /peer-exchange/1.0.1 only
	updater              *update.Service
	autoUpdateMu         sync.RWMutex
	autoUpdate           bool
	traffic              *traffic.Recorder

	peerExchangeOnce sync.Map
	peerExchangeDial sync.Map

	closeOnce sync.Once
	closeErr  error
	ownsHost  bool

	myServersStore *meshserver.MyServersStore

	connectedServersStore *meshserver.ConnectedMeshServersStore

	myGroupsStore *meshserver.MyGroupsStore

	resourceIDsStore *meshserver.ResourceIDsStore
}

type Options struct {
	EnableSOCKS5   bool
	EnableLocalAPI bool
	Host           host.Host
	Routing        corerouting.Routing
	CloseHost      bool
}

const (
	peerExchangeInterval       = time.Minute
	peerExchangeTTL            = 2 * time.Minute
	peerExchangeMaxEntries     = 16
	peerExchangeMaxAddrs       = 4
	peerExchangeDialTimeout    = 8 * time.Second
	peerExchangeStreamTimeout  = 10 * time.Second
	startupCacheConnectTimeout = 8 * time.Second
	bootstrapCacheMaxEntries   = 200
)

// New creates and initializes a new App instance.
func New(ctx context.Context, cfg config.Config) (*App, error) {
	return NewWithOptions(ctx, cfg, Options{
		EnableSOCKS5:   true,
		EnableLocalAPI: true,
	})
}

func NewWithOptions(ctx context.Context, cfg config.Config, opts Options) (*App, error) {
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	myServersPath := filepath.Join(cfg.DataDir, "my_meshserver_servers.db")
	myServersStore := meshserver.NewMyServersStore(myServersPath)
	myGroupsPath := filepath.Join(cfg.DataDir, "my_meshserver_groups.db")
	myGroupsStore := meshserver.NewMyGroupsStore(myGroupsPath)
	connectedServersPath := filepath.Join(cfg.DataDir, "connected_meshservers.db")
	connectedServersStore := meshserver.NewConnectedMeshServersStore(connectedServersPath)
	resourceIDsPath := filepath.Join(cfg.DataDir, "meshserver_resource_ids.db")
	resourceIDsStore := meshserver.NewResourceIDsStore(resourceIDsPath)
	cachePath := filepath.Join(cfg.DataDir, "relays.json")
	relayCache, err := relaycache.New(cachePath)
	if err != nil {
		return nil, fmt.Errorf("init relay cache: %w", err)
	}
	bootstrapPath := filepath.Join(cfg.DataDir, "bootstrap.json")
	bootstrapCache, err := relaycache.New(bootstrapPath)
	if err != nil {
		return nil, fmt.Errorf("init bootstrap cache: %w", err)
	}

	a := &App{
		ctx:                   ctx,
		cfg:                   cfg,
		startTime:             time.Now(),
		store:                 store.NewMemoryStore(),
		circuitStore:          store.NewCircuitStore(),
		relayCache:            relayCache,
		bootstrapCache:        bootstrapCache,
		myServersStore:        myServersStore,
		myGroupsStore:         myGroupsStore,
		connectedServersStore: connectedServersStore,
		resourceIDsStore:      resourceIDsStore,
		updater:               update.NewService("chenjia404", "meshproxy", "meshproxy"),
		autoUpdate:            cfg.AutoUpdate,
		traffic:               traffic.NewRecorder(),
	}

	// Identity
	idMgr, err := identity.NewManager(cfg.IdentityKeyPath)
	if err != nil {
		return nil, fmt.Errorf("init identity: %w", err)
	}
	a.idMgr = idMgr

	// P2P host
	var h *p2p.Host
	if opts.Host != nil {
		expectedPeerID, err := peer.IDFromPrivateKey(idMgr.PrivateKey())
		if err != nil {
			return nil, fmt.Errorf("derive identity peer id: %w", err)
		}
		if opts.Host.ID() != expectedPeerID {
			return nil, fmt.Errorf("injected host peer id mismatch: host=%s identity=%s", opts.Host.ID(), expectedPeerID)
		}
		h = &p2p.Host{
			Host:        opts.Host,
			Routing:     opts.Routing,
			ListenAddrs: opts.Host.Addrs(),
		}
		a.ownsHost = opts.CloseHost
	} else {
		h, err = p2p.NewHost(ctx, idMgr.PrivateKey(), cfg.P2P.ListenAddrs)
		if err != nil {
			return nil, fmt.Errorf("init p2p host: %w", err)
		}
		a.ownsHost = true
	}
	a.host = h

	// Embedded IPFS（可選）：復用同一 libp2p host + DHT routing。
	if cfg.IPFS.Enabled {
		ipfsRoot := filepath.Join(cfg.DataDir, cfg.IPFS.DataDir)
		n, err := ipfsnode.NewEmbeddedIPFS(ctx, h.Host, h.Routing, ipfsRoot, cfg.IPFS)
		if err != nil {
			return nil, fmt.Errorf("init embedded ipfs: %w", err)
		}
		a.ipfsEmb = n
	}

	// DHT-based rendezvous discovery can be disabled for fixed-topology deployments.
	if !cfg.P2P.NoDiscovery {
		p2p.StartDiscovery(ctx, h.Host, h.Routing, cfg.P2P.DiscoveryTag, func(info peer.AddrInfo) {
			if a.relayCache == nil || info.ID == "" {
				return
			}
			if len(info.Addrs) > 0 {
				a.Host().Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)
			}
			addrs := filterMultiaddrStrings(info.Addrs)
			if len(addrs) == 0 {
				return
			}
			if err := a.relayCache.Add(info.ID.String(), addrs); err != nil {
				log.Printf("[relaycache] persist discovered peer %s: %v", info.ID.String(), err)
			}
			if a.chat != nil {
				a.chat.OnPeerDiscovered(info.ID.String())
			}
		})
	}

	// Gossip
	gossipComp, err := p2p.NewGossip(ctx, h.Host)
	if err != nil {
		return nil, fmt.Errorf("init gossip: %w", err)
	}
	a.gossip = gossipComp

	// Discovery manager
	selfVersion := update.Version
	if selfVersion == "" {
		selfVersion = "devel"
	}
	selfDesc, err := discovery.BuildSelfDescriptor(selfVersion, idMgr.PrivateKey(), a.P2PListenAddrs(), true, cfg.Mode == config.ModeRelayExit, time.Minute*2)
	if err != nil {
		return nil, fmt.Errorf("build self descriptor: %w", err)
	}
	if err := discovery.SignDescriptor(idMgr.PrivateKey(), selfDesc); err != nil {
		return nil, fmt.Errorf("sign self descriptor: %w", err)
	}
	a.selfDesc = selfDesc
	// Second descriptor: identical fields + started_at_unix, for peer-exchange/1.0.1 (gossip and 1.0.0 use selfDesc without this field).
	pxDesc := *selfDesc
	pxDesc.StartedAtUnix = a.startTime.Unix()
	pxDesc.Signature = ""
	if err := discovery.SignDescriptor(idMgr.PrivateKey(), &pxDesc); err != nil {
		return nil, fmt.Errorf("sign peer-exchange self descriptor: %w", err)
	}
	a.selfPeerExchangeDesc = &pxDesc
	discoveryStore := discovery.NewStore()
	discoveryMgr := discovery.NewManager(ctx, h.Host, gossipComp, discoveryStore, selfDesc)
	discoveryMgr.Start()
	a.discovery = discoveryMgr
	a.installDirectPeerExchange()
	safe.Go("app.runPeerExchange", func() { a.runPeerExchange(ctx) })

	// Learn exit peer addrs (DHT FindPeer + Connect) so GeoIP gets real IP soon after discovery.
	safe.Go("app.runPeerAddrLearner", func() { runPeerAddrLearner(ctx, h.Host, h.Routing, discoveryStore, 25*time.Second) })

	// Bootstrap
	p2p.Bootstrap(ctx, h.Host, cfg.P2P.BootstrapPeers)

	// Local SOCKS5 and observability
	streamMgr := client.NewStreamManager()
	a.streamMgr = streamMgr
	a.recentErrors = api.NewRecentErrorsStore(100)

	chatSvc, err := chat.NewService(ctx, filepath.Join(cfg.DataDir, "chat.db"), filepath.Join(cfg.DataDir, chat.DefaultAvatarStorageDir), a.Host(), h.Routing, discoveryStore, cfg.Chat.OfflineStoreNodes)
	if err != nil {
		return nil, fmt.Errorf("init chat service: %w", err)
	}
	a.chat = chatSvc
	a.chatRelayV1Table = relay.NewChatRelayV1Table()
	chatSvc.SetNodePrivateKey(idMgr.PrivateKey())
	if a.ipfsEmb != nil {
		chatSvc.SetIPFSAvatarPinner(newChatIPFSAvatarPinner(a.ipfsEmb))
	}
	connections := make([]meshserver.ConnectionConfig, 0, len(cfg.MeshServerConnections()))
	for _, conn := range cfg.MeshServerConnections() {
		connections = append(connections, meshserver.ConnectionConfig{
			Name:        conn.Name,
			PeerID:      conn.PeerID,
			ClientAgent: conn.ClientAgent,
			ProtocolID:  conn.ProtocolID,
		})
	}
	meshMgr, err := meshserver.NewManager(ctx, a.Host(), h.Routing, idMgr.PrivateKey(), connections)
	if err != nil {
		return nil, fmt.Errorf("init meshserver manager: %w", err)
	}
	a.meshServer = meshMgr
	// Persist all configured/initialized meshserver connections for the local "/servers" list.
	if a.connectedServersStore != nil {
		for _, info := range a.meshServer.List() {
			_ = a.connectedServersStore.Put(info)
		}
	}
	a.restoreCachedPeerConnections()

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
	cm := client.NewCircuitManager(ctx, a.Host(), a.circuitStore, selector, streamMgr, a.traffic)
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
	var relaySvc *relay.Service
	if cfg.Mode == config.ModeRelay || cfg.Mode == config.ModeRelayExit {
		relaySvc = relay.NewService(a.Host(), a.traffic)
	}
	// Exit service when this node is relay+exit（帶出口策略檢查）。始終創建 Policy 以便出口政策 API 可註冊。
	var exitSvc *exit.Service
	if cfg.Mode == config.ModeRelayExit {
		policy := exit.NewPolicyChecker(cfg.Exit) // cfg.Exit 為 nil 時使用預設策略
		exitSvc = exit.NewService(a.Host(), policy, cfg.Socks5.ExitUpstream, a.traffic)
		exitSocks := exit.NewSocks5Server(cfg.Socks5.ExitUpstream, policy)
		if err := exitSocks.Start(); err != nil {
			return nil, fmt.Errorf("start exit socks5: %w", err)
		}
		a.exitSocks5 = exitSocks
	}
	if exitSvc != nil && relaySvc != nil {
		a.Host().SetStreamHandler(p2p.ProtocolRawTunnelE2E, func(str network.Stream) {
			var header tunnel.RouteHeader
			if err := tunnel.ReadJSONFrame(str, &header); err != nil {
				_ = str.Close()
				return
			}
			if len(header.Path) == 0 || header.HopIndex < 0 || header.HopIndex >= len(header.Path) {
				_ = str.Close()
				return
			}
			if header.HopIndex < len(header.Path)-1 {
				relaySvc.HandleRawTunnelStreamWithHeader(str, header)
				return
			}
			exitSvc.HandleRawTunnelE2EStreamWithHeader(str, header)
		})
	} else if exitSvc != nil {
		a.Host().SetStreamHandler(p2p.ProtocolRawTunnelE2E, exitSvc.HandleRawTunnelE2EStream)
	} else if relaySvc != nil {
		a.Host().SetStreamHandler(p2p.ProtocolRawTunnelE2E, relaySvc.HandleRawTunnelStream)
	}
	if chatSvc != nil && relaySvc != nil {
		a.Host().SetStreamHandler(p2p.ProtocolChatRelayE2E, func(str network.Stream) {
			var header tunnel.RouteHeader
			if err := tunnel.ReadJSONFrame(str, &header); err != nil {
				_ = str.Close()
				return
			}
			if len(header.Path) == 0 || header.HopIndex < 0 || header.HopIndex >= len(header.Path) {
				_ = str.Close()
				return
			}
			if header.HopIndex < len(header.Path)-1 {
				relaySvc.HandleChatRelayStreamWithHeader(str, header)
				return
			}
			chatSvc.HandleRelayE2EStreamWithHeader(str, header)
		})
	}

	// ChatRelay V1（《聊天中继.md》）：connect / handshake / data / heartbeat
	if chatSvc != nil {
		a.Host().SetStreamHandler(chatrelay.ProtocolRelayConnect, func(str network.Stream) {
			a.dispatchRelayV1Connect(str)
		})
		a.Host().SetStreamHandler(chatrelay.ProtocolRelayHandshake, func(str network.Stream) {
			a.dispatchRelayV1Handshake(str)
		})
		a.Host().SetStreamHandler(chatrelay.ProtocolRelayData, func(str network.Stream) {
			a.dispatchRelayV1Data(str)
		})
		a.Host().SetStreamHandler(chatrelay.ProtocolRelayHeartbeat, func(str network.Stream) {
			a.dispatchRelayV1Heartbeat(str)
		})
	}

	if opts.EnableSOCKS5 {
		socks := client.NewSocks5Server(cfg.Socks5.Listen, a.Host(), cm, selector, streamMgr, &errorRecorderAdapter{store: a.recentErrors}, a.traffic)
		socks.SetAllowUDPAssociate(cfg.Socks5.AllowUDPAssociate)
		socks.SetTunnelToExit(cfg.Socks5.TunnelToExit, cfg.Socks5.ExitUpstream)
		if err := socks.Start(); err != nil {
			return nil, fmt.Errorf("start socks5: %w", err)
		}
		a.socks5 = socks
	}

	if opts.EnableLocalAPI {
		// Local API (full observability: status, nodes, relays, exits, circuits, streams, pool, scores, errors, metrics)
		apiOpts := &api.LocalAPIOpts{
			Relays:              discoveryStore,
			Exits:               discoveryStore,
			Streams:             &streamsAPIAdapter{sm: streamMgr, circuits: a.circuitStore},
			Pool:                &poolStatusAPIAdapter{cm: cm},
			CircuitPoolConfig:   &poolConfigAPIAdapter{cm: cm},
			Scores:              &scoresPlaceholder{},
			Errors:              a.recentErrors,
			Metrics:             &metricsSummaryAdapter{app: a},
			ExitSelection:       selector,
			Socks5Tunnel:        &socks5TunnelAPIAdapter{app: a},
			ExitCandidates:      selector,
			ExitCountryResolver: selector,
			ConfigPath:          cfg.ConfigFilePath,
			ExitService:         exitSvc,
			ChatService:         chatSvc,
			MeshServer:          &meshServerAPIAdapter{app: a},
			UpdateService:       a,
			UpdateSettings:      a,
			IPFS:                a.ipfsEmb,
		}
		localAPI := api.NewLocalAPI(cfg.API.Listen, a, a.discovery, a, apiOpts)
		localAPI.Start()
		a.localAPI = localAPI
	}

	safe.Go("app.autoUpdateLoop", func() { a.runAutoUpdateLoop(ctx) })

	return a, nil
}

// Run blocks until the context is cancelled and then shuts down components.
func (a *App) Run() error {
	<-a.ctx.Done()
	log.Printf("shutting down meshproxy...")
	return a.Close()
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		var firstErr error
		if a.localAPI != nil {
			// firstErr is guaranteed to be nil at this point (no earlier assignments in this closure),
			// so the additional `firstErr == nil` check is redundant.
			if err := a.localAPI.Shutdown(); err != nil {
				firstErr = err
			}
		}
		if a.ipfsEmb != nil {
			if err := a.ipfsEmb.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if a.socks5 != nil {
			if err := a.socks5.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if a.chat != nil {
			if err := a.chat.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if a.meshServer != nil {
			if err := a.meshServer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if a.exitSocks5 != nil {
			if err := a.exitSocks5.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if a.discovery != nil {
			a.discovery.Stop()
		}
		if a.host != nil && a.ownsHost {
			if err := a.host.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		a.closeErr = firstErr
	})
	return a.closeErr
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

func (a *App) TrafficStats() api.TrafficStatsResponse {
	if a == nil || a.traffic == nil {
		return api.TrafficStatsResponse{}
	}
	stats := a.traffic.Snapshot()
	return api.TrafficStatsResponse{
		BytesSent:     stats.BytesSent,
		BytesReceived: stats.BytesReceived,
		BytesTotal:    stats.BytesTotal,
	}
}

func (a *App) CheckForUpdate(ctx context.Context) (update.Info, error) {
	if a == nil || a.updater == nil {
		return update.Info{}, fmt.Errorf("updater not available")
	}
	return a.updater.Check(ctx)
}

func (a *App) ApplyUpdate(ctx context.Context) (update.ApplyResult, error) {
	if a == nil || a.updater == nil {
		return update.ApplyResult{}, fmt.Errorf("updater not available")
	}
	return a.updater.Apply(ctx)
}

func (a *App) GetAutoUpdate() bool {
	if a == nil {
		return false
	}
	a.autoUpdateMu.RLock()
	defer a.autoUpdateMu.RUnlock()
	return a.autoUpdate
}

func (a *App) SetAutoUpdate(enabled bool) error {
	if a == nil {
		return fmt.Errorf("app is nil")
	}
	if a.cfg.ConfigFilePath != "" {
		if err := config.SaveAutoUpdateSettings(a.cfg.ConfigFilePath, enabled); err != nil {
			return err
		}
	}
	a.autoUpdateMu.Lock()
	a.autoUpdate = enabled
	a.autoUpdateMu.Unlock()
	if enabled {
		safe.Go("app.autoUpdateTriggeredCheck", func() {
			ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
			defer cancel()
			a.autoUpdateOnce(ctx)
		})
	}
	return nil
}

func (a *App) runAutoUpdateLoop(ctx context.Context) {
	startupDelay := 90 * time.Second
	period := 6 * time.Hour
	timer := time.NewTimer(startupDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			a.autoUpdateOnce(ctx)
			timer.Reset(period)
		}
	}
}

func (a *App) autoUpdateOnce(ctx context.Context) {
	if a == nil || a.updater == nil || !a.GetAutoUpdate() {
		return
	}
	if update.Version == "" || update.Version == "devel" || update.Version == "(devel)" {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	info, err := a.CheckForUpdate(checkCtx)
	if err != nil {
		log.Printf("[update] auto check failed: %v", err)
		return
	}
	if !info.UpdateAvailable || !info.AssetAvailable {
		return
	}
	applyCtx, applyCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer applyCancel()
	result, err := a.ApplyUpdate(applyCtx)
	if err != nil {
		log.Printf("[update] auto apply failed: %v", err)
		return
	}
	log.Printf("[update] auto update scheduled: status=%s restart=%v version=%s", result.Status, result.RestartScheduled, result.LatestVersion)
}

// Host returns the underlying libp2p host, mainly for internal usage.
func (a *App) Host() host.Host {
	if a.host == nil {
		return nil
	}
	return a.host.Host
}

func (a *App) ChatService() *chat.Service {
	if a == nil {
		return nil
	}
	return a.chat
}

func (a *App) MeshServerClient() *meshserver.Client {
	if a == nil {
		return nil
	}
	if a.meshServer == nil {
		return nil
	}
	client, _ := a.meshServer.Get("")
	return client
}

func (a *App) MeshServerEnabled() bool {
	return a != nil && a.meshServer != nil && a.meshServer.HasAny()
}

func (a *App) MeshServerConnections() []meshserver.ConnectionInfo {
	if a == nil || a.meshServer == nil {
		return nil
	}
	return a.meshServer.List()
}

// MeshServerListConnectedServers returns the persisted list of connected meshservers.
// It merges stored identity fields with live connection status when possible.
func (a *App) MeshServerListConnectedServers() []meshserver.ConnectionInfo {
	if a == nil || a.connectedServersStore == nil {
		return nil
	}

	entries := a.connectedServersStore.ListEntries()
	if len(entries) == 0 {
		return nil
	}

	live := make(map[string]meshserver.ConnectionInfo, len(a.MeshServerConnections()))
	for _, ci := range a.MeshServerConnections() {
		live[ci.Name] = ci
	}

	out := make([]meshserver.ConnectionInfo, 0, len(entries))
	for _, e := range entries {
		ci := meshserver.ConnectionInfo{
			Name:        e.Name,
			PeerID:      e.PeerID,
			ClientAgent: e.ClientAgent,
			ProtocolID:  e.ProtocolID,
		}
		if liveCI, ok := live[e.Name]; ok {
			ci.Connected = liveCI.Connected
			ci.Authenticated = liveCI.Authenticated
			ci.SessionID = liveCI.SessionID
			ci.UserID = liveCI.UserID
			ci.DisplayName = liveCI.DisplayName
		}
		out = append(out, ci)
	}
	return out
}

func (a *App) MeshServerGetOrCreateSpaceID(serverID string) (int, error) {
	if a == nil || a.resourceIDsStore == nil {
		return 0, nil
	}
	return a.resourceIDsStore.GetOrCreateSpaceID(serverID)
}

func (a *App) MeshServerGetServerIDBySpaceID(spaceID int) (string, error) {
	if a == nil || a.resourceIDsStore == nil {
		return "", nil
	}
	return a.resourceIDsStore.GetServerIDBySpaceID(spaceID)
}

func (a *App) MeshServerGetOrCreateChannelID(channelID string) (int, error) {
	if a == nil || a.resourceIDsStore == nil {
		return 0, nil
	}
	return a.resourceIDsStore.GetOrCreateChannelID(channelID)
}

func (a *App) MeshServerGetChannelIDByChannelIDIntID(channelIDIntID int) (string, error) {
	if a == nil || a.resourceIDsStore == nil {
		return "", nil
	}
	return a.resourceIDsStore.GetChannelIDByChannelIDIntID(channelIDIntID)
}

func (a *App) MeshServerConnect(ctx context.Context, name, peerID, clientAgent, protocolID string) (meshserver.ConnectionInfo, error) {
	if a == nil || a.meshServer == nil {
		return meshserver.ConnectionInfo{}, fmt.Errorf("meshserver client not available")
	}
	info, err := a.meshServer.Add(ctx, meshserver.ConnectionConfig{
		Name:        name,
		PeerID:      peerID,
		ClientAgent: clientAgent,
		ProtocolID:  protocolID,
	})
	if err != nil {
		return meshserver.ConnectionInfo{}, err
	}
	// Best-effort: persist joined business servers after successful auth.
	if a.myServersStore != nil {
		_, _ = a.MeshServerListMyServersForConnection(ctx, info.Name)
	}
	// Best-effort: persist connected meshservers.
	if a.connectedServersStore != nil {
		_ = a.connectedServersStore.Put(info)
	}
	return info, nil
}

func (a *App) MeshServerDisconnect(name string) error {
	if a == nil || a.meshServer == nil {
		return fmt.Errorf("meshserver client not available")
	}
	return a.meshServer.Remove(name)
}

func (a *App) meshServerClient(connection string) (*meshserver.Client, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	client, err := a.meshServer.Get(connection)
	if err != nil {
		return nil, fmt.Errorf("meshserver client not available: %w", err)
	}
	return client, nil
}

func parseUint32ID(s string) uint32 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(v)
}

func (a *App) MeshServerListServersFor(ctx context.Context, connection string) (*sessionv1.ListSpacesResp, error) {
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.ListServers(ctx)
}

func (a *App) MeshServerGetCreateSpacePermissionsForConnection(ctx context.Context, connection string) (*sessionv1.GetCreateSpacePermissionsResp, error) {
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.GetCreateSpacePermissions(ctx)
}

// MeshServerFetchHTTPAccessToken 以本機 libp2p 身份對指定 meshserver HTTP 基底 URL 取得 JWT（見 meshserver docs/api.md）。
func (a *App) MeshServerFetchHTTPAccessToken(ctx context.Context, baseURL, protocolID string) (*meshserver.HTTPAccessTokenResult, error) {
	if a == nil || a.idMgr == nil {
		return nil, fmt.Errorf("identity not available")
	}
	if a.host == nil {
		return nil, fmt.Errorf("host not available")
	}
	return meshserver.FetchHTTPAccessToken(ctx, baseURL, a.host.Host.ID().String(), a.idMgr.PrivateKey(), protocolID)
}

func (a *App) MeshServerCreateSpaceFor(ctx context.Context, connection, name, description string, visibility sessionv1.Visibility, allowChannelCreation bool) (*sessionv1.CreateSpaceResp, error) {
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.CreateSpace(ctx, name, description, visibility, allowChannelCreation)
}

func (a *App) MeshServerJoinServer(ctx context.Context, serverID string) (*sessionv1.JoinSpaceResp, error) {
	return a.MeshServerJoinServerForConnection(ctx, "", serverID)
}

func (a *App) MeshServerJoinServerForConnection(ctx context.Context, connection, serverID string) (*sessionv1.JoinSpaceResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	resp, err := client.JoinServer(ctx, serverID)
	if err == nil && a.myServersStore != nil {
		// Best-effort: update persisted joined servers.
		_, _ = a.MeshServerListMyServersForConnection(ctx, connection)
	}
	return resp, err
}

func (a *App) MeshServerInviteServerMember(ctx context.Context, serverID, targetUserID string) (*sessionv1.InviteSpaceMemberResp, error) {
	return a.MeshServerInviteServerMemberForConnection(ctx, "", serverID, targetUserID)
}

func (a *App) MeshServerInviteServerMemberForConnection(ctx context.Context, connection, serverID, targetUserID string) (*sessionv1.InviteSpaceMemberResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.InviteServerMember(ctx, serverID, targetUserID)
}

func (a *App) MeshServerKickServerMember(ctx context.Context, serverID, targetUserID string) (*sessionv1.KickSpaceMemberResp, error) {
	return a.MeshServerKickServerMemberForConnection(ctx, "", serverID, targetUserID)
}

func (a *App) MeshServerKickServerMemberForConnection(ctx context.Context, connection, serverID, targetUserID string) (*sessionv1.KickSpaceMemberResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.KickServerMember(ctx, serverID, targetUserID)
}

func (a *App) MeshServerBanServerMember(ctx context.Context, serverID, targetUserID string) (*sessionv1.BanSpaceMemberResp, error) {
	return a.MeshServerBanServerMemberForConnection(ctx, "", serverID, targetUserID)
}

func (a *App) MeshServerBanServerMemberForConnection(ctx context.Context, connection, serverID, targetUserID string) (*sessionv1.BanSpaceMemberResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.BanServerMember(ctx, serverID, targetUserID)
}

func (a *App) MeshServerListServerMembers(ctx context.Context, serverID string, afterMemberID uint64, limit uint32) (*sessionv1.ListSpaceMembersResp, error) {
	return a.MeshServerListServerMembersForConnection(ctx, "", serverID, afterMemberID, limit)
}

func (a *App) MeshServerListServerMembersForConnection(ctx context.Context, connection, serverID string, afterMemberID uint64, limit uint32) (*sessionv1.ListSpaceMembersResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.ListServerMembers(ctx, serverID, afterMemberID, limit)
}

func (a *App) MeshServerListChannelsFor(ctx context.Context, connection, serverID string) (*sessionv1.ListChannelsResp, error) {
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.ListChannels(ctx, parseUint32ID(serverID))
}

func (a *App) MeshServerCreateGroupFor(ctx context.Context, connection, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateGroupResp, error) {
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.CreateGroup(ctx, serverID, name, description, visibility, slowModeSeconds)
}

func (a *App) MeshServerCreateChannelFor(ctx context.Context, connection, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateChannelResp, error) {
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.CreateChannel(ctx, serverID, name, description, visibility, slowModeSeconds)
}

func (a *App) MeshServerSubscribeChannelFor(ctx context.Context, connection, channelID string, lastSeenSeq uint64) (*sessionv1.SubscribeChannelResp, error) {
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.SubscribeChannel(ctx, parseUint32ID(channelID), lastSeenSeq)
}

func (a *App) MeshServerUnsubscribeChannelFor(ctx context.Context, connection, channelID string) (*sessionv1.UnsubscribeChannelResp, error) {
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.UnsubscribeChannel(ctx, parseUint32ID(channelID))
}

func (a *App) MeshServerSendTextFor(ctx context.Context, connection, channelID, clientMsgID, text string) (*sessionv1.SendMessageAck, error) {
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.SendMessage(ctx, channelID, clientMsgID, sessionv1.MessageType_TEXT, text, nil, nil)
}

func (a *App) MeshServerSyncChannelFor(ctx context.Context, connection, channelID string, afterSeq uint64, limit uint32) (*sessionv1.SyncChannelResp, error) {
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.SyncChannel(ctx, channelID, afterSeq, limit)
}

func (a *App) MeshServerListServers(ctx context.Context) (*sessionv1.ListSpacesResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return a.MeshServerListServersFor(ctx, "")
}

func (a *App) MeshServerListServersForConnection(ctx context.Context, connection string) (*sessionv1.ListSpacesResp, error) {
	return a.MeshServerListServersFor(ctx, connection)
}

// MeshServerGetMyPermissionsForConnection returns a derived "GET_MY_PERMISSIONS" result.
// We compute it from:
// - auth result (current user_id)
// - server summary (allow_channel_creation)
// - member list (member role)
func (a *App) MeshServerGetMyPermissionsForConnection(ctx context.Context, connection, serverID string) (*meshserver.MyPermissions, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	auth := client.AuthResult()
	if auth == nil {
		return nil, fmt.Errorf("meshserver client not authenticated")
	}

	// Server-level settings (e.g. allow_channel_creation).
	serversResp, err := client.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	spaceID := parseUint32ID(serverID)
	var allowChannelCreation bool
	serverFound := false
	for _, s := range serversResp.Spaces {
		if s != nil && s.SpaceId == spaceID {
			allowChannelCreation = s.AllowChannelCreation
			serverFound = true
			break
		}
	}
	if !serverFound {
		return nil, fmt.Errorf("server not found: %s", serverID)
	}

	// Find my member role by paginating server members.
	myRole := sessionv1.MemberRole_MEMBER_ROLE_UNSPECIFIED
	found := false
	var after uint64
	limit := uint32(50)
	for {
		membersResp, err := client.ListServerMembers(ctx, serverID, after, limit)
		if err != nil {
			return nil, err
		}
		for _, m := range membersResp.Members {
			if m != nil && m.UserId == auth.UserId {
				myRole = m.Role
				found = true
				break
			}
		}
		if found || !membersResp.HasMore {
			break
		}
		after = membersResp.NextAfterMemberId
	}

	isAdmin := myRole == sessionv1.MemberRole_OWNER || myRole == sessionv1.MemberRole_ADMIN
	return &meshserver.MyPermissions{
		ServerID: serverID,
		Role:     myRole,

		CanCreateChannel:      allowChannelCreation && isAdmin,
		CanManageMembers:      isAdmin,
		CanSetMemberRoles:     isAdmin,
		CanSetChannelCreation: isAdmin,
	}, nil
}

// MeshServerListMyServersForConnection returns the servers where the current authenticated user
// is already a member (i.e. has an entry in LIST_SERVER_MEMBERS).
func (a *App) MeshServerListMyServersForConnection(ctx context.Context, connection string) (*meshserver.MyServersResp, error) {
	if a == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}

	// Cache key is the meshserver connection name / peer id (default name == peer id).
	cacheKey := strings.TrimSpace(connection)
	if cacheKey == "" && a.meshServer != nil {
		cacheKey = a.meshServer.DefaultName()
	}

	// Try live calculation first.
	if a.meshServer != nil {
		client, err := a.meshServerClient(connection)
		if err == nil && client != nil {
			auth := client.AuthResult()
			if auth == nil {
				return nil, fmt.Errorf("meshserver client not authenticated")
			}

			serversResp, err := client.ListServers(ctx)
			if err != nil {
				// Fall through to cache.
			} else {
				if serversResp == nil || len(serversResp.Spaces) == 0 {
					resp := &meshserver.MyServersResp{Servers: []*meshserver.MyServer{}}
					if a.myServersStore != nil {
						_ = a.myServersStore.Put(cacheKey, resp)
					}
					return resp, nil
				}

				out := make([]*meshserver.MyServer, 0, len(serversResp.Spaces))
				for _, s := range serversResp.Spaces {
					if s == nil || s.SpaceId == 0 {
						continue
					}

					var myRole sessionv1.MemberRole = sessionv1.MemberRole_MEMBER_ROLE_UNSPECIFIED
					found := false
					after := uint64(0)
					limit := uint32(50)
					for {
						membersResp, err := client.ListServerMembers(ctx, strconv.FormatUint(uint64(s.SpaceId), 10), after, limit)
						if err != nil {
							// Fall through to cache.
							goto cache_fallback
						}
						for _, m := range membersResp.Members {
							if m != nil && m.UserId == auth.UserId {
								myRole = m.Role
								found = true
								break
							}
						}
						if found || !membersResp.HasMore {
							break
						}
						after = membersResp.NextAfterMemberId
					}

					if found {
						out = append(out, &meshserver.MyServer{Server: s, Role: myRole})
					}
				}

				resp := &meshserver.MyServersResp{Servers: out}
				if a.myServersStore != nil {
					_ = a.myServersStore.Put(cacheKey, resp)
				}
				return resp, nil
			}
		}
	}

	// Cache fallback.
	if a.myServersStore != nil && cacheKey != "" {
		if resp, ok := a.myServersStore.Get(cacheKey); ok {
			return resp, nil
		}
	}
	return nil, fmt.Errorf("meshserver client not available")

cache_fallback:
	if a.myServersStore != nil && cacheKey != "" {
		if resp, ok := a.myServersStore.Get(cacheKey); ok {
			return resp, nil
		}
	}
	return nil, fmt.Errorf("meshserver client not available")
}

// MeshServerListMyGroupsForConnection returns the groups (GROUP-type channels)
// that the current authenticated user can view in the given Space.
//
// The response is cached locally as a best-effort fallback.
func (a *App) MeshServerListMyGroupsForConnection(ctx context.Context, connection, spaceID string) (*meshserver.MyGroupsResp, error) {
	if a == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	spaceID = strings.TrimSpace(spaceID)
	if spaceID == "" {
		return nil, fmt.Errorf("space_id is required")
	}

	cacheKey := strings.TrimSpace(connection)
	if cacheKey == "" && a.meshServer != nil {
		cacheKey = a.meshServer.DefaultName()
	}
	cacheKey = cacheKey + "|" + spaceID

	// Try live calculation first.
	if a.meshServer != nil {
		client, err := a.meshServerClient(connection)
		if err == nil && client != nil {
			channelsResp, err := client.ListChannels(ctx, parseUint32ID(spaceID))
			if err == nil {
				groups := make([]*meshserver.MyGroup, 0)
				for _, ch := range channelsResp.Channels {
					if ch == nil {
						continue
					}
					if ch.Type != sessionv1.ChannelType_GROUP {
						continue
					}
					// LIST_CHANNELS is user-permission aware; interpret can_view as membership/joined group.
					if !ch.CanView {
						continue
					}
					groups = append(groups, &meshserver.MyGroup{
						ID: func() int {
							// Prefer native numeric channel_id as stable id.
							return int(ch.ChannelId)
						}(),
						ChannelID:       strconv.FormatUint(uint64(ch.ChannelId), 10),
						Type:            ch.Type,
						Name:            ch.Name,
						Description:     ch.Description,
						Visibility:      ch.Visibility,
						SlowModeSeconds: ch.SlowModeSeconds,
						LastSeq:         ch.LastSeq,
						CanView:         ch.CanView,
						CanSendMessage:  ch.CanSendMessage,
						CanSendImage:    ch.CanSendImage,
						CanSendFile:     ch.CanSendFile,
					})
				}
				resp := &meshserver.MyGroupsResp{SpaceID: spaceID, Groups: groups}
				if a.myGroupsStore != nil {
					_ = a.myGroupsStore.Put(cacheKey, resp)
				}
				return resp, nil
			}
		}
	}

	// Cache fallback.
	if a.myGroupsStore != nil {
		if resp, ok := a.myGroupsStore.Get(cacheKey); ok {
			return resp, nil
		}
	}
	return nil, fmt.Errorf("meshserver client not available")
}

func (a *App) MeshServerListChannels(ctx context.Context, serverID string) (*sessionv1.ListChannelsResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return a.MeshServerListChannelsFor(ctx, "", serverID)
}

func (a *App) MeshServerListChannelsForConnection(ctx context.Context, connection, serverID string) (*sessionv1.ListChannelsResp, error) {
	return a.MeshServerListChannelsFor(ctx, connection, serverID)
}

func (a *App) MeshServerCreateGroup(ctx context.Context, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateGroupResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return a.MeshServerCreateGroupFor(ctx, "", serverID, name, description, visibility, slowModeSeconds)
}

func (a *App) MeshServerCreateGroupForConnection(ctx context.Context, connection, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateGroupResp, error) {
	return a.MeshServerCreateGroupFor(ctx, connection, serverID, name, description, visibility, slowModeSeconds)
}

func (a *App) MeshServerCreateChannel(ctx context.Context, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateChannelResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return a.MeshServerCreateChannelFor(ctx, "", serverID, name, description, visibility, slowModeSeconds)
}

func (a *App) MeshServerCreateChannelForConnection(ctx context.Context, connection, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateChannelResp, error) {
	return a.MeshServerCreateChannelFor(ctx, connection, serverID, name, description, visibility, slowModeSeconds)
}

func (a *App) MeshServerSubscribeChannel(ctx context.Context, channelID string, lastSeenSeq uint64) (*sessionv1.SubscribeChannelResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return a.MeshServerSubscribeChannelFor(ctx, "", channelID, lastSeenSeq)
}

func (a *App) MeshServerSubscribeChannelForConnection(ctx context.Context, connection, channelID string, lastSeenSeq uint64) (*sessionv1.SubscribeChannelResp, error) {
	return a.MeshServerSubscribeChannelFor(ctx, connection, channelID, lastSeenSeq)
}

func (a *App) MeshServerUnsubscribeChannel(ctx context.Context, channelID string) (*sessionv1.UnsubscribeChannelResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return a.MeshServerUnsubscribeChannelFor(ctx, "", channelID)
}

func (a *App) MeshServerUnsubscribeChannelForConnection(ctx context.Context, connection, channelID string) (*sessionv1.UnsubscribeChannelResp, error) {
	return a.MeshServerUnsubscribeChannelFor(ctx, connection, channelID)
}

func (a *App) MeshServerSendText(ctx context.Context, channelID, clientMsgID, text string) (*sessionv1.SendMessageAck, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return a.MeshServerSendTextFor(ctx, "", channelID, clientMsgID, text)
}

func (a *App) MeshServerSendTextForConnection(ctx context.Context, connection, channelID, clientMsgID, text string) (*sessionv1.SendMessageAck, error) {
	return a.MeshServerSendTextFor(ctx, connection, channelID, clientMsgID, text)
}

func (a *App) MeshServerSendMessage(ctx context.Context, channelID, clientMsgID string, messageType sessionv1.MessageType, text string, images []*sessionv1.MediaImage, files []*sessionv1.MediaFile) (*sessionv1.SendMessageAck, error) {
	return a.MeshServerSendMessageForConnection(ctx, "", channelID, clientMsgID, messageType, text, images, files)
}

func (a *App) MeshServerSendMessageForConnection(ctx context.Context, connection, channelID, clientMsgID string, messageType sessionv1.MessageType, text string, images []*sessionv1.MediaImage, files []*sessionv1.MediaFile) (*sessionv1.SendMessageAck, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	return client.SendMessage(ctx, channelID, clientMsgID, messageType, text, images, files)
}

func (a *App) MeshServerGetMediaForConnection(ctx context.Context, connection, mediaID string) (*sessionv1.MediaFile, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	client, err := a.meshServerClient(connection)
	if err != nil {
		return nil, err
	}
	resp, err := client.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.File == nil {
		return nil, fmt.Errorf("media not found")
	}
	return resp.File, nil
}

func (a *App) MeshServerSyncChannel(ctx context.Context, channelID string, afterSeq uint64, limit uint32) (*sessionv1.SyncChannelResp, error) {
	if a == nil || a.meshServer == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return a.MeshServerSyncChannelFor(ctx, "", channelID, afterSeq, limit)
}

func (a *App) MeshServerSyncChannelForConnection(ctx context.Context, connection, channelID string, afterSeq uint64, limit uint32) (*sessionv1.SyncChannelResp, error) {
	return a.MeshServerSyncChannelFor(ctx, connection, channelID, afterSeq, limit)
}

func (a *App) ListServers(ctx context.Context) (*sessionv1.ListSpacesResp, error) {
	return a.MeshServerListServers(ctx)
}

func (a *App) ListChannels(ctx context.Context, serverID string) (*sessionv1.ListChannelsResp, error) {
	return a.MeshServerListChannels(ctx, serverID)
}

func (a *App) CreateGroup(ctx context.Context, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateGroupResp, error) {
	return a.MeshServerCreateGroup(ctx, serverID, name, description, visibility, slowModeSeconds)
}

func (a *App) CreateChannel(ctx context.Context, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateChannelResp, error) {
	return a.MeshServerCreateChannel(ctx, serverID, name, description, visibility, slowModeSeconds)
}

func (a *App) SubscribeChannel(ctx context.Context, channelID string, lastSeenSeq uint64) (*sessionv1.SubscribeChannelResp, error) {
	return a.MeshServerSubscribeChannel(ctx, channelID, lastSeenSeq)
}

func (a *App) UnsubscribeChannel(ctx context.Context, channelID string) (*sessionv1.UnsubscribeChannelResp, error) {
	return a.MeshServerUnsubscribeChannel(ctx, channelID)
}

func (a *App) SendText(ctx context.Context, channelID, clientMsgID, text string) (*sessionv1.SendMessageAck, error) {
	return a.MeshServerSendText(ctx, channelID, clientMsgID, text)
}

func (a *App) SyncChannel(ctx context.Context, channelID string, afterSeq uint64, limit uint32) (*sessionv1.SyncChannelResp, error) {
	return a.MeshServerSyncChannel(ctx, channelID, afterSeq, limit)
}

func (a *App) LocalAPIListen() string {
	if a == nil || a.localAPI == nil {
		return ""
	}
	return a.cfg.API.Listen
}

// ListCircuits implements api.CircuitProvider.
func (a *App) ListCircuits() []protocol.CircuitInfo {
	if a.circuitStore == nil {
		return nil
	}
	return a.circuitStore.GetAll()
}

// meshServerAPIAdapter adapts App meshserver helpers to api.MeshServerProvider.
type meshServerAPIAdapter struct {
	app *App
}

func (m *meshServerAPIAdapter) ListConnections() []meshserver.ConnectionInfo {
	if m == nil || m.app == nil {
		return nil
	}
	return m.app.MeshServerConnections()
}

func (m *meshServerAPIAdapter) ListConnectedServers() []meshserver.ConnectionInfo {
	if m == nil || m.app == nil {
		return nil
	}
	return m.app.MeshServerListConnectedServers()
}

func (m *meshServerAPIAdapter) GetOrCreateSpaceID(serverID string) (int, error) {
	if m == nil || m.app == nil {
		return 0, nil
	}
	return m.app.MeshServerGetOrCreateSpaceID(serverID)
}

func (m *meshServerAPIAdapter) GetServerIDBySpaceID(spaceID int) (string, error) {
	if m == nil || m.app == nil {
		return "", nil
	}
	return m.app.MeshServerGetServerIDBySpaceID(spaceID)
}

func (m *meshServerAPIAdapter) GetOrCreateChannelID(channelID string) (int, error) {
	if m == nil || m.app == nil {
		return 0, nil
	}
	return m.app.MeshServerGetOrCreateChannelID(channelID)
}

func (m *meshServerAPIAdapter) GetChannelIDByChannelIDIntID(channelIDIntID int) (string, error) {
	if m == nil || m.app == nil {
		return "", nil
	}
	return m.app.MeshServerGetChannelIDByChannelIDIntID(channelIDIntID)
}

func (m *meshServerAPIAdapter) Connect(ctx context.Context, name, peerID, clientAgent, protocolID string) (meshserver.ConnectionInfo, error) {
	if m == nil || m.app == nil {
		return meshserver.ConnectionInfo{}, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerConnect(ctx, name, peerID, clientAgent, protocolID)
}

func (m *meshServerAPIAdapter) Disconnect(name string) error {
	if m == nil || m.app == nil {
		return fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerDisconnect(name)
}

func (m *meshServerAPIAdapter) ListServers(ctx context.Context, connection string) (*sessionv1.ListSpacesResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerListServersForConnection(ctx, connection)
}

func (m *meshServerAPIAdapter) JoinServer(ctx context.Context, connection, serverID string) (*sessionv1.JoinSpaceResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerJoinServerForConnection(ctx, connection, serverID)
}

func (m *meshServerAPIAdapter) InviteServerMember(ctx context.Context, connection, serverID, targetUserID string) (*sessionv1.InviteSpaceMemberResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerInviteServerMemberForConnection(ctx, connection, serverID, targetUserID)
}

func (m *meshServerAPIAdapter) KickServerMember(ctx context.Context, connection, serverID, targetUserID string) (*sessionv1.KickSpaceMemberResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerKickServerMemberForConnection(ctx, connection, serverID, targetUserID)
}

func (m *meshServerAPIAdapter) BanServerMember(ctx context.Context, connection, serverID, targetUserID string) (*sessionv1.BanSpaceMemberResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerBanServerMemberForConnection(ctx, connection, serverID, targetUserID)
}

func (m *meshServerAPIAdapter) ListServerMembers(ctx context.Context, connection, serverID string, afterMemberID uint64, limit uint32) (*sessionv1.ListSpaceMembersResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerListServerMembersForConnection(ctx, connection, serverID, afterMemberID, limit)
}

func (m *meshServerAPIAdapter) ListChannels(ctx context.Context, connection, serverID string) (*sessionv1.ListChannelsResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerListChannelsForConnection(ctx, connection, serverID)
}

func (m *meshServerAPIAdapter) CreateGroup(ctx context.Context, connection, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateGroupResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerCreateGroupForConnection(ctx, connection, serverID, name, description, visibility, slowModeSeconds)
}

func (m *meshServerAPIAdapter) CreateChannel(ctx context.Context, connection, serverID, name, description string, visibility sessionv1.Visibility, slowModeSeconds uint32) (*sessionv1.CreateChannelResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerCreateChannelForConnection(ctx, connection, serverID, name, description, visibility, slowModeSeconds)
}

func (m *meshServerAPIAdapter) SubscribeChannel(ctx context.Context, connection, channelID string, lastSeenSeq uint64) (*sessionv1.SubscribeChannelResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerSubscribeChannelForConnection(ctx, connection, channelID, lastSeenSeq)
}

func (m *meshServerAPIAdapter) UnsubscribeChannel(ctx context.Context, connection, channelID string) (*sessionv1.UnsubscribeChannelResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerUnsubscribeChannelForConnection(ctx, connection, channelID)
}

func (m *meshServerAPIAdapter) SendText(ctx context.Context, connection, channelID, clientMsgID, text string) (*sessionv1.SendMessageAck, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerSendTextForConnection(ctx, connection, channelID, clientMsgID, text)
}

func (m *meshServerAPIAdapter) SendMessage(ctx context.Context, connection, channelID, clientMsgID string, messageType sessionv1.MessageType, text string, images []*sessionv1.MediaImage, files []*sessionv1.MediaFile) (*sessionv1.SendMessageAck, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerSendMessageForConnection(ctx, connection, channelID, clientMsgID, messageType, text, images, files)
}

func (m *meshServerAPIAdapter) GetMedia(ctx context.Context, connection, mediaID string) (*sessionv1.MediaFile, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerGetMediaForConnection(ctx, connection, mediaID)
}

func (m *meshServerAPIAdapter) SyncChannel(ctx context.Context, connection, channelID string, afterSeq uint64, limit uint32) (*sessionv1.SyncChannelResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerSyncChannelForConnection(ctx, connection, channelID, afterSeq, limit)
}

func (m *meshServerAPIAdapter) GetMyPermissions(ctx context.Context, connection, serverID string) (*meshserver.MyPermissions, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerGetMyPermissionsForConnection(ctx, connection, serverID)
}

func (m *meshServerAPIAdapter) ListMyServers(ctx context.Context, connection string) (*meshserver.MyServersResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerListMyServersForConnection(ctx, connection)
}

func (m *meshServerAPIAdapter) ListMyGroups(ctx context.Context, connection, spaceID string) (*meshserver.MyGroupsResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerListMyGroupsForConnection(ctx, connection, spaceID)
}

func (m *meshServerAPIAdapter) GetCreateSpacePermissions(ctx context.Context, connection string) (*sessionv1.GetCreateSpacePermissionsResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerGetCreateSpacePermissionsForConnection(ctx, connection)
}

func (m *meshServerAPIAdapter) FetchMeshServerHTTPAccessToken(ctx context.Context, baseURL, protocolID string) (*meshserver.HTTPAccessTokenResult, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerFetchHTTPAccessToken(ctx, baseURL, protocolID)
}

func (m *meshServerAPIAdapter) CreateSpace(ctx context.Context, connection, name, description string, visibility sessionv1.Visibility, allowChannelCreation bool) (*sessionv1.CreateSpaceResp, error) {
	if m == nil || m.app == nil {
		return nil, fmt.Errorf("meshserver client not available")
	}
	return m.app.MeshServerCreateSpaceFor(ctx, connection, name, description, visibility, allowChannelCreation)
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

type socks5TunnelAPIAdapter struct {
	app *App
}

func (s *socks5TunnelAPIAdapter) GetTunnelToExit() bool {
	if s.app == nil {
		return false
	}
	return s.app.cfg.Socks5.TunnelToExit
}

func (s *socks5TunnelAPIAdapter) SetTunnelToExit(enabled bool) {
	if s.app == nil {
		return
	}
	s.app.cfg.Socks5.TunnelToExit = enabled
	if s.app.socks5 != nil {
		s.app.socks5.SetTunnelToExit(enabled, s.app.cfg.Socks5.ExitUpstream)
	}
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
		"traffic":             a.TrafficStats(),
		"circuits_total":      a.circuitStore.Count(),
		"streams_active":      len(streams),
		"relays_known":        len(relays),
		"exits_known":         len(exits),
		"errors_recent_count": errorsCount,
		"pool_status":         poolStatus,
	}
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

func (a *App) restoreCachedPeerConnections() {
	if a == nil || a.host == nil {
		return
	}
	recordsByPeer := make(map[string][]string)
	merge := func(cache *relaycache.Cache) {
		if cache == nil {
			return
		}
		for _, rec := range cache.Records() {
			if rec == nil || rec.PeerID == "" {
				continue
			}
			recordsByPeer[rec.PeerID] = append(recordsByPeer[rec.PeerID], rec.Addrs...)
		}
	}
	merge(a.relayCache)
	merge(a.bootstrapCache)
	for peerID, addrs := range recordsByPeer {
		peerID := peerID
		addrs := filterAddrStrings(addrs)
		if peerID == "" || peerID == a.PeerID() || len(addrs) == 0 {
			continue
		}
		pid, err := peer.Decode(peerID)
		if err != nil {
			continue
		}
		maddrs := make([]multiaddr.Multiaddr, 0, len(addrs))
		for _, addr := range addrs {
			full := ensurePeerMultiaddr(addr, peerID)
			if full == "" {
				continue
			}
			maddr, err := multiaddr.NewMultiaddr(full)
			if err != nil {
				continue
			}
			maddrs = append(maddrs, maddr)
		}
		if len(maddrs) == 0 {
			continue
		}
		a.Host().Peerstore().AddAddrs(pid, maddrs, time.Hour)
		safe.Go("app.restoreCachedPeerConnection", func() {
			if a.Host().Network().Connectedness(pid) == network.Connected {
				return
			}
			ctx, cancel := context.WithTimeout(a.ctx, startupCacheConnectTimeout)
			defer cancel()
			if err := a.Host().Connect(ctx, peer.AddrInfo{ID: pid, Addrs: maddrs}); err != nil {
				log.Printf("[startup] connect cached peer %s failed: %v", peerID, err)
				return
			}
			log.Printf("[startup] connected cached peer %s", peerID)
		})
	}
}

func (a *App) runPeerExchange(ctx context.Context) {
	if a.gossip == nil || a.discovery == nil || a.idMgr == nil || a.selfDesc == nil {
		return
	}
	safe.Go("app.runPeerExchangeSubscriber", func() { a.runPeerExchangeSubscriber(ctx) })
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
	msg, err := a.buildPeerExchangeMessage(false, false)
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

// buildPeerExchangeMessage builds a signed PeerExchangeMessage. If includeSenderStartup is true, Sender uses
// selfPeerExchangeDesc (started_at_unix + version); otherwise Sender uses selfDesc so gossip and legacy 1.0.0 peers verify.
func (a *App) buildPeerExchangeMessage(allowEmpty bool, includeSenderStartup bool) (*discovery.PeerExchangeMessage, error) {
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
	var senderCopy discovery.NodeDescriptor
	if includeSenderStartup && a.selfPeerExchangeDesc != nil {
		senderCopy = *a.selfPeerExchangeDesc
	} else {
		senderCopy = *a.selfDesc
	}
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
	a.Host().SetStreamHandler(p2p.ProtocolPeerXLegacy, a.handlePeerExchangeStream)
	a.Host().Network().Notify(&network.NotifyBundle{
		ConnectedF: func(_ network.Network, conn network.Conn) {
			a.recordConnectedRelay(conn.RemotePeer(), conn.RemoteMultiaddr())
			a.onPeerConnected(conn.RemotePeer())
		},
	})
}

func (a *App) onPeerConnected(pid peer.ID) {
	if a == nil || a.host == nil || pid == "" || pid == a.Host().ID() {
		return
	}
	if a.chat != nil {
		a.chat.OnPeerConnected(pid.String())
	}
	if _, loaded := a.peerExchangeOnce.LoadOrStore(pid.String(), struct{}{}); loaded {
		return
	}
	safe.Go("app.exchangePeerSnapshot", func() { a.exchangePeerSnapshot(pid) })
}

func (a *App) recordConnectedRelay(pid peer.ID, remote multiaddr.Multiaddr) {
	if a == nil || pid == "" {
		return
	}
	addrs := make([]string, 0, 8)
	if remote != nil {
		addrs = append(addrs, remote.String())
	}
	if a.host != nil {
		addrs = append(addrs, filterMultiaddrStrings(a.Host().Peerstore().Addrs(pid))...)
	}
	addrs = filterAddrStrings(addrs)
	if len(addrs) == 0 {
		return
	}
	safe.Go("app.persistConnectedPeerByProtocol", func() { a.persistConnectedPeerByProtocol(pid, addrs) })
}

func (a *App) persistConnectedPeerByProtocol(pid peer.ID, addrs []string) {
	if a == nil || a.host == nil || pid == "" || len(addrs) == 0 {
		return
	}
	supported, err := a.Host().Peerstore().SupportsProtocols(pid, protocol.CircuitProtocolID)
	if err == nil && len(supported) > 0 {
		if a.relayCache != nil {
			if err := a.relayCache.Add(pid.String(), addrs); err != nil {
				log.Printf("[relaycache] persist connected peer %s: %v", pid.String(), err)
			}
		}
		return
	}
	if a.bootstrapCache != nil {
		if err := a.bootstrapCache.AddWithLimit(pid.String(), addrs, bootstrapCacheMaxEntries); err != nil {
			log.Printf("[bootstrapcache] persist connected peer %s: %v", pid.String(), err)
		}
	}
}

func (a *App) exchangePeerSnapshot(pid peer.ID) {
	if a.host == nil || pid == "" {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, peerExchangeDialTimeout)
	defer cancel()
	stream, err := a.Host().NewStream(ctx, pid, p2p.ProtocolPeerX)
	includeSenderStartup := true
	if err != nil {
		stream, err = a.Host().NewStream(ctx, pid, p2p.ProtocolPeerXLegacy)
		includeSenderStartup = false
	}
	if err != nil {
		return
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(peerExchangeStreamTimeout))

	req, err := a.buildPeerExchangeMessage(true, includeSenderStartup)
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
	includeSenderStartup := stream.Protocol() == p2p.ProtocolPeerX

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

	resp, err := a.buildPeerExchangeMessage(true, includeSenderStartup)
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
	// 持久化對端 Sender（含 version / started_at_unix，見 peer-exchange/1.0.1），供 discovery 與聊天中繼排序使用。
	if msg.Sender != nil && msg.Sender.PeerID != "" && msg.Sender.PeerID != a.PeerID() {
		if ok, err := discovery.VerifyDescriptor(msg.Sender); err == nil && ok {
			senderCopy := *msg.Sender
			a.discovery.Store().Upsert(&senderCopy)
		}
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
		descCopy := *desc
		a.discovery.Store().Upsert(&descCopy)

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
	if _, loaded := a.peerExchangeDial.LoadOrStore(peerID, struct{}{}); loaded {
		return
	}
	defer a.peerExchangeDial.Delete(peerID)
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
		if v != "" && v != "0.0.0.0" && v != "127.0.0.1" {
			return true
		}
	}
	if v, err := m.ValueForProtocol(multiaddr.P_IP6); err == nil {
		if v != "" && v != "::" && v != "::1" {
			return true
		}
	}
	return false
}

// chatIPFSAvatarPinner 將聊天頭像寫入嵌入式 IPFS（與 /api/ipfs/add 相同匯入參數）。
type chatIPFSAvatarPinner struct {
	emb *ipfsnode.EmbeddedIPFS
}

func newChatIPFSAvatarPinner(emb *ipfsnode.EmbeddedIPFS) *chatIPFSAvatarPinner {
	return &chatIPFSAvatarPinner{emb: emb}
}

func (p *chatIPFSAvatarPinner) PinAvatar(ctx context.Context, fileName string, data []byte) (string, error) {
	return p.pinFile(ctx, fileName, data)
}

func (p *chatIPFSAvatarPinner) PinChatFile(ctx context.Context, fileName string, data []byte) (string, error) {
	return p.pinFile(ctx, fileName, data)
}

func (p *chatIPFSAvatarPinner) pinFile(ctx context.Context, fileName string, data []byte) (string, error) {
	if p == nil || p.emb == nil || len(data) == 0 {
		return "", nil
	}
	opt := ipfsnode.AddFileOptions{
		Filename:     fileName,
		RawLeaves:    p.emb.RawLeaves(),
		CIDVersion:   p.emb.CIDVersion(),
		HashFunction: p.emb.HashFunction(),
		Chunker:      p.emb.Chunker(),
		Pin:          p.emb.AutoPinOnAdd(),
	}
	c, err := p.emb.Service().AddFile(ctx, bytes.NewReader(data), opt)
	if err != nil {
		return "", err
	}
	return c.String(), nil
}
