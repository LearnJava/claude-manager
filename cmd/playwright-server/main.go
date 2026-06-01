// cmd/playwright-server is a headless control-plane server for Playwright tests.
// It loads a TOML config, wires up a SessionManager and control.Server, and
// prints "CM_CONTROL_TOKEN=<hex>" to stdout so the caller can read it.
// Exits cleanly on SIGINT/SIGTERM.
//
// Usage:
//
//	playwright-server -config testdata/configs/playwright.toml -port 7334 \
//	                  -scenarios testdata/scenarios
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"claude-manager/internal/config"
	"claude-manager/internal/control"
	"claude-manager/internal/session"
)

func main() {
	cfgPath := flag.String("config", "", "path to TOML config (required)")
	port := flag.String("port", "7334", "control-plane listen port")
	scenarios := flag.String("scenarios", "", "path to fakeclaude scenarios directory (sets FAKECLAUDE_SCENARIO)")
	flag.Parse()

	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "playwright-server: -config is required")
		os.Exit(1)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "playwright-server: load config: %v\n", err)
		os.Exit(1)
	}

	// Override settings for deterministic, fast tests.
	cfg.Settings.DefaultRetryDelay = 1
	cfg.Settings.RateLimitPause = 1
	cfg.Settings.CrashRecovery = false
	cfg.Settings.SessionStartDelay = 0

	if *scenarios != "" {
		if err := os.Setenv("FAKECLAUDE_SCENARIO", *scenarios); err != nil {
			fmt.Fprintf(os.Stderr, "playwright-server: setenv: %v\n", err)
			os.Exit(1)
		}
	}

	token := control.GenerateToken()
	// Print the token before listening — the parent process reads stdout.
	fmt.Printf("CM_CONTROL_TOKEN=%s\n", token)

	ce := control.NewControlEmitter(500)
	mgr := session.NewSessionManager(cfg, *cfgPath, nil, ce)
	app := &configApp{cfg: cfg, cfgPath: *cfgPath}
	srv := control.NewServer(mgr, app, ce, token)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Start(ctx, *port); err != nil {
		fmt.Fprintf(os.Stderr, "playwright-server: %v\n", err)
		os.Exit(1)
	}
}

// configApp implements control.AppAPI backed by the in-memory config + file save.
type configApp struct {
	cfg     *config.AppConfig
	cfgPath string
}

func (a *configApp) GetConfig() *config.AppConfig { return a.cfg }

func (a *configApp) UpdateConfig(cfg config.AppConfig) error {
	a.cfg = &cfg
	return config.Save(&cfg, a.cfgPath)
}
