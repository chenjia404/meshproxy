package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	peer "github.com/libp2p/go-libp2p/core/peer"
	"gopkg.in/yaml.v3"

	"github.com/chenjia404/meshproxy/internal/offlinestore"
)

// ModeRelay only relay traffic.
const ModeRelay = "relay"

// ModeRelayExit relay and exit traffic.
const ModeRelayExit = "relay+exit"

// Config is the root configuration for meshproxy.
type Config struct {
	// ConfigFilePath is set by the loader/caller (e.g. main) for persisting runtime changes (e.g. exit_selection). Not from YAML.
	ConfigFilePath string `yaml:"-"`
	// Mode controls whether this node only relays traffic or also acts as an exit.
	// Allowed values: "relay", "relay+exit".
	Mode string `yaml:"mode"`

	// DataDir is the base directory for persistent data such as identity keys.
	DataDir string `yaml:"data_dir"`

	// IdentityKeyPath is the file path for the Ed25519 private key.
	// If empty, it will default to $DataDir/identity.key.
	IdentityKeyPath string `yaml:"identity_key_path"`

	// P2P contains libp2p related configuration.
	P2P P2PConfig `yaml:"p2p"`

	// Socks5 contains local SOCKS5 listener configuration.
	Socks5 Socks5Config `yaml:"socks5"`

	// API configures the local management HTTP API.
	API APIConfig `yaml:"api"`

	// AutoUpdate controls whether meshproxy periodically checks GitHub releases
	// and automatically applies updates when a newer compatible asset is found.
	AutoUpdate bool `yaml:"auto_update"`

	// CircuitPool configures pre-built circuit pool (low latency / anonymous / country).
	CircuitPool CircuitPoolConfig `yaml:"circuit_pool"`

	// Client configures client-side retry and failover.
	Client ClientConfig `yaml:"client"`

	// MeshServer configures the centralized meshserver integration.
	MeshServer MeshServerConfig `yaml:"meshserver"`

	// Exit 僅在 mode=relay+exit 時生效，用於出口節點運營策略（允許/拒絕端口、域名、peer 等）。
	Exit *ExitConfig `yaml:"exit"`

	// IPFS 嵌入式閘道與最小寫入 API（復用同一 libp2p host，見 ipfs.md）。
	IPFS IPFSConfig `yaml:"ipfs"`

	// Chat 私聊相關（預設離線 store 節點等）。
	Chat ChatConfig `yaml:"chat"`

	// LogModules 逗号分隔，仅输出匹配 [name] 模块标签的日志行（name 与此处一致，大小写不敏感）；空表示不过滤。无 [module] 前缀的行（如启动提示）仍会输出。
	LogModules string `yaml:"log_modules"`
}

// ChatConfig 私聊；OfflineStorePeers 與 p2p.bootstrap_peers 相同，為 multiaddr 字符串列表（含 /p2p/<peerID>）；亦可僅填 peer_id 走 DHT。
// OfflineStoreNodes 由 Normalize 從 OfflineStorePeers 解析得到，不參與 YAML 反序列化。
type ChatConfig struct {
	OfflineStorePeers []string                        `yaml:"offline_store_peers"`
	OfflineStoreNodes []offlinestore.OfflineStoreNode `yaml:"-"`
}

