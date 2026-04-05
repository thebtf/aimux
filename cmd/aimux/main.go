package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
)

const version = "3.0.0-dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "aimux: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Find config directory (embedded in binary or alongside it)
	configDir := findConfigDir()

	// Load configuration
	cfg, err := config.Load(configDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Initialize logger
	logPath := config.ExpandPath(cfg.Server.LogFile)
	log, err := logger.New(logPath, logger.ParseLevel(cfg.Server.LogLevel))
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer log.Close()

	log.Info("aimux v%s starting", version)

	// Discover CLIs
	registry := driver.NewRegistry(cfg.CLIProfiles)
	registry.Probe()

	enabled := registry.EnabledCLIs()
	log.Info("CLI discovery complete: %d available: %v", len(enabled), enabled)

	if len(enabled) == 0 {
		return fmt.Errorf("no CLI tools found — install at least one of: codex, gemini, claude, qwen, aider, droid, opencode")
	}

	// Initialize role router
	router := routing.NewRouter(cfg.Roles, enabled)
	_ = router // Will be used by MCP server in Phase 3

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.Info("received signal %v, shutting down", sig)
		cancel()
	}()

	// TODO: Phase 3 — start MCP server here
	log.Info("aimux v%s ready (MCP server not yet implemented)", version)

	// Wait for shutdown signal
	<-ctx.Done()
	log.Info("aimux shutdown complete")

	return nil
}

// findConfigDir locates the config directory.
// Priority: AIMUX_CONFIG_DIR env → ./config/ → embedded.
func findConfigDir() string {
	if dir := os.Getenv("AIMUX_CONFIG_DIR"); dir != "" {
		return dir
	}

	// Check relative to executable
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Join(filepath.Dir(exe), "config")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}

	// Check relative to working directory
	if info, err := os.Stat("config"); err == nil && info.IsDir() {
		return "config"
	}

	return "config"
}
