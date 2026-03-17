package sdk

import (
	"context"
	"time"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
	host "github.com/libp2p/go-libp2p/core/host"
	corerouting "github.com/libp2p/go-libp2p/core/routing"

	"github.com/chenjia404/meshproxy/internal/app"
	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/identity"
	"github.com/chenjia404/meshproxy/internal/update"
)

type Config = config.Config
type APIConfig = config.APIConfig
type P2PConfig = config.P2PConfig
type Socks5Config = config.Socks5Config
type CircuitPoolConfig = config.CircuitPoolConfig
type ClientConfig = config.ClientConfig
type ExitConfig = config.ExitConfig
type ExitSelectionConfig = config.ExitSelectionConfig
type ExitSelectionMode = config.ExitSelectionMode
type GeoIPConfig = config.GeoIPConfig

const (
	ModeRelay     = config.ModeRelay
	ModeRelayExit = config.ModeRelayExit

	ExitSelectionAuto             = config.ExitSelectionAuto
	ExitSelectionCountryOnly      = config.ExitSelectionCountryOnly
	ExitSelectionCountryPreferred = config.ExitSelectionCountryPreferred
	ExitSelectionFixedPeer        = config.ExitSelectionFixedPeer
)

type Options struct {
	EnableSOCKS5   bool
	EnableLocalAPI bool
	Host           host.Host
	Routing        corerouting.Routing
	CloseHost      bool
}

type UpdateInfo = update.Info
type UpdateApplyResult = update.ApplyResult

type Node struct {
	inner *app.App
	chat  *ChatService
}

func DefaultConfig() Config {
	return config.Default()
}

func LoadConfig(path string) (Config, error) {
	return config.Load(path)
}

func LoadOrCreatePrivateKey(path string) (crypto.PrivKey, error) {
	mgr, err := identity.NewManager(path)
	if err != nil {
		return nil, err
	}
	return mgr.PrivateKey(), nil
}

func New(ctx context.Context, cfg Config, opts Options) (*Node, error) {
	inner, err := app.NewWithOptions(ctx, cfg, app.Options{
		EnableSOCKS5:   opts.EnableSOCKS5,
		EnableLocalAPI: opts.EnableLocalAPI,
		Host:           opts.Host,
		Routing:        opts.Routing,
		CloseHost:      opts.CloseHost,
	})
	if err != nil {
		return nil, err
	}
	return &Node{
		inner: inner,
		chat:  &ChatService{inner: inner.ChatService()},
	}, nil
}

func (n *Node) Close() error {
	if n == nil || n.inner == nil {
		return nil
	}
	return n.inner.Close()
}

func (n *Node) Run() error {
	if n == nil || n.inner == nil {
		return nil
	}
	return n.inner.Run()
}

func (n *Node) PeerID() string {
	if n == nil || n.inner == nil {
		return ""
	}
	return n.inner.PeerID()
}

func (n *Node) Mode() string {
	if n == nil || n.inner == nil {
		return ""
	}
	return n.inner.Mode()
}

func (n *Node) Socks5Listen() string {
	if n == nil || n.inner == nil {
		return ""
	}
	return n.inner.Socks5Listen()
}

func (n *Node) LocalAPIListen() string {
	if n == nil || n.inner == nil {
		return ""
	}
	return n.inner.LocalAPIListen()
}

func (n *Node) P2PListenAddrs() []string {
	if n == nil || n.inner == nil {
		return nil
	}
	return n.inner.P2PListenAddrs()
}

func (n *Node) StartTime() time.Time {
	if n == nil || n.inner == nil {
		return time.Time{}
	}
	return n.inner.StartTime()
}

func (n *Node) Host() host.Host {
	if n == nil || n.inner == nil {
		return nil
	}
	return n.inner.Host()
}

func (n *Node) Chat() *ChatService {
	if n == nil {
		return nil
	}
	return n.chat
}

func (n *Node) CheckForUpdate(ctx context.Context) (UpdateInfo, error) {
	if n == nil || n.inner == nil {
		return UpdateInfo{}, nil
	}
	return n.inner.CheckForUpdate(ctx)
}

func (n *Node) ApplyUpdate(ctx context.Context) (UpdateApplyResult, error) {
	if n == nil || n.inner == nil {
		return UpdateApplyResult{}, nil
	}
	return n.inner.ApplyUpdate(ctx)
}