// IPFSConfig 嵌入式 IPFS 子系統（boxo + 共享 host）。
type IPFSConfig struct {
	Enabled bool `yaml:"enabled"`

	// DataDir 相對於根 DataDir 的子目錄，存放 ipfs 資料；空則為 "ipfs"。
	DataDir string `yaml:"data_dir"`

	GatewayEnabled  bool `yaml:"gateway_enabled"`
	GatewayWritable bool `yaml:"gateway_writable"` // true 時允許 POST/PUT /ipfs/ 上傳（multipart，欄位 file，行為同 /api/ipfs/add）

	APIEnabled bool `yaml:"api_enabled"`

	// Storage
	DatastoreType    string `yaml:"datastore_type"` // "leveldb"
	BlockstoreNoSync bool   `yaml:"blockstore_no_sync"`

	// Import（預設與 ipfs.md 一致）
	Chunker      string `yaml:"chunker"`
	RawLeaves    bool   `yaml:"raw_leaves"`
	CIDVersion   int    `yaml:"cid_version"`
	HashFunction string `yaml:"hash_function"`

	// Provide / routing
	AutoProvide              bool   `yaml:"auto_provide"`
	ReprovideIntervalSeconds int    `yaml:"reprovide_interval_seconds"`
	RoutingMode              string `yaml:"routing_mode"`

	// Fetch
	FetchTimeoutSeconds int    `yaml:"fetch_timeout_seconds"`
	HTTPMirrorGateway   string `yaml:"http_mirror_gateway"`

	// Pin
	AutoPinOnAdd bool `yaml:"auto_pin_on_add"`

	// Add 單次上傳上限（位元組），預設 64MiB。
	MaxUploadBytes int64 `yaml:"max_upload_bytes"`
}

const defaultIPFSHTTPMirrorGateway = "https://ipfs.io"

// ExitConfig 出口節點策略與運行時配置（運營者控制允許代理的目標範圍）。
type ExitConfig struct {
	Enabled bool              `yaml:"enabled"`
	Policy  ExitPolicyConfig  `yaml:"policy"`
	Runtime ExitRuntimeConfig `yaml:"runtime"`
}

// ExitPolicyConfig 出口策略：端口、域名、peer、私網/回環等。
type ExitPolicyConfig struct {
	AllowTCP  bool `yaml:"allow_tcp" json:"allow_tcp"`
	AllowUDP  bool `yaml:"allow_udp" json:"allow_udp"`
	RemoteDNS bool `yaml:"remote_dns" json:"remote_dns"`

	AllowedPorts []int `yaml:"allowed_ports" json:"allowed_ports"`
	DeniedPorts  []int `yaml:"denied_ports" json:"denied_ports"`

	AllowedDomains        []string `yaml:"allowed_domains" json:"allowed_domains"`
	DeniedDomains         []string `yaml:"denied_domains" json:"denied_domains"`
	AllowedDomainSuffixes []string `yaml:"allowed_domain_suffixes" json:"allowed_domain_suffixes"`
	DeniedDomainSuffixes  []string `yaml:"denied_domain_suffixes" json:"denied_domain_suffixes"`

	PeerWhitelist []string `yaml:"peer_whitelist" json:"peer_whitelist"`
	PeerBlacklist []string `yaml:"peer_blacklist" json:"peer_blacklist"`

	AllowPrivateIPTargets bool `yaml:"allow_private_ip_targets" json:"allow_private_ip_targets"`
	AllowLoopbackTargets  bool `yaml:"allow_loopback_targets" json:"allow_loopback_targets"`
	AllowLinkLocalTargets bool `yaml:"allow_link_local_targets" json:"allow_link_local_targets"`
}

// ExitRuntimeConfig 運行時狀態（drain 模式等），可通過 API 更新。
type ExitRuntimeConfig struct {
	DrainMode        bool `yaml:"drain_mode" json:"drain_mode"`
	AcceptNewStreams bool `yaml:"accept_new_streams" json:"accept_new_streams"`
}

