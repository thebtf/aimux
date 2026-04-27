package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/thebtf/aimux/pkg/build"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/logger"
	aimuxServer "github.com/thebtf/aimux/pkg/server"
	"github.com/thebtf/mcp-mux/muxcore"
	"github.com/thebtf/mcp-mux/muxcore/engine"
	"github.com/thebtf/mcp-mux/muxcore/owner"
)

// shimErrMsg is the verbatim JSON-RPC error message returned by stubSessionHandler.
// Verbatim per spec FR-6 C6 decision — do not paraphrase.
const shimErrMsg = "shim mode is not expected to serve MCP requests; this is either a CC misconfiguration or a muxcore regression — see aimux.log for details"

// runShim constructs and runs the muxcore engine in shim/client mode.
// Shim mode bridges stdio<->IPC to an existing daemon without performing
// any daemon-level initialization (no SQLite, no LoomEngine, no warmup).
//
// AIMUX_ENGINE_NAME controls IPC socket discovery via pkg/server.ResolveEngineName,
// preserving dev/prod daemon isolation (PR #71).
func runShim(ctx context.Context, cfg *config.Config, log *logger.Logger) error {
	// Engine name controls IPC socket discovery — different names = isolated daemons.
	// Shared resolution logic with cmd/aimux/main.go avoids drift in daemon naming.
	engineName := aimuxServer.ResolveEngineName()

	log.Info("aimux v%s shim ready (name=%s)", build.Version, engineName)

	exePath, exeErr := os.Executable()
	if exeErr != nil {
		return fmt.Errorf("resolve executable: %w", exeErr)
	}
	hadDirectUpstream := false
	previousDirectUpstream := os.Getenv("AIMUX_DIRECT_UPSTREAM")
	if previousDirectUpstream != "" {
		hadDirectUpstream = true
	}
	if err := os.Setenv("AIMUX_DIRECT_UPSTREAM", "1"); err != nil {
		return fmt.Errorf("set AIMUX_DIRECT_UPSTREAM: %w", err)
	}
	defer func() {
		if hadDirectUpstream {
			_ = os.Setenv("AIMUX_DIRECT_UPSTREAM", previousDirectUpstream)
			return
		}
		_ = os.Unsetenv("AIMUX_DIRECT_UPSTREAM")
	}()

	eng, engErr := engine.New(engine.Config{
		Name:           engineName,
		Command:        exePath,
		Args:           []string{},
		DaemonFlag:     daemonFlagValue(),
		Persistent:     true,
		SessionHandler: &stubSessionHandler{log: log},
		StdinEOFPolicy: owner.StdinEOFWaitForDisconnect,
		Logger:         log.StdLogger(),
	})
	if engErr != nil {
		return fmt.Errorf("shim engine init: %w", engErr)
	}
	if runErr := eng.Run(ctx); runErr != nil && !errors.Is(runErr, context.Canceled) {
		return fmt.Errorf("shim engine: %w", runErr)
	}
	return nil
}

// stubSessionHandler is a defence-in-depth stub for shim-mode engine.New.
// muxcore requires at least one of {Command, Handler, SessionHandler} to be non-nil.
// In normal shim operation (engine.runClient path) this handler is NEVER invoked —
// runClient is a pure stdio<->IPC bridge. The stub guards against a hypothetical
// future muxcore regression that starts dispatching to the handler in client mode.
//
// Contract (spec FR-6 C6):
//   - Returns JSON-RPC INTERNAL_ERROR -32603 with verbatim shimErrMsg
//   - Echoes the incoming request's JSON-RPC id in the response envelope
//   - sync.Once guards log.Error to prevent log flood on repeated invocations
type stubSessionHandler struct {
	logOnce sync.Once
	log     *logger.Logger
}

// HandleRequest implements muxcore.SessionHandler.
// Returns INTERNAL_ERROR -32603 unconditionally; this method should never be called
// in normal shim operation. See struct doc for rationale.
func (s *stubSessionHandler) HandleRequest(ctx context.Context, project muxcore.ProjectContext, request []byte) ([]byte, error) {
	// Parse id and method from incoming request to echo id and log method.
	var req struct {
		ID     mcp.RequestId `json:"id"`
		Method string        `json:"method"`
	}
	_ = json.Unmarshal(request, &req)

	// Log once to prevent flood if a muxcore regression dispatches every request here.
	s.logOnce.Do(func() {
		s.log.Error("stubSessionHandler.HandleRequest invoked — this should never happen in shim mode; method=%s id=%s stack=%s",
			req.Method, req.ID.String(), debug.Stack())
	})

	errResp := mcp.NewJSONRPCError(req.ID, mcp.INTERNAL_ERROR, shimErrMsg, nil)
	return json.Marshal(errResp)
}
