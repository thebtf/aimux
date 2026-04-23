package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
	aimuxServer "github.com/thebtf/aimux/pkg/server"
	muxdaemon "github.com/thebtf/mcp-mux/muxcore/daemon"
	"github.com/thebtf/mcp-mux/muxcore/engine"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "aimux: %v\n", err)
		var exitErr *exitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		os.Exit(1)
	}
}

func run() error {
	handoff, err := parseHandoffFlags(os.Args[1:])
	if err != nil {
		return err
	}

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

	// NEW: mode detection before any heavy init (T005, AIMUX-6).
	// detectMode mirrors muxcore's own isDaemonMode logic so daemon/shim agree.
	// Returns error on MCP_MUX_SESSION_ID (proxy rejection per FR-4).
	// Note: main() already prints returned errors with "aimux: %v" prefix to stderr,
	// so we do NOT print here — avoid double-stderr (G002 LOW-1).
	mode, modeErr := detectMode(os.Args, os.Getenv)
	if modeErr != nil {
		return modeErr
	}

	// FR-5 postmortem complement to the stderr notice in detectMode: emit a
	// single warning into aimux.log once the logger is available, so deprecated
	// env-var usage is captured even when stderr is discarded (G002 LOW-2).
	if os.Getenv("AIMUX_NO_ENGINE") == "1" {
		log.Warn("aimux: AIMUX_NO_ENGINE=1 is deprecated and ignored; aimux always runs via muxcore engine (daemon or shim mode)")
	}

	// NEW: hoist ctx creation so both branches share it.
	// Shim branch passes ctx to runShim; daemon branch passes ctx to engine.Run.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if handoff.From != "" {
		mode = ModeDaemon
	}

	// FR-8: emit audit log line naming the detected mode and signal before any
	// mode-specific branch executes. Enables postmortem correlation with the
	// "aimux v<version> starting" line — first two log lines identify the path taken.
	modeSignal := "default"
	switch {
	case handoff.From != "":
		modeSignal = "handoff"
	case mode == ModeDaemon:
		modeSignal = "arg"
	}
	modeName := "shim"
	if mode == ModeDaemon {
		modeName = "daemon"
	}
	log.Info("aimux v%s mode=%s signal=%s", aimuxServer.Version, modeName, modeSignal)

	if handoff.From != "" {
		cleanupHandoff, handoffErr := bootstrapSuccessorHandoff(ctx, log, handoff)
		if handoffErr != nil {
			return handoffErr
		}
		defer cleanupHandoff()
	}

	// NEW: shim branch — return directly without any heavy init.
	if mode == ModeShim {
		return runShim(ctx, cfg, log)
	}

	// DAEMON BRANCH (existing code below stays; T006-T009 edit it)

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
	srv := aimuxServer.NewDaemon(cfg, log, registry, router)
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
		// Engine mode is the only path for stdio transport (AIMUX-6 / FR-5).
		// Shim invocations are already short-circuited above; this branch is the
		// daemon-side engine run. AIMUX_NO_ENGINE=1 is deprecated and ignored —
		// detectMode emitted the deprecation notice before we got here (FR-5).
		// MCP_MUX_SESSION_ID (proxy) is rejected by detectMode per FR-4.
		// Engine name controls IPC socket discovery — different names = isolated
		// daemons. Override via AIMUX_ENGINE_NAME to run dev/prod binaries side by
		// side without version skew (e.g., aimux-dev binary in .mcp.json sets
		// AIMUX_ENGINE_NAME=aimux-dev to avoid colliding with stable aimux daemon).
		engineName := os.Getenv("AIMUX_ENGINE_NAME")
		if engineName == "" {
			engineName = "aimux"
		}

		log.Info("aimux v%s ready — serving MCP via muxcore engine (name=%s)", aimuxServer.Version, engineName)
		eng, engErr := engine.New(engine.Config{
			Name:           engineName,
			SessionHandler: srv.SessionHandler(),
			Handler:        srv.StdioHandler(), // kept for proxy mode compatibility
			Persistent:     true,
		})
		if engErr != nil {
			return fmt.Errorf("engine init: %w", engErr)
		}
		srv.SetDaemonControlSocketPath(eng.ControlSocketPath())
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

type handoffFlags struct {
	From  string
	Token string
}

func parseHandoffFlags(args []string) (handoffFlags, error) {
	filteredArgs, err := extractHandoffFlagArgs(args)
	if err != nil {
		return handoffFlags{}, err
	}

	fs := flag.NewFlagSet("aimux", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var handoff handoffFlags
	fs.StringVar(&handoff.From, "handoff-from", "", "existing muxcore socket path to hand off from")
	fs.StringVar(&handoff.Token, "handoff-token", "", "64-character hex token authorizing successor handoff")

	if err := fs.Parse(filteredArgs); err != nil {
		return handoffFlags{}, fmt.Errorf("parse handoff flags: %w", err)
	}
	if err := validateHandoffFlags(handoff); err != nil {
		return handoffFlags{}, err
	}

	return handoff, nil
}

func extractHandoffFlagArgs(args []string) ([]string, error) {
	filtered := make([]string, 0, 4)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--handoff-from", "--handoff-token":
			filtered = append(filtered, args[i])
			if i+1 >= len(args) {
				continue
			}
			filtered = append(filtered, args[i+1])
			i++
		default:
			if len(args[i]) >= len("--handoff-from=") && args[i][:len("--handoff-from=")] == "--handoff-from=" {
				filtered = append(filtered, args[i])
				continue
			}
			if len(args[i]) >= len("--handoff-token=") && args[i][:len("--handoff-token=")] == "--handoff-token=" {
				filtered = append(filtered, args[i])
			}
		}
	}
	return filtered, nil
}

