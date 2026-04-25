package server

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/upgrade"
	"github.com/thebtf/mcp-mux/muxcore"
	muxdaemon "github.com/thebtf/mcp-mux/muxcore/daemon"
	"github.com/thebtf/mcp-mux/muxcore/engine"
)

// ServeStdio starts the MCP server on stdio transport using os.Stdin/os.Stdout.
func (s *Server) ServeStdio() error {
	s.log.Info("MCP server starting on stdio (aimux v%s)", Version)
	s.configureMuxCompatibility()
	return server.ServeStdio(s.mcp)
}

// SessionHandler returns a muxcore.SessionHandler that dispatches MCP requests
// via MCPServer.HandleMessage with per-project session isolation.
// Used by muxcore engine daemon mode for direct JSON-RPC dispatch.
func (s *Server) SessionHandler() muxcore.SessionHandler {
	h := &aimuxHandler{srv: s}
	s.sessionHandler = h
	return h
}

// SetDaemonControlSocketPath stores the live muxcore daemon control socket path.
// Engine-mode upgrade uses this explicit seam to request daemon-side graceful restart.
func (s *Server) SetDaemonControlSocketPath(socketPath string) {
	s.daemonControlSocketPath = socketPath
}

// SetMuxEngine stores the live muxcore engine so daemon-mode paths can access
// the in-process daemon directly instead of routing through the control socket.
func (s *Server) SetMuxEngine(eng *engine.MuxEngine) {
	s.muxEngine = eng
}

func (s *Server) gracefulRestartFunc() upgrade.GracefulRestartFunc {
	if d := s.liveDaemon(); d != nil {
		return func(ctx context.Context, drainTimeoutMs int) error {
			_, afterFn, err := d.HandleGracefulRestart(drainTimeoutMs)
			if afterFn != nil {
				afterFn()
			}
			return err
		}
	}
	return upgrade.NewControlSocketGracefulRestartFunc(s.daemonControlSocketPath)
}

func (s *Server) handoffStatusFunc() upgrade.HandoffStatusFunc {
	if d := s.liveDaemon(); d != nil {
		return func(ctx context.Context) (upgrade.HandoffStatus, error) {
			return readDaemonHandoffStatus(d)
		}
	}
	return upgrade.NewControlSocketHandoffStatusFunc(s.daemonControlSocketPath)
}

func (s *Server) liveDaemon() *muxdaemon.Daemon {
	if s == nil || s.muxEngine == nil {
		return nil
	}
	if s.muxEngine.Mode() != engine.ModeDaemon {
		return nil
	}
	return s.muxEngine.Daemon()
}

func readDaemonHandoffStatus(d *muxdaemon.Daemon) (upgrade.HandoffStatus, error) {
	if d == nil {
		return upgrade.HandoffStatus{}, fmt.Errorf("nil daemon")
	}
	status := d.HandleStatus()
	handoffRaw, ok := status["handoff"]
	if !ok {
		return upgrade.HandoffStatus{}, fmt.Errorf("daemon status missing handoff counters")
	}
	handoffMap, ok := handoffRaw.(map[string]any)
	if !ok {
		return upgrade.HandoffStatus{}, fmt.Errorf("daemon handoff counters malformed")
	}
	fallbackValue, ok := handoffMap["fallback"]
	if !ok {
		return upgrade.HandoffStatus{}, fmt.Errorf("daemon handoff counters missing fallback")
	}
	var fallback uint64
	switch v := fallbackValue.(type) {
	case uint64:
		fallback = v
	case float64:
		fallback = uint64(v)
	case int:
		fallback = uint64(v)
	case int64:
		fallback = uint64(v)
	default:
		return upgrade.HandoffStatus{}, fmt.Errorf("daemon handoff fallback counter has unexpected type %T", fallbackValue)
	}
	return upgrade.HandoffStatus{Fallback: fallback}, nil
}

