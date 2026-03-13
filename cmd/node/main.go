package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meshproxy/internal/app"
	"meshproxy/internal/config"
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "path to config file")
	modeFlag := flag.String("mode", "", "override node mode (relay or relay+exit)")
	socksAddrFlag := flag.String("socks5", "", "override local SOCKS5 listen address, e.g. 127.0.0.1:1080")
	apiAddrFlag := flag.String("api", "", "override local API listen address, e.g. 127.0.0.1:19080")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

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
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config after overrides: %v", err)
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

