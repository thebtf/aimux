package executor

import (
	"context"

	"github.com/thebtf/aimux/pkg/types"
)

// Compile-time assertion: CLIPTYAdapter must implement ExecutorV2.
var _ types.ExecutorV2 = (*CLIPTYAdapter)(nil)

// CLIPTYAdapter wraps a legacy types.LegacyExecutor (typically *pty.Executor) as
// an ExecutorV2. It accepts the interface rather than the concrete type to avoid
// an import cycle: pkg/executor/pty imports pkg/executor for IOManager.
//
// When session is non-nil the adapter operates in session-bound mode:
// Send() dispatches via session.Send() instead of legacy.Run() (FR-2, AIMUX-14).
//
// Capabilities:
//   - Streaming: false — output is collected and returned in one Response.
//   - PersistentSessions: true — pty.Start() implements multi-turn sessions (M6).
//
// Thread safety: CLIPTYAdapter is safe for concurrent use; the underlying
// executor spawns an independent process per Run() call when stateless.
type CLIPTYAdapter struct {
	legacy  types.LegacyExecutor
	session types.Session // nil when stateless (backward-compat default)
}

// NewCLIPTYAdapter creates a CLIPTYAdapter wrapping the given legacy executor.
// Accepts types.LegacyExecutor to avoid the import cycle; pass *pty.Executor directly.
// The adapter operates in stateless mode (session == nil); existing callers are unaffected.
func NewCLIPTYAdapter(legacy types.LegacyExecutor) *CLIPTYAdapter {
	return &CLIPTYAdapter{legacy: legacy}
}

// NewCLIPTYAdapterWithSession creates a CLIPTYAdapter bound to an existing Session.
// Send() dispatches via session.Send() (session-bound mode, FR-2).
// Existing stateless Send() path via legacy.Run() is preserved byte-identically when
// session is nil — Stateless SpawnMode immutability invariant (AIMUX-13 FR-1).
func NewCLIPTYAdapterWithSession(legacy types.LegacyExecutor, sess types.Session) *CLIPTYAdapter {
	return &CLIPTYAdapter{legacy: legacy, session: sess}
}

// Info returns static metadata for the PTY executor adapter.
func (a *CLIPTYAdapter) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "pty",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			Streaming:          false,
			PersistentSessions: true,
		},
	}
}

// Send dispatches the message via session.Send() when session-bound, or via the
// legacy Run() path when stateless (Stateless SpawnMode preserved byte-identically,
// AIMUX-13 FR-1 immutability invariant).
//
// Recognised Metadata keys (via messageToSpawnArgs, stateless path only):
//   - "command" (string)   — executable path/name
//   - "args"    ([]string) — command-line arguments
//   - "cwd"     (string)   — working directory
//   - "timeout" (int/int64/float64) — timeout in seconds
//   - "env"     (map[string]string or map[string]any) — extra env vars
//   - "stdin"   (string)   — override stdin (default: msg.Content)
//   - "completion_pattern" (string) — regex to detect process completion
func (a *CLIPTYAdapter) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	// Session-bound mode (FR-2): dispatch via persistent session.
	if a.session != nil {
		result, err := a.session.Send(ctx, msg.Content)
		if err != nil {
			return nil, err
		}
		return resultToResponse(result), nil
	}

	// Stateless path — byte-identical to original (AIMUX-13 FR-1).
	args := messageToSpawnArgs(msg)

	result, err := a.legacy.Run(ctx, args)
	if err != nil {
		return nil, err
	}
	return resultToResponse(result), nil
}

// SendStream calls Send and delivers the complete response as a single chunk
// with Done=true. PTY executor does not support incremental streaming.
func (a *CLIPTYAdapter) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	resp, err := a.Send(ctx, msg)
	if err != nil {
		return nil, err
	}
	onChunk(types.Chunk{Content: resp.Content, Done: true})
	return resp, nil
}

// IsAlive returns HealthAlive when the adapter is session-bound and the session
// process is alive, or when the stateless PTY executor reports itself as available.
func (a *CLIPTYAdapter) IsAlive() types.HealthStatus {
	if a.session != nil {
		if a.session.Alive() {
			return types.HealthAlive
		}
		return types.HealthDead
	}
	if a.legacy.Available() {
		return types.HealthAlive
	}
	return types.HealthDead
}

// Close terminates the bound session when session-bound, or is a no-op when stateless
// (the PTY executor spawns a new process per Run() call and holds no persistent resources).
func (a *CLIPTYAdapter) Close() error {
	if a.session != nil {
		return a.session.Close()
	}
	return nil
}

// Legacy returns the underlying LegacyExecutor for Strangler Fig bridging.
func (a *CLIPTYAdapter) Legacy() types.LegacyExecutor {
	return a.legacy
}
