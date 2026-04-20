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

	log.Info("aimux v%s starting", aimuxServer.Version)

	// Discover CLIs
	registry := driver.NewRegistry(cfg.CLIProfiles)
	registry.Probe()

	enabled := registry.EnabledCLIs()
	log.Info("CLI discovery: %d available: %v", len(enabled), enabled)

	if len(enabled) == 0 {
		return fmt.Errorf("no CLI tools found — install at least one of: codex, gemini, claude, qwen, aider, droid, opencode")
	}

	// Warmup runs in the background — it must NOT block startup. Every
	// aimux.exe invocation enters this code path, including short-lived shim
	// processes that just bridge stdio↔IPC to an existing daemon. Blocking
	// 15s on warmup here meant every /mcp reconnect exceeded CC's 20s
	// handshake timeout and failed. The router is initialized from the
	// binary-only pool (all CLIs with a resolved binary, per Probe()) so
	// MCP becomes ready immediately; warmup updates registry availability
	// as probes complete in the background.
	afterWarmup := registry.EnabledCLIs()
	go func() {
		log.Info("running CLI warmup probes in background (AIMUX_WARMUP=false to skip)")
		if warmupErr := driver.RunWarmup(context.Background(), registry, cfg); warmupErr != nil {
			log.Warn("warmup error (non-fatal): %v", warmupErr)
		}
		after := registry.EnabledCLIs()
		log.Info("CLI warmup complete (background): %d available: %v", len(after), after)
		if len(after) == 0 {
			// All probes failed — restore binary-only pool so calls still route.
			// Root cause is usually an env issue (PATH, cold-start timeout) in
			// the spawned daemon/shim context, not genuine CLI breakage.
			log.Warn("all CLI probes failed — restoring binary-only pool (health-gate bypassed)")
			for _, name := range registry.ProbeableCLIs() {
				registry.SetAvailable(name, true)
			}
		}
	}()

	// Initialize role router with the binary-only CLI pool. Warmup's
	// availability updates propagate through the registry — the router
	// reads live availability on each dispatch.
	router := routing.NewRouterWithPriority(cfg.Roles, afterWarmup, cfg.CLIProfiles, cfg.Server.CLIPriority)

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
		log.Info("aimux v%s ready — serving MCP on SSE at %s", aimuxServer.Version, port)
		return srv.ServeSSE(port)
	case "http", "streamablehttp":
		log.Info("aimux v%s ready — serving MCP on HTTP at %s", aimuxServer.Version, port)
		return srv.ServeHTTP(port)
	default:
		// Engine mode is DEFAULT for stdio transport.
		// Engine auto-detects: daemon flag → daemon mode, MCP_MUX_SESSION_ID → proxy mode,
		// otherwise → client/shim mode (spawn daemon, bridge stdio↔IPC transparently).
		// AIMUX_NO_ENGINE=1 bypasses for debugging.
		if os.Getenv("AIMUX_NO_ENGINE") == "1" {
			log.Info("aimux v%s ready — serving MCP on stdio (engine bypassed)", aimuxServer.Version)
			return srv.ServeStdio()
		}

		// Engine name controls IPC socket discovery — different names = isolated
		// daemons. Override via AIMUX_ENGINE_NAME to run dev/prod binaries side by
		// side without version skew (e.g., aimux-dev binary in .mcp.json sets
		// AIMUX_ENGINE_NAME=aimux-dev to avoid colliding with stable aimux daemon).
		engineName := os.Getenv("AIMUX_ENGINE_NAME")
		if engineName == "" {
			engineName = "aimux"
		}

		log.Info("aimux v%s ready — serving MCP via muxcore engine (name=%s)", aimuxServer.Version, engineName)
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		eng, engErr := engine.New(engine.Config{
			Name:           engineName,
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
