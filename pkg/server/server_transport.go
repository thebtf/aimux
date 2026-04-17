package server

import (
	"context"
	"crypto/subtle"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/mcp-mux/muxcore"
)

// ServeStdio starts the MCP server on stdio transport using os.Stdin/os.Stdout.
func (s *Server) ServeStdio() error {
	s.log.Info("MCP server starting on stdio (aimux v%s)", Version)
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

// StdioHandler returns a handler function compatible with muxcore engine.Handler.
// The handler wraps the MCP server's stdio transport, accepting custom stdin/stdout
// from the engine's IPC layer instead of hardcoded os.Stdin/os.Stdout.
func (s *Server) StdioHandler() func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	return func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
		s.log.Info("MCP server starting on engine stdio (aimux v%s)", Version)
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
	// Build the http.Server first so WithHTTPServer can be passed to the single
	// NewSSEServer call. The handler is set after construction to avoid a
	// chicken-and-egg reference, but the http.Server addr is configured upfront.
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
		if got == "" {
			log.Warn("auth: missing Authorization header from %s path=%s", r.RemoteAddr, r.URL.Path)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			log.Warn("auth: bearer token mismatch from %s path=%s", r.RemoteAddr, r.URL.Path)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