// ClientConfig configures retry and failover for circuit build and exit connect, and exit selection.
type ClientConfig struct {
	// BuildRetries is the number of retries when building a circuit fails (1 or 2; 0 = no retry).
	BuildRetries int `yaml:"build_retries"`
	// BeginTCPRetries is the number of retries when exit connect (BEGIN_TCP) fails, using another circuit/exit.
	BeginTCPRetries int `yaml:"begin_tcp_retries"`
	// BeginConnectTimeoutSeconds is how long to wait for CONNECTED after BEGIN before counting a timeout.
	BeginConnectTimeoutSeconds int `yaml:"begin_connect_timeout_seconds"`
	// HeartbeatEnabled controls whether idle circuits are periodically probed.
	HeartbeatEnabled bool `yaml:"heartbeat_enabled"`
	// HeartbeatIntervalSeconds is how often the client sends ping on reusable circuits.
	HeartbeatIntervalSeconds int `yaml:"heartbeat_interval_seconds"`
	// HeartbeatTimeoutSeconds is how long to wait for pong before counting one failure.
	HeartbeatTimeoutSeconds int `yaml:"heartbeat_timeout_seconds"`
	// HeartbeatFailureThreshold is the number of consecutive heartbeat failures before a circuit is declared dead.
	HeartbeatFailureThreshold int `yaml:"heartbeat_failure_threshold"`
	// SkipHeartbeatWhenActiveSeconds skips heartbeat if the circuit recently carried traffic.
	SkipHeartbeatWhenActiveSeconds int `yaml:"skip_heartbeat_when_active_seconds"`
	// ExitSelection configures how the last hop (exit) is chosen for each circuit.
	ExitSelection ExitSelectionConfig `yaml:"exit_selection"`
	// GeoIP configures how to resolve exit node country from IP (when descriptor has no ExitInfo.Country).
	GeoIP GeoIPConfig `yaml:"geoip"`
}

// MeshServerConfig configures the centralized meshserver integration.
type MeshServerConfig struct {
	// Enabled is kept for backward compatibility and ignored by runtime wiring.
	Enabled bool `yaml:"enabled"`
	// PeerID is the legacy single-server connection target. Prefer Servers.
	PeerID string `yaml:"peer_id"`
	// Servers contains one or more meshserver connections.
	Servers []MeshServerConnectionConfig `yaml:"servers"`
	// ClientAgent is the legacy default client agent for single-server setups.
	ClientAgent string `yaml:"client_agent"`
	// ProtocolID is the legacy default protocol id for single-server setups.
	ProtocolID string `yaml:"protocol_id"`
}

// MeshServerConnectionConfig configures one meshserver connection.
type MeshServerConnectionConfig struct {
	Name        string `yaml:"name"`
	PeerID      string `yaml:"peer_id"`
	ClientAgent string `yaml:"client_agent"`
	ProtocolID  string `yaml:"protocol_id"`
}

// GeoIPConfig configures IP-to-country resolution for exit node selection/display.
type GeoIPConfig struct {
	// Provider: "none" (default), "ip-api", "geolite2". geolite2 uses data_dir/GeoLite2-Country.mmdb (downloads if missing).
	Provider string `yaml:"provider"`
	// CacheTTLMinutes is how long to cache IP->country (default 1440 = 24h). Only used when Provider is set.
	CacheTTLMinutes int `yaml:"cache_ttl_minutes"`
}

// ExitSelectionMode is the strategy for choosing the exit node.
type ExitSelectionMode string

const (
	ExitSelectionAuto             ExitSelectionMode = "auto"
	ExitSelectionCountryOnly      ExitSelectionMode = "country_only"
	ExitSelectionCountryPreferred ExitSelectionMode = "country_preferred"
	ExitSelectionFixedPeer        ExitSelectionMode = "fixed_peer"
)

// ExitSelectionConfig configures exit node selection (last hop of the circuit).
type ExitSelectionConfig struct {
	Mode               ExitSelectionMode `yaml:"mode" json:"mode"`
	AllowedCountries   []string          `yaml:"allowed_countries" json:"allowed_countries"`
	PreferredCountries []string          `yaml:"preferred_countries" json:"preferred_countries"`
	FixedExitPeerID    string            `yaml:"fixed_exit_peer_id" json:"fixed_exit_peer_id"`

	ExcludeCountries  []string `yaml:"exclude_countries" json:"exclude_countries"`
	ExcludePeerIDs    []string `yaml:"exclude_peer_ids" json:"exclude_peer_ids"`
	RequireRemoteDNS  bool     `yaml:"require_remote_dns" json:"require_remote_dns"`
	RequireTCPSupport bool     `yaml:"require_tcp_support" json:"require_tcp_support"`

	FallbackToAny   bool `yaml:"fallback_to_any" json:"fallback_to_any"`
	AllowDirectExit bool `yaml:"allow_direct_exit" json:"allow_direct_exit"`
}

