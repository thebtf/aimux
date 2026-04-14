package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
	aimuxServer "github.com/thebtf/aimux/pkg/server"
	"github.com/thebtf/mcp-mux/muxcore/engine"
)

const version = "3.0.0-dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "aimux: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configDir := findConfigDir()

	cfg, err := config.Load(configDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

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
	log.Info("CLI discovery: %d available: %v", len(enabled), enabled)

	if len(enabled) == 0 {
		return fmt.Errorf("no CLI tools found — install at least one of: codex, gemini, claude, qwen, aider, droid, opencode")
	}

	// Initialize role router with capability profiles for fallback routing
	router := routing.NewRouterWithProfiles(cfg.Roles, enabled, cfg.CLIProfiles)

	// Create MCP server
	srv := aimuxServer.New(cfg, log, registry, router)
	defer srv.Shutdown()

	// Select transport: env var MCP_TRANSPORT overrides config
	transport := cfg.Server.Transport.Type
	if envTransport := os.Getenv("MCP_TRANSPORT"); envTransport != "" {
		transport = envTransport
	}

	port := cfg.Server.Transport.Port
	if port == "" {
		port = ":8080"
	}

	switch transport {
	case "sse":
		log.Info("aimux v%s ready — serving MCP on SSE at %s", version, port)
		return srv.ServeSSE(port)
	case "http", "streamablehttp":
		log.Info("aimux v%s ready — serving MCP on HTTP at %s", version, port)
		return srv.ServeHTTP(port)
	default:
		// Engine mode is DEFAULT for stdio transport.
		// Engine auto-detects: daemon flag → daemon mode, MCP_MUX_SESSION_ID → proxy mode,
		// otherwise → client/shim mode (spawn daemon, bridge stdio↔IPC transparently).
		// AIMUX_NO_ENGINE=1 bypasses for debugging.
		if os.Getenv("AIMUX_NO_ENGINE") == "1" {
			log.Info("aimux v%s ready — serving MCP on stdio (engine bypassed)", version)
			return srv.ServeStdio()
		}

		log.Info("aimux v%s ready — serving MCP via muxcore engine", version)
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		eng, engErr := engine.New(engine.Config{
			Name:           "aimux",
			SessionHandler: srv.SessionHandler(),
			Handler:        srv.StdioHandler(), // kept for proxy mode compatibility
			Persistent:     true,
		})
		if engErr != nil {
			return fmt.Errorf("engine init: %w", engErr)
		}
		if runErr := eng.Run(ctx); runErr != nil && !errors.Is(runErr, context.Canceled) {
			return fmt.Errorf("engine: %w", runErr)
		}
		return nil
	}
}

func findConfigDir() string {
	if dir := os.Getenv("AIMUX_CONFIG_DIR"); dir != "" {
		return dir
	}

	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Join(filepath.Dir(exe), "config")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}

	if info, err := os.Stat("config"); err == nil && info.IsDir() {
		return "config"
	}

	return "config"
}
