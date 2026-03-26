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
	"strings"
	"syscall"
	"time"

	"github.com/chenjia404/meshproxy/internal/logfilter"
	"github.com/chenjia404/meshproxy/internal/safe"
	"github.com/chenjia404/meshproxy/sdk"
)

func main() {
	defer safe.Recover("main")

	cfgPath := findConfigPath(os.Args[1:], "config.yaml")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := sdk.LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}
	baseDataDir := cfg.DataDir
	baseIdentityKeyPath := cfg.IdentityKeyPath

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	cfgFileFlag := fs.String("config", cfgPath, "path to config file (default: config.yaml in current directory)")
	if err := sdk.RegisterFlags(fs, &cfg); err != nil {
		log.Fatalf("register flags failed: %v", err)
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		log.Fatalf("parse flags failed: %v", err)
	}
	if flagWasSet(fs, "data_dir") && !flagWasSet(fs, "identity_key_path") && baseIdentityKeyPath == filepath.Join(baseDataDir, "identity.key") {
		cfg.IdentityKeyPath = filepath.Join(cfg.DataDir, "identity.key")
	}
	if err := cfg.Normalize(); err != nil {
		log.Fatalf("normalize config failed: %v", err)
	}
	cfg.ConfigFilePath = *cfgFileFlag
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config after overrides: %v", err)
	}
	logfilter.Apply(cfg.LogModules)
	if cleanup, err := setupCrashCapture(cfg.DataDir); err != nil {
		log.Printf("setup crash capture failed: %v", err)
	} else {
		defer cleanup()
	}

	application, err := sdk.New(ctx, cfg, sdk.Options{
		EnableSOCKS5:   true,
		EnableLocalAPI: true,
	})
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

func flagWasSet(fs *flag.FlagSet, name string) bool {
	wasSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func findConfigPath(args []string, fallback string) string {
	path := fallback
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-config" || arg == "--config":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "-config="):
			path = strings.TrimPrefix(arg, "-config=")
		case strings.HasPrefix(arg, "--config="):
			path = strings.TrimPrefix(arg, "--config=")
		}
	}
	return path
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