// CircuitPoolConfig configures the circuit pool for pre-built circuits.
type CircuitPoolConfig struct {
	// MinPerPool is the minimum number of idle circuits to keep per pool kind.
	MinPerPool int `yaml:"min_per_pool" json:"min_per_pool"`
	// MaxPerPool is the maximum number of circuits (idle + in use) per pool kind.
	MaxPerPool int `yaml:"max_per_pool" json:"max_per_pool"`
	// MinTotal is the minimum total number of circuits to keep across all pool kinds.
	MinTotal int `yaml:"min_total" json:"min_total"`
	// MaxTotal is the maximum total number of circuits to keep across all pool kinds.
	MaxTotal int `yaml:"max_total" json:"max_total"`
	// IdleTimeoutSeconds is the max seconds an idle circuit stays in pool before being closed.
	IdleTimeoutSeconds int `yaml:"idle_timeout_seconds" json:"idle_timeout_seconds"`
	// ReplenishIntervalSeconds is how often the pool maintenance runs.
	ReplenishIntervalSeconds int `yaml:"replenish_interval_seconds" json:"replenish_interval_seconds"`
}

// P2PConfig groups libp2p related configuration.
type P2PConfig struct {
	// ListenAddrs is a list of multiaddrs to listen on.
	ListenAddrs []string `yaml:"listen_addrs"`

	// PublicIP is the public IP address that libp2p should advertise.
	// When set, the host will rewrite listen addrs to use this IP for address announcement
	// and disable identify-based address discovery to avoid leaking private IPs.
	PublicIP string `yaml:"public_ip"`

	// BootstrapPeers are the multiaddrs of peers to connect to on startup.
	BootstrapPeers []string `yaml:"bootstrap_peers"`

	// NoDiscovery disables DHT rendezvous advertise/find-peers discovery.
	NoDiscovery bool `yaml:"nodisc"`

	// DiscoveryTag is the rendezvous string used for DHT-based peer discovery.
	// Nodes sharing the same tag will try to discover and connect to each other.
	DiscoveryTag string `yaml:"discovery_tag"`
}

// Socks5Config groups SOCKS5 listener configuration.
type Socks5Config struct {
	// Listen is the address for the local SOCKS5 server.
	Listen string `yaml:"listen"`
	// TunnelToExit forwards accepted TCP streams to the selected exit's local SOCKS5 service without parsing SOCKS5 locally.
	TunnelToExit bool `yaml:"tunnel_to_exit"`
	// ExitUpstream is the SOCKS5 address that the exit node should connect to when TunnelToExit is enabled.
	ExitUpstream string `yaml:"exit_upstream"`
	// AllowUDPAssociate enables SOCKS5 UDP ASSOCIATE (relay UDP over circuits).
	AllowUDPAssociate bool `yaml:"allow_udp_associate"`
}

// APIConfig groups local management API configuration.
type APIConfig struct {
	// Listen is the HTTP address for the local API.
	Listen string `yaml:"listen"`
}

