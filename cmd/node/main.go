package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/chenjia404/meshproxy/internal/app"
	"github.com/chenjia404/meshproxy/internal/config"
	"github.com/chenjia404/meshproxy/internal/safe"
)

func main() {
	defer safe.Recover("main")

	cfgPath := flag.String("config", "config.yaml", "path to config file (default: config.yaml in current directory)")
	modeFlag := flag.String("mode", "", "override node mode (relay or relay+exit)")
	socksAddrFlag := flag.String("socks5", "", "override local SOCKS5 listen address, e.g. 127.0.0.1:1080")
	apiAddrFlag := flag.String("api", "", "override local API listen address, e.g. 127.0.0.1:19080")
	noDiscFlag := flag.Bool("nodisc", false, "disable DHT rendezvous peer discovery")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}
	cfg.ConfigFilePath = *cfgPath

	// command-line overrides (for quick testing)
	if *modeFlag != "" {
		cfg.Mode = *modeFlag
	}
	if *socksAddrFlag != "" {
		cfg.Socks5.Listen = *socksAddrFlag
	}
	if *apiAddrFlag != "" {
		cfg.API.Listen = *apiAddrFlag
	}
	if *noDiscFlag {
		cfg.P2P.NoDiscovery = true
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config after overrides: %v", err)
	}
	if cleanup, err := setupCrashCapture(cfg.DataDir); err != nil {
		log.Printf("setup crash capture failed: %v", err)
	} else {
		defer cleanup()
	}

	application, err := app.New(ctx, cfg)
	if err != nil {
		log.Fatalf("init app failed: %v", err)
	}

	log.Printf("meshproxy starting in mode=%s peer_id=%s", application.Mode(), application.PeerID())

	if err := application.Run(); err != nil {
		// allow graceful shutdown errors to be reported but not as stack traces
		fmt.Fprintf(os.Stderr, "meshproxy exited with error: %v\n", err)
	}

	log.Printf("meshproxy stopped, uptime=%s", time.Since(application.StartTime()).String())
}

func setupCrashCapture(dataDir string) (func(), error) {
	if dataDir == "" {
		dataDir = "data"
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	crashPath := filepath.Join(dataDir, "crash.log")
	f, err := os.OpenFile(crashPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		return nil, err
	}
	log.Printf("crash output will be written to %s", crashPath)
	return func() {
		_ = f.Close()
	}, nil
}