func validateHandoffFlags(handoff handoffFlags) error {
	if (handoff.From == "") != (handoff.Token == "") {
		return fmt.Errorf("--handoff-from and --handoff-token must both be set")
	}
	if handoff.From == "" {
		return nil
	}

	if len(handoff.Token) != 64 {
		return fmt.Errorf("--handoff-token must be 64 hex characters")
	}
	if _, err := hex.DecodeString(handoff.Token); err != nil {
		return fmt.Errorf("--handoff-token must be 64 hex characters: %w", err)
	}

	info, err := os.Stat(handoff.From)
	if err != nil {
		return fmt.Errorf("--handoff-from path must exist: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("--handoff-from path must not be a directory")
	}

	return nil
}

type exitCodeError struct {
	Code int
	Err  error
}

func (e *exitCodeError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *exitCodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type handoffRelay struct {
	tokenPath string
	socketPath string
	listener  net.Listener
	done      chan error
}

func bootstrapSuccessorHandoff(ctx context.Context, log *logger.Logger, handoff handoffFlags) (func(), error) {
	upstreams, err := receivePredecessorHandoff(ctx, handoff)
	if err != nil {
		log.Error("handoff bootstrap failed: %v", err)
		return nil, &exitCodeError{Code: 2, Err: err}
	}

	relay, err := startLocalHandoffRelay(ctx, handoff.Token, upstreams)
	if err != nil {
		closeHandoffUpstreams(upstreams)
		return nil, fmt.Errorf("start local handoff relay: %w", err)
	}

	if err := os.Setenv("MCPMUX_HANDOFF_TOKEN_PATH", relay.tokenPath); err != nil {
		relay.cleanup(log)
		closeHandoffUpstreams(upstreams)
		return nil, fmt.Errorf("set MCPMUX_HANDOFF_TOKEN_PATH: %w", err)
	}
	if err := os.Setenv("MCPMUX_HANDOFF_SOCKET", relay.socketPath); err != nil {
		relay.cleanup(log)
		closeHandoffUpstreams(upstreams)
		return nil, fmt.Errorf("set MCPMUX_HANDOFF_SOCKET: %w", err)
	}

	log.Info("handoff bootstrap ready: predecessor=%s relay=%s upstreams=%d", handoff.From, relay.socketPath, len(upstreams))
	return func() {
		relay.cleanup(log)
		closeHandoffUpstreams(upstreams)
	}, nil
}

func receivePredecessorHandoff(ctx context.Context, handoff handoffFlags) ([]muxdaemon.HandoffUpstream, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := dialPlatformHandoffConn(dialCtx, handoff.From)
	if err != nil {
		return nil, fmt.Errorf("connect to predecessor %q: %w", handoff.From, err)
	}
	defer conn.Close()

	upstreams, err := muxdaemon.ReceiveHandoff(dialCtx, conn, handoff.Token)
	if err != nil {
		if errors.Is(err, muxdaemon.ErrTokenMismatch) || errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("handoff token mismatch or authentication rejected by predecessor")
		}
		return nil, fmt.Errorf("receive handoff from predecessor: %w", err)
	}
	return upstreams, nil
}

func startLocalHandoffRelay(ctx context.Context, token string, upstreams []muxdaemon.HandoffUpstream) (*handoffRelay, error) {
	tokenPath, err := writeSuccessorHandoffTokenFile(token)
	if err != nil {
		return nil, fmt.Errorf("prepare handoff token: %w", err)
	}

	listener, socketPath, err := listenPlatformHandoffRelay()
	if err != nil {
		_ = muxdaemon.DeleteHandoffToken(tokenPath)
		return nil, err
	}

	relay := &handoffRelay{
		tokenPath:  tokenPath,
		socketPath: socketPath,
		listener:   listener,
		done:       make(chan error, 1),
	}

	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			relay.done <- fmt.Errorf("accept local handoff relay: %w", acceptErr)
			return
		}
		defer conn.Close()

		_, performErr := muxdaemon.PerformHandoff(ctx, conn, token, upstreams)
		relay.done <- performErr
	}()

	return relay, nil
}

func writeSuccessorHandoffTokenFile(token string) (string, error) {
	file, err := os.CreateTemp("", "aimux-handoff-*.tok")
	if err != nil {
		return "", fmt.Errorf("create temp token file: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temp token file: %w", err)
	}
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write temp token file: %w", err)
	}
	return path, nil
}

func closeHandoffUpstreams(upstreams []muxdaemon.HandoffUpstream) {
	for _, upstream := range upstreams {
		if upstream.StdinFD > 0 {
			_ = os.NewFile(upstream.StdinFD, "").Close()
		}
		if upstream.StdoutFD > 0 {
			_ = os.NewFile(upstream.StdoutFD, "").Close()
		}
	}
}

func (r *handoffRelay) cleanup(log *logger.Logger) {
	if r == nil {
		return
	}
	if r.listener != nil {
		_ = r.listener.Close()
	}
	select {
	case err := <-r.done:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			log.Warn("handoff relay finished with error: %v", err)
		}
	default:
	}
	if err := muxdaemon.DeleteHandoffToken(r.tokenPath); err != nil {
		log.Warn("handoff token cleanup failed: %v", err)
	}
	_ = removePlatformHandoffRelay(r.socketPath)
}