// Default returns a Config with sane defaults.
func Default() Config {
	return Config{
		Mode:    ModeRelay,
		DataDir: "data",
		P2P: P2PConfig{
			ListenAddrs: []string{
				"/ip4/0.0.0.0/tcp/4001",
				"/ip6/::/tcp/4001",
				"/ip4/0.0.0.0/udp/4001/quic-v1",
				"/ip6/::/udp/4001/quic-v1",
			},
			BootstrapPeers: []string{
				"/ip4/8.210.130.36/tcp/4002/p2p/12D3KooWMxVFQZVQwnPqVF4Leosp1qewEtCzYqnEgizSAi33x3EK",
				"/ip4/104.131.131.82/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
				"/dnsaddr/bootstrap.libp2p.io/ipfs/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
				"/dnsaddr/bootstrap.libp2p.io/ipfs/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
				"/dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb",
			},
			DiscoveryTag: "meshproxy",
		},
		Socks5: Socks5Config{
			TunnelToExit:      true,
			ExitUpstream:      "127.0.0.1:1081",
			Listen:            "127.0.0.1:1080",
			AllowUDPAssociate: true,
		},
		API: APIConfig{
			Listen: "127.0.0.1:19080",
		},
		AutoUpdate: true,
		CircuitPool: CircuitPoolConfig{
			MinPerPool:               1,
			MaxPerPool:               3,
			MinTotal:                 3,
			MaxTotal:                 5,
			IdleTimeoutSeconds:       300,
			ReplenishIntervalSeconds: 30,
		},
		Client: ClientConfig{
			BuildRetries:                   1,
			BeginTCPRetries:                1,
			BeginConnectTimeoutSeconds:     30,
			HeartbeatEnabled:               true,
			HeartbeatIntervalSeconds:       30,
			HeartbeatTimeoutSeconds:        8,
			HeartbeatFailureThreshold:      5,
			SkipHeartbeatWhenActiveSeconds: 30,
			ExitSelection: ExitSelectionConfig{
				Mode:              ExitSelectionAuto,
				RequireTCPSupport: true,
				FallbackToAny:     true,
				AllowDirectExit:   true,
			},
			GeoIP: GeoIPConfig{
				Provider:        "none",
				CacheTTLMinutes: 1440,
			},
		},
		MeshServer: MeshServerConfig{
			Enabled:     true,
			ClientAgent: "meshproxy-client",
			ProtocolID:  "/meshserver/session/1.0.0",
		},
		IPFS: IPFSConfig{
			Enabled:             true,
			DataDir:             "ipfs",
			GatewayEnabled:      true,
			GatewayWritable:     true,
			APIEnabled:          true,
			DatastoreType:       "leveldb",
			Chunker:             "size-1048576",
			RawLeaves:           true,
			CIDVersion:          1,
			HashFunction:        "sha2-256",
			AutoProvide:         true,
			RoutingMode:         "dht-client",
			FetchTimeoutSeconds: 60,
			HTTPMirrorGateway:   defaultIPFSHTTPMirrorGateway,
			AutoPinOnAdd:        true,
			MaxUploadBytes:      64 << 20,
		},
		Chat: ChatConfig{},
	}
}

// Load reads configuration from YAML file and merges with defaults.
// If the file does not exist, defaults are used.
func Load(path string) (Config, error) {
	cfg := Default()

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// no config file, just use defaults
			if err := cfg.postProcess(); err != nil {
				return Config{}, err
			}
			return cfg, cfg.Validate()
		}
		return Config{}, fmt.Errorf("stat config file: %w", err)
	}
	if info.IsDir() {
		return Config{}, fmt.Errorf("config path %s is a directory", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal yaml: %w", err)
	}

	if err := cfg.Normalize(); err != nil {
		return Config{}, err
	}
	return cfg, cfg.Validate()
}

// Normalize fills derived fields and defaults that depend on others.
func (c *Config) Normalize() error {
	return c.postProcess()
}

