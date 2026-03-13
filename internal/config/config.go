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

// ClientConfig configures retry and failover for circuit build and exit connect.
type ClientConfig struct {
	// BuildRetries is the number of retries when building a circuit fails (1 or 2; 0 = no retry).
	BuildRetries int `yaml:"build_retries"`
	// BeginTCPRetries is the number of retries when exit connect (BEGIN_TCP) fails, using another circuit/exit.
	BeginTCPRetries int `yaml:"begin_tcp_retries"`
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

	// Relay must always be enabled, which is implied by mode.
	// When mode is relay+exit, exit is automatically enabled by the rest of the system.
	return nil
}
