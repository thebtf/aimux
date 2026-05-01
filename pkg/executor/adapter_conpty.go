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
//
// SendStream now forwards per-line stripped output through onChunk as the underlying
// IOManager reads them (via SpawnArgs.OnOutput); the final chunk has Done=true.
// Session-bound mode preserves the collected-response behaviour (single Done=true chunk).
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
			Streaming:          true,
		},
	}
}

// Send dispatches the message via session.Send() when session-bound, or via the
// legacy Run() path when stateless (Stateless SpawnMode preserved byte-identically,
// AIMUX-13 FR-1 immutability invariant). Metadata key handling and SystemPrompt
// prepending are delegated to messageToSpawnArgs (see adapter_common.go).
func (a *CLIConPTYAdapter) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	// Session-bound mode (FR-2): dispatch via persistent session.
	// SystemPrompt parity with stateless path — prepend before sending so
	// session-bound and stateless modes preserve identical message-context
	// semantics (PR #134 review — gemini high / coderabbit major).
	if a.session != nil {
		content := msg.Content
		if msg.SystemPrompt != "" {
			content = fmt.Sprintf("System: %s\n\n%s", msg.SystemPrompt, msg.Content)
		}
		result, err := a.session.Send(ctx, content)
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

// SendStream forwards per-line stripped output through onChunk as the underlying
// process produces it, then emits a final chunk with Done=true.
//
// Stateless path: SpawnArgs.OnOutput is populated so that IOManager delivers each
// new line to onChunk in real time before the process exits.
//
// Session-bound path: no per-line hook available; the full response is collected
// and delivered as a single Done=true chunk (multi-turn streaming is deferred).
func (a *CLIConPTYAdapter) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	// Session-bound mode: no per-line hook available — fall back to collected chunk.
	if a.session != nil {
		resp, err := a.Send(ctx, msg)
		if err != nil {
			return nil, err
		}
		onChunk(types.Chunk{Content: resp.Content, Done: true})
		return resp, nil
	}

	// Stateless path: wire OnOutput so IOManager forwards each line as it arrives.
	spawnArgs := messageToSpawnArgs(msg)
	if msg.SystemPrompt != "" && spawnArgs.Stdin == msg.Content {
		spawnArgs.Stdin = fmt.Sprintf("System: %s\n\n%s", msg.SystemPrompt, msg.Content)
	}
	spawnArgs.OnOutput = func(line string) {
		onChunk(types.Chunk{Content: line + "\n", Done: false})
	}

	result, err := a.legacy.Run(ctx, spawnArgs)
	// Emit the terminal Done marker regardless of error.
	onChunk(types.Chunk{Done: true})
	if err != nil {
		return nil, fmt.Errorf("conpty adapter: %w", err)
	}
	return resultToResponse(result), nil
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