// postProcess fills derived fields and defaults that depend on others.
func (c *Config) postProcess() error {
	c.LogModules = strings.TrimSpace(c.LogModules)
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	c.P2P.PublicIP = strings.TrimSpace(c.P2P.PublicIP)
	if c.IdentityKeyPath == "" {
		c.IdentityKeyPath = filepath.Join(c.DataDir, "identity.key")
	}
	if c.CircuitPool.MinPerPool <= 0 {
		c.CircuitPool.MinPerPool = 1
	}
	if c.CircuitPool.MaxPerPool <= 0 {
		c.CircuitPool.MaxPerPool = 2
	}
	if c.CircuitPool.MaxPerPool < c.CircuitPool.MinPerPool {
		c.CircuitPool.MaxPerPool = c.CircuitPool.MinPerPool
	}
	if c.CircuitPool.MinTotal <= 0 {
		c.CircuitPool.MinTotal = 1
	}
	if c.CircuitPool.MaxTotal <= 0 {
		c.CircuitPool.MaxTotal = 1
	}
	if c.CircuitPool.MaxTotal < c.CircuitPool.MinTotal {
		c.CircuitPool.MaxTotal = c.CircuitPool.MinTotal
	}
	if c.CircuitPool.IdleTimeoutSeconds <= 0 {
		c.CircuitPool.IdleTimeoutSeconds = 300
	}
	if c.CircuitPool.ReplenishIntervalSeconds <= 0 {
		c.CircuitPool.ReplenishIntervalSeconds = 30
	}
	if c.Client.BuildRetries < 0 {
		c.Client.BuildRetries = 0
	}
	if c.Client.BuildRetries > 2 {
		c.Client.BuildRetries = 2
	}
	if c.Client.BeginTCPRetries < 0 {
		c.Client.BeginTCPRetries = 0
	}
	if c.Client.BeginTCPRetries > 2 {
		c.Client.BeginTCPRetries = 2
	}
	if c.Client.BeginConnectTimeoutSeconds <= 0 {
		c.Client.BeginConnectTimeoutSeconds = 30
	}
	if c.Client.HeartbeatIntervalSeconds <= 0 {
		c.Client.HeartbeatIntervalSeconds = 30
	}
	if c.Client.HeartbeatTimeoutSeconds <= 0 {
		c.Client.HeartbeatTimeoutSeconds = 8
	}
	if c.Client.HeartbeatFailureThreshold <= 0 {
		c.Client.HeartbeatFailureThreshold = 5
	}
	if c.Client.SkipHeartbeatWhenActiveSeconds < 0 {
		c.Client.SkipHeartbeatWhenActiveSeconds = 0
	} else if c.Client.SkipHeartbeatWhenActiveSeconds == 0 {
		c.Client.SkipHeartbeatWhenActiveSeconds = 30
	}
	if c.Client.ExitSelection.Mode == "" {
		c.Client.ExitSelection.Mode = ExitSelectionAuto
	}
	if c.MeshServer.ClientAgent == "" {
		c.MeshServer.ClientAgent = "meshproxy-client"
	}
	if c.MeshServer.ProtocolID == "" {
		c.MeshServer.ProtocolID = "/meshserver/session/1.0.0"
	}
	if len(c.MeshServer.Servers) == 0 && c.MeshServer.PeerID != "" {
		c.MeshServer.Servers = []MeshServerConnectionConfig{{
			Name:        "default",
			PeerID:      c.MeshServer.PeerID,
			ClientAgent: c.MeshServer.ClientAgent,
			ProtocolID:  c.MeshServer.ProtocolID,
		}}
	}
	for i := range c.MeshServer.Servers {
		if c.MeshServer.Servers[i].Name == "" {
			if c.MeshServer.Servers[i].PeerID != "" {
				c.MeshServer.Servers[i].Name = c.MeshServer.Servers[i].PeerID
			} else {
				c.MeshServer.Servers[i].Name = fmt.Sprintf("meshserver-%d", i+1)
			}
		}
		if c.MeshServer.Servers[i].ClientAgent == "" {
			c.MeshServer.Servers[i].ClientAgent = c.MeshServer.ClientAgent
		}
		if c.MeshServer.Servers[i].ProtocolID == "" {
			c.MeshServer.Servers[i].ProtocolID = c.MeshServer.ProtocolID
		}
	}
	if c.Mode == ModeRelayExit && c.Exit == nil {
		exit := defaultExitConfig()
		c.Exit = &exit
	}
	if c.Exit != nil {
		if err := c.Exit.Policy.Validate(); err != nil {
			return err
		}
	}
	if err := c.IPFS.normalize(); err != nil {
		return err
	}
	if err := c.resolveChatOfflineStorePeers(); err != nil {
		return err
	}
	for _, srv := range c.MeshServer.Servers {
		if srv.PeerID == "" {
			return errors.New("meshserver.servers.peer_id must not be empty")
		}
	}
	return nil
}

