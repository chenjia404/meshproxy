package config

import (
	"flag"
	"io"
	"testing"
)

func TestRegisterFlagsMapsNestedConfigFields(t *testing.T) {
	cfg := Default()
	cfg.Exit = nil

	fs := flag.NewFlagSet("meshproxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := RegisterFlags(fs, &cfg); err != nil {
		t.Fatalf("RegisterFlags() error = %v", err)
	}

	args := []string{
		"--mode=relay+exit",
		"--data_dir=/tmp/meshproxy",
		"--p2p.nodisc",
		"--p2p.bootstrap_peers=/ip4/1.2.3.4/tcp/1234/p2p/QmTest,/dnsaddr/bootstrap.libp2p.io/p2p/QmFoo",
		"--socks5.listen=127.0.0.1:1089",
		"--api.listen=127.0.0.1:19081",
		"--client.exit_selection.mode=fixed_peer",
		"--client.exit_selection.fixed_exit_peer_id=12D3KooWabc",
		"--exit.enabled=false",
		"--exit.policy.allow_tcp=false",
		"--exit.policy.allowed_ports=80,443",
		"--exit.runtime.accept_new_streams=false",
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.Mode != ModeRelayExit {
		t.Fatalf("cfg.Mode = %q, want %q", cfg.Mode, ModeRelayExit)
	}
	if !cfg.P2P.NoDiscovery {
		t.Fatalf("cfg.P2P.NoDiscovery = false, want true")
	}
	if got, want := cfg.P2P.BootstrapPeers, []string{"/ip4/1.2.3.4/tcp/1234/p2p/QmTest", "/dnsaddr/bootstrap.libp2p.io/p2p/QmFoo"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("cfg.P2P.BootstrapPeers = %#v, want %#v", got, want)
	}
	if cfg.Socks5.Listen != "127.0.0.1:1089" {
		t.Fatalf("cfg.Socks5.Listen = %q, want %q", cfg.Socks5.Listen, "127.0.0.1:1089")
	}
	if cfg.API.Listen != "127.0.0.1:19081" {
		t.Fatalf("cfg.API.Listen = %q, want %q", cfg.API.Listen, "127.0.0.1:19081")
	}
	if cfg.Client.ExitSelection.Mode != ExitSelectionFixedPeer {
		t.Fatalf("cfg.Client.ExitSelection.Mode = %q, want %q", cfg.Client.ExitSelection.Mode, ExitSelectionFixedPeer)
	}
	if cfg.Client.ExitSelection.FixedExitPeerID != "12D3KooWabc" {
		t.Fatalf("cfg.Client.ExitSelection.FixedExitPeerID = %q, want %q", cfg.Client.ExitSelection.FixedExitPeerID, "12D3KooWabc")
	}
	if cfg.Exit == nil {
		t.Fatal("cfg.Exit = nil, want non-nil")
	}
	if cfg.Exit.Enabled {
		t.Fatalf("cfg.Exit.Enabled = true, want false")
	}
	if cfg.Exit.Policy.AllowTCP {
		t.Fatalf("cfg.Exit.Policy.AllowTCP = true, want false")
	}
	if got, want := cfg.Exit.Policy.AllowedPorts, []int{80, 443}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("cfg.Exit.Policy.AllowedPorts = %#v, want %#v", got, want)
	}
	if cfg.Exit.Runtime.AcceptNewStreams {
		t.Fatalf("cfg.Exit.Runtime.AcceptNewStreams = true, want false")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRegisterFlagsAliasesRemainWorking(t *testing.T) {
	cfg := Default()

	fs := flag.NewFlagSet("meshproxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := RegisterFlags(fs, &cfg); err != nil {
		t.Fatalf("RegisterFlags() error = %v", err)
	}

	args := []string{
		"--mode=relay",
		"--socks5=127.0.0.1:1090",
		"--api=127.0.0.1:19090",
		"--nodisc=false",
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if cfg.Socks5.Listen != "127.0.0.1:1090" {
		t.Fatalf("cfg.Socks5.Listen = %q, want %q", cfg.Socks5.Listen, "127.0.0.1:1090")
	}
	if cfg.API.Listen != "127.0.0.1:19090" {
		t.Fatalf("cfg.API.Listen = %q, want %q", cfg.API.Listen, "127.0.0.1:19090")
	}
	if cfg.P2P.NoDiscovery {
		t.Fatalf("cfg.P2P.NoDiscovery = true, want false")
	}
}
