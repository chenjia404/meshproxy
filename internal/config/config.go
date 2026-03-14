package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
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

	// CircuitPool configures pre-built circuit pool (low latency / anonymous / country).
	CircuitPool CircuitPoolConfig `yaml:"circuit_pool"`

	// Client configures client-side retry and failover.
	Client ClientConfig `yaml:"client"`
}

// ClientConfig configures retry and failover for circuit build and exit connect, and exit selection.
type ClientConfig struct {
	// BuildRetries is the number of retries when building a circuit fails (1 or 2; 0 = no retry).
	BuildRetries int `yaml:"build_retries"`
	// BeginTCPRetries is the number of retries when exit connect (BEGIN_TCP) fails, using another circuit/exit.
	BeginTCPRetries int `yaml:"begin_tcp_retries"`
	// ExitSelection configures how the last hop (exit) is chosen for each circuit.
	ExitSelection ExitSelectionConfig `yaml:"exit_selection"`
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
	MinPerPool int `yaml:"min_per_pool"`
	// MaxPerPool is the maximum number of circuits (idle + in use) per pool kind.
	MaxPerPool int `yaml:"max_per_pool"`
	// IdleTimeoutSeconds is the max seconds an idle circuit stays in pool before being closed.
	IdleTimeoutSeconds int `yaml:"idle_timeout_seconds"`
	// ReplenishIntervalSeconds is how often the pool maintenance runs.
	ReplenishIntervalSeconds int `yaml:"replenish_interval_seconds"`
}

// P2PConfig groups libp2p related configuration.
type P2PConfig struct {
	// ListenAddrs is a list of multiaddrs to listen on.
	ListenAddrs []string `yaml:"listen_addrs"`

	// BootstrapPeers are the multiaddrs of peers to connect to on startup.
	BootstrapPeers []string `yaml:"bootstrap_peers"`

	// DiscoveryTag is the rendezvous string used for DHT-based peer discovery.
	// Nodes sharing the same tag will try to discover and connect to each other.
	DiscoveryTag string `yaml:"discovery_tag"`
}

// Socks5Config groups SOCKS5 listener configuration.
type Socks5Config struct {
	// Listen is the address for the local SOCKS5 server.
	Listen string `yaml:"listen"`
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
				"/ip4/104.131.131.82/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
				"/dnsaddr/bootstrap.libp2p.io/ipfs/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
				"/dnsaddr/bootstrap.libp2p.io/ipfs/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
				"/dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb",
			},
			DiscoveryTag: "meshproxy",
		},
		Socks5: Socks5Config{
			Listen: "127.0.0.1:1080",
		},
		API: APIConfig{
			Listen: "127.0.0.1:19080",
		},
		CircuitPool: CircuitPoolConfig{
			MinPerPool:               1,
			MaxPerPool:               3,
			IdleTimeoutSeconds:       300,
			ReplenishIntervalSeconds: 30,
		},
		Client: ClientConfig{
			BuildRetries:    1,
			BeginTCPRetries: 1,
			ExitSelection: ExitSelectionConfig{
				Mode:               ExitSelectionAuto,
				RequireTCPSupport:  true,
				FallbackToAny:     true,
				AllowDirectExit:   true,
			},
		},
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

	if err := cfg.postProcess(); err != nil {
		return Config{}, err
	}
	return cfg, cfg.Validate()
}

// postProcess fills derived fields and defaults that depend on others.
func (c *Config) postProcess() error {
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	if c.IdentityKeyPath == "" {
		c.IdentityKeyPath = filepath.Join(c.DataDir, "identity.key")
	}
	if c.CircuitPool.MinPerPool <= 0 {
		c.CircuitPool.MinPerPool = 1
	}
	if c.CircuitPool.MaxPerPool <= 0 {
		c.CircuitPool.MaxPerPool = 3
	}
	if c.CircuitPool.MaxPerPool < c.CircuitPool.MinPerPool {
		c.CircuitPool.MaxPerPool = c.CircuitPool.MinPerPool
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
	if c.Client.ExitSelection.Mode == "" {
		c.Client.ExitSelection.Mode = ExitSelectionAuto
	}
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
	if c.API.Listen == "" {
		return errors.New("api.listen must not be empty")
	}

	switch c.Client.ExitSelection.Mode {
	case ExitSelectionAuto, ExitSelectionCountryOnly, ExitSelectionCountryPreferred, ExitSelectionFixedPeer:
	default:
		return fmt.Errorf("invalid client.exit_selection.mode %q", c.Client.ExitSelection.Mode)
	}
	if c.Client.ExitSelection.Mode == ExitSelectionFixedPeer && c.Client.ExitSelection.FixedExitPeerID == "" {
		return errors.New("client.exit_selection.fixed_exit_peer_id required when mode is fixed_peer")
	}
	return nil
}

// Write 將配置寫入 YAML 文件（用於持久化出口策略等運行時修改）。
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