// resolveChatOfflineStorePeers 將 chat.offline_store_peers（與 bootstrap_peers 同格式的字符串列表）解析為 OfflineStoreNodes。
func (c *Config) resolveChatOfflineStorePeers() error {
	if c == nil {
		return nil
	}
	c.Chat.OfflineStoreNodes = nil
	seen := make(map[string]struct{})
	for i, raw := range c.Chat.OfflineStorePeers {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		node, err := offlinestore.ParseOfflineStorePeerEntry(raw)
		if err != nil {
			return fmt.Errorf("chat.offline_store_peers[%d]: %w", i, err)
		}
		pid := strings.TrimSpace(node.PeerID)
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		c.Chat.OfflineStoreNodes = append(c.Chat.OfflineStoreNodes, node)
	}
	return nil
}

func defaultExitPolicyConfig() ExitPolicyConfig {
	return ExitPolicyConfig{
		AllowTCP:              true,
		AllowUDP:              false,
		RemoteDNS:             true,
		AllowedPorts:          []int{80, 443, 8080},
		DeniedPorts:           []int{25, 465, 587, 22, 3389},
		AllowPrivateIPTargets: false,
		AllowLoopbackTargets:  false,
		AllowLinkLocalTargets: false,
	}
}

func defaultExitConfig() ExitConfig {
	return ExitConfig{
		Enabled: true,
		Policy:  defaultExitPolicyConfig(),
		Runtime: ExitRuntimeConfig{
			DrainMode:        false,
			AcceptNewStreams: true,
		},
	}
}

