// Package executor — CLIConPTYAdapter wraps the legacy conpty.Executor as ExecutorV2.
package executor

import (
	"context"
	"fmt"

	"github.com/thebtf/aimux/pkg/types"
)

// Compile-time interface check.
var _ types.ExecutorV2 = (*CLIConPTYAdapter)(nil)

// CLIConPTYAdapter adapts the legacy types.LegacyExecutor (typically *conpty.Executor)
// to the ExecutorV2 interface. It accepts the interface to avoid an import cycle:
// pkg/executor/conpty imports pkg/executor for IOManager.
//
// When session is non-nil the adapter operates in session-bound mode:
// Send() dispatches via session.Send() instead of legacy.Run() (FR-2, AIMUX-14).
// When session is nil the adapter is stateless — each Send call spawns a fresh process.
type CLIConPTYAdapter struct {
	legacy  types.LegacyExecutor
	session types.Session // nil when stateless (backward-compat default)
}

// NewCLIConPTYAdapter creates a new CLIConPTYAdapter wrapping the given legacy executor.
// Accepts types.LegacyExecutor to avoid the import cycle; pass *conpty.Executor directly.
// The adapter operates in stateless mode (session == nil); existing callers are unaffected.
func NewCLIConPTYAdapter(legacy types.LegacyExecutor) *CLIConPTYAdapter {
	return &CLIConPTYAdapter{legacy: legacy}
}

// NewCLIConPTYAdapterWithSession creates a CLIConPTYAdapter bound to an existing Session.
// Send() dispatches via session.Send() (session-bound mode, FR-2).
// Existing stateless Send() path via legacy.Run() is preserved byte-identically when
// session is nil — Stateless SpawnMode immutability invariant (AIMUX-13 FR-1).
func NewCLIConPTYAdapterWithSession(legacy types.LegacyExecutor, sess types.Session) *CLIConPTYAdapter {
	return &CLIConPTYAdapter{legacy: legacy, session: sess}
}

// Info returns metadata describing this executor.
func (a *CLIConPTYAdapter) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "conpty",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			PersistentSessions: true,
			Streaming:          false,
		},
	}
}

// Send dispatches the message via session.Send() when session-bound, or via the
// legacy Run() path when stateless (Stateless SpawnMode preserved byte-identically,
// AIMUX-13 FR-1 immutability invariant). Metadata key handling and SystemPrompt
// prepending are delegated to messageToSpawnArgs (see adapter_common.go).
func (a *CLIConPTYAdapter) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	// Session-bound mode (FR-2): dispatch via persistent session.
	if a.session != nil {
		result, err := a.session.Send(ctx, msg.Content)
		if err != nil {
			return nil, fmt.Errorf("conpty adapter: %w", err)
		}
		return resultToResponse(result), nil
	}

	// Stateless path — byte-identical to original (AIMUX-13 FR-1).
	spawnArgs := messageToSpawnArgs(msg)

	result, err := a.legacy.Run(ctx, spawnArgs)
	if err != nil {
		return nil, fmt.Errorf("conpty adapter: %w", err)
	}

	return resultToResponse(result), nil
}

// SendStream calls Send and then invokes onChunk once with Done=true.
// ConPTY does not support incremental streaming; the full response is buffered
// by the underlying executor. Streaming will be added in M6.
func (a *CLIConPTYAdapter) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	resp, err := a.Send(ctx, msg)
	if err != nil {
		return nil, err
	}
	onChunk(types.Chunk{Content: resp.Content, Done: true})
	return resp, nil
}

// IsAlive returns HealthAlive when the adapter is session-bound and the session
// process is alive, or when the stateless conpty executor is available on this platform.
func (a *CLIConPTYAdapter) IsAlive() types.HealthStatus {
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

// Close terminates the bound session when session-bound, or is a no-op when stateless.
func (a *CLIConPTYAdapter) Close() error {
	if a.session != nil {
		return a.session.Close()
	}
	return nil
}

// Legacy returns the underlying LegacyExecutor for Strangler Fig bridging.
func (a *CLIConPTYAdapter) Legacy() types.LegacyExecutor {
	return a.legacy
}