func (s *Server) configureMuxCompatibility() {
	if s.mcp == nil {
		return
	}
	hooks := s.mcp.GetHooks()
	if hooks == nil {
		return
	}
	hooks.AddAfterInitialize(func(ctx context.Context, id any, message *mcp.InitializeRequest, result *mcp.InitializeResult) {
		if result.Capabilities.Experimental == nil {
			result.Capabilities.Experimental = make(map[string]any)
		}
		result.Capabilities.Experimental["x-mux"] = map[string]any{
			"sharing":    "session-aware",
			"persistent": true,
		}
	})
}

// StdioHandler returns a handler function compatible with muxcore engine.Handler.
// The handler wraps the MCP server's stdio transport, accepting custom stdin/stdout
// from the engine's IPC layer instead of hardcoded os.Stdin/os.Stdout.
func (s *Server) StdioHandler() func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	return func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
		s.log.Info("MCP server starting on engine stdio (aimux v%s)", Version)
		s.configureMuxCompatibility()
		stdioSrv := server.NewStdioServer(s.mcp)
		return stdioSrv.Listen(ctx, stdin, stdout)
	}
}

// ServeSSE starts the MCP server with Server-Sent Events transport.
// If authToken is configured, all requests must carry a valid Bearer token.
func (s *Server) ServeSSE(addr string) error {
	addr = ensureLocalhostBinding(addr)
	s.log.Info("MCP server starting on SSE at %s (aimux v%s)", addr, Version)
	if !isLocalhostAddr(addr) {
		s.log.Warn("SSE transport bound to non-localhost address %s", addr)
	}
	if s.authToken == "" {
		return server.NewSSEServer(s.mcp).Start(addr)
	}
	s.log.Info("SSE transport: bearer token authentication enabled")
	httpSrv := &http.Server{
		Addr:         addr,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	sseServer := server.NewSSEServer(s.mcp, server.WithHTTPServer(httpSrv))
	httpSrv.Handler = bearerAuthMiddleware(s.authToken, s.log, sseServer)
	return sseServer.Start(addr)
}

// ServeHTTP starts the MCP server with StreamableHTTP transport.
// If authToken is configured, all requests must carry a valid Bearer token.
func (s *Server) ServeHTTP(addr string, opts ...server.StreamableHTTPOption) error {
	addr = ensureLocalhostBinding(addr)
	s.log.Info("MCP server starting on HTTP at %s (aimux v%s)", addr, Version)
	if !isLocalhostAddr(addr) {
		s.log.Warn("HTTP transport bound to non-localhost address %s", addr)
	}
	if s.authToken == "" {
		return server.NewStreamableHTTPServer(s.mcp, opts...).Start(addr)
	}
	s.log.Info("HTTP transport: bearer token authentication enabled")
	httpSrv := &http.Server{
		Addr:         addr,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	allOpts := append(opts, server.WithStreamableHTTPServer(httpSrv))
	httpMCPServer := server.NewStreamableHTTPServer(s.mcp, allOpts...)
	httpSrv.Handler = bearerAuthMiddleware(s.authToken, s.log, httpMCPServer)
	return httpMCPServer.Start(addr)
}

// ensureLocalhostBinding rewrites bare port specs and 0.0.0.0 to 127.0.0.1 to
// prevent accidental exposure on all interfaces.
//   - ":8080"         → "127.0.0.1:8080"
//   - "0.0.0.0:8080"  → "127.0.0.1:8080"
func ensureLocalhostBinding(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "127.0.0.1" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "127.0.0.1" + addr[len("0.0.0.0"):]
	}
	return addr
}

// isLocalhostAddr checks if the address is bound to localhost/127.0.0.1.
func isLocalhostAddr(addr string) bool {
	return strings.HasPrefix(addr, "127.0.0.1") || strings.HasPrefix(addr, "localhost") || strings.HasPrefix(addr, "[::1]")
}

// bearerAuthMiddleware returns an http.Handler that enforces Bearer token authentication.
// Requests missing or presenting an incorrect token receive 401 Unauthorized.
// When token is empty the original handler is returned unchanged (backward-compatible).
// Auth failures are logged at WARN level via the provided logger.
func bearerAuthMiddleware(token string, log *logger.Logger, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	expected := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if len(got) != len(expected) || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			if log != nil {
				log.Warn("auth: unauthorized request path=%s remote=%s", r.URL.Path, r.RemoteAddr)
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="aimux"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