func (c *IPFSConfig) normalize() error {
	if c.DataDir == "" {
		c.DataDir = "ipfs"
	}
	if c.DatastoreType == "" {
		c.DatastoreType = "leveldb"
	}
	if c.Chunker == "" {
		c.Chunker = "size-1048576"
	}
	if c.CIDVersion == 0 {
		c.CIDVersion = 1
	}
	if c.HashFunction == "" {
		c.HashFunction = "sha2-256"
	}
	if c.RoutingMode == "" {
		c.RoutingMode = "dht-client"
	}
	if c.FetchTimeoutSeconds <= 0 {
		c.FetchTimeoutSeconds = 60
	}
	c.HTTPMirrorGateway = strings.TrimSpace(c.HTTPMirrorGateway)
	c.HTTPMirrorGateway = strings.TrimRight(c.HTTPMirrorGateway, "/")
	if c.HTTPMirrorGateway == "" {
		c.HTTPMirrorGateway = defaultIPFSHTTPMirrorGateway
	}
	if c.HTTPMirrorGateway != "" {
		u, err := url.Parse(c.HTTPMirrorGateway)
		if err != nil {
			return fmt.Errorf("invalid ipfs.http_mirror_gateway: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("invalid ipfs.http_mirror_gateway scheme %q", u.Scheme)
		}
		if strings.TrimSpace(u.Host) == "" {
			return errors.New("ipfs.http_mirror_gateway host must not be empty")
		}
	}
	if c.MaxUploadBytes <= 0 {
		c.MaxUploadBytes = 64 << 20
	}
	return nil
}

// Validate 校驗出口策略配置。
func (e *ExitPolicyConfig) Validate() error {
	// denied 優先於 allowed，無需額外校驗
	return nil
}

// Validate checks configuration values for correctness.
func (c *Config) Validate() error {
	switch c.Mode {
	case ModeRelay, ModeRelayExit:
	default:
		return fmt.Errorf("invalid mode %q, must be %q or %q", c.Mode, ModeRelay, ModeRelayExit)
	}

	if c.Socks5.Listen == "" {
		return errors.New("socks5.listen must not be empty")
	}
	if c.Socks5.ExitUpstream == "" {
		c.Socks5.ExitUpstream = "127.0.0.1:1081"
	}
	if c.Socks5.TunnelToExit {
		if _, _, err := net.SplitHostPort(c.Socks5.ExitUpstream); err != nil {
			return fmt.Errorf("invalid socks5.exit_upstream %q: %w", c.Socks5.ExitUpstream, err)
		}
	}
	if c.API.Listen == "" {
		return errors.New("api.listen must not be empty")
	}
	if c.P2P.PublicIP != "" && net.ParseIP(c.P2P.PublicIP) == nil {
		return fmt.Errorf("invalid p2p.public_ip %q", c.P2P.PublicIP)
	}

	switch c.Client.ExitSelection.Mode {
	case ExitSelectionAuto, ExitSelectionCountryOnly, ExitSelectionCountryPreferred, ExitSelectionFixedPeer:
	default:
		return fmt.Errorf("invalid client.exit_selection.mode %q", c.Client.ExitSelection.Mode)
	}
	if c.Client.ExitSelection.Mode == ExitSelectionFixedPeer && c.Client.ExitSelection.FixedExitPeerID == "" {
		return errors.New("client.exit_selection.fixed_exit_peer_id required when mode is fixed_peer")
	}
	for i, n := range c.Chat.OfflineStoreNodes {
		pid := strings.TrimSpace(n.PeerID)
		if pid == "" {
			return fmt.Errorf("chat.offline_store_peers[%d]: peer_id is required", i)
		}
		if _, err := peer.Decode(pid); err != nil {
			return fmt.Errorf("chat.offline_store_peers[%d]: %w", i, err)
		}
	}
	return nil
}

// MeshServerConnections returns the normalized list of meshserver connections.
func (c Config) MeshServerConnections() []MeshServerConnectionConfig {
	if len(c.MeshServer.Servers) == 0 && c.MeshServer.PeerID == "" {
		return nil
	}
	out := make([]MeshServerConnectionConfig, 0, len(c.MeshServer.Servers))
	if len(c.MeshServer.Servers) > 0 {
		out = append(out, c.MeshServer.Servers...)
		return out
	}
	return []MeshServerConnectionConfig{{
		Name:        "default",
		PeerID:      c.MeshServer.PeerID,
		ClientAgent: c.MeshServer.ClientAgent,
		ProtocolID:  c.MeshServer.ProtocolID,
	}}
}

// Write 將配置寫入 YAML 文件（用於持久化出口選擇等運行時修改）。
func Write(path string, c *Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// SaveExitSelection 從 path 讀取配置，僅更新 client.exit_selection 後寫回，使控制台/API 的修改持久化。
func SaveExitSelection(path string, ec ExitSelectionConfig) error {
	c, err := Load(path)
	if err != nil {
		return fmt.Errorf("load config for save: %w", err)
	}
	c.Client.ExitSelection = ec
	return Write(path, &c)
}

// SaveExitSelectionSettings persists client.exit_selection and optional socks5.tunnel_to_exit together.
func SaveExitSelectionSettings(path string, ec ExitSelectionConfig, tunnelToExit *bool) error {
	if path == "" {
		return nil
	}
	c, err := Load(path)
	if err != nil {
		return fmt.Errorf("load config for save: %w", err)
	}
	c.Client.ExitSelection = ec
	if tunnelToExit != nil {
		c.Socks5.TunnelToExit = *tunnelToExit
	}
	return Write(path, &c)
}

// SaveExitConfig 從 path 讀取配置，僅更新 exit 段（policy + runtime）後寫回，使 API 的出口策略/維護模式修改持久化。
func SaveExitConfig(path string, exit ExitConfig) error {
	if path == "" {
		return nil
	}
	c, err := Load(path)
	if err != nil {
		return fmt.Errorf("load config for save: %w", err)
	}
	c.Exit = &exit
	return Write(path, &c)
}

// SaveCircuitPoolConfig 從 path 讀取配置，僅更新 circuit_pool 後寫回，使控制台/API 的修改持久化。
func SaveCircuitPoolConfig(path string, cp CircuitPoolConfig) error {
	if path == "" {
		return nil
	}
	c, err := Load(path)
	if err != nil {
		return fmt.Errorf("load config for save: %w", err)
	}
	c.CircuitPool = cp
	return Write(path, &c)
}

// SaveAutoUpdateSettings updates the root auto_update flag and persists it to the config file.
func SaveAutoUpdateSettings(path string, enabled bool) error {
	if path == "" {
		return nil
	}
	c, err := Load(path)
	if err != nil {
		return fmt.Errorf("load config for save: %w", err)
	}
	c.AutoUpdate = enabled
	return Write(path, &c)
}
