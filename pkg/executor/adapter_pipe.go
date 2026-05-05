package executor

import (
	"context"
	"fmt"

	"github.com/thebtf/aimux/pkg/types"
)

// Compile-time assertion: CLIPipeAdapter must implement ExecutorV2.
var _ types.ExecutorV2 = (*CLIPipeAdapter)(nil)

// CLIPipeAdapter wraps a legacy types.LegacyExecutor (typically *pipe.Executor) as
// an ExecutorV2. It accepts the interface to avoid an import cycle:
// pkg/executor/pipe imports pkg/executor for IOManager and process helpers.
// Pipe execution is inherently stateless — each Send spawns a fresh process.
//
// SendStream now forwards per-line stripped output through onChunk as the
// underlying IOManager reads them (via SpawnArgs.OnOutput); the final chunk
// has Done=true. Session-bound mode preserves the collected-response behaviour
// (single Done=true chunk) because sessions have no IOManager hook.
//
// When session is non-nil the adapter operates in session-bound mode:
// Send() dispatches via session.Send() instead of legacy.Run() (FR-2, AIMUX-14).
//
// Capabilities:
//   - Streaming: true — per-line output forwarded through onChunk as it arrives.
//   - PersistentSessions: true — pipe.Start() implements multi-turn sessions.
//
// Thread safety: CLIPipeAdapter is safe for concurrent use; Run() spawns an
// independent process per call.
type CLIPipeAdapter struct {
	legacy  types.LegacyExecutor
	session types.Session // nil when stateless (backward-compat default)
}

// NewCLIPipeAdapter creates a CLIPipeAdapter wrapping the given legacy executor.
// Accepts types.LegacyExecutor to avoid the import cycle; pass *pipe.Executor directly.
// The adapter operates in stateless mode (session == nil); existing callers are unaffected.
func NewCLIPipeAdapter(legacy types.LegacyExecutor) *CLIPipeAdapter {
	return &CLIPipeAdapter{legacy: legacy}
}

// NewCLIPipeAdapterWithSession creates a CLIPipeAdapter bound to an existing Session.
// Send() dispatches via session.Send() (session-bound mode, FR-2).
// Existing stateless Send() path via legacy.Run() is preserved byte-identically when
// session is nil — Stateless SpawnMode immutability invariant (AIMUX-13 FR-1).
func NewCLIPipeAdapterWithSession(legacy types.LegacyExecutor, sess types.Session) *CLIPipeAdapter {
	return &CLIPipeAdapter{legacy: legacy, session: sess}
}

// Info returns static metadata for the pipe executor adapter.
func (a *CLIPipeAdapter) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "pipe",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			PersistentSessions: true,
			Streaming:          true,
		},
	}
}

// Send dispatches the message via session.Send() when session-bound, or via the
// legacy Run() path when stateless (Stateless SpawnMode preserved byte-identically,
// AIMUX-13 FR-1 immutability invariant).
//
// Recognised Metadata keys (via messageToSpawnArgs, stateless path only):
//   - "command" (string)          — executable path/name (required)
//   - "args"    ([]string/[]any)  — command-line arguments
//
// SystemPrompt, when non-empty, is prepended to the payload on both session-bound
// and stateless paths so the CLI receives full context.
func (a *CLIPipeAdapter) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
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
			return nil, err
		}
		return resultToResponse(result), nil
	}

	// Stateless path — byte-identical to original (AIMUX-13 FR-1).
	args := messageToSpawnArgs(msg)

	// SystemPrompt: prepend to stdin so the CLI receives full context.
	if msg.SystemPrompt != "" && args.Stdin == msg.Content {
		args.Stdin = fmt.Sprintf("System: %s\n\n%s", msg.SystemPrompt, args.Stdin)
	}

	result, err := a.legacy.Run(ctx, args)
	if err != nil {
		return nil, err
	}
	return resultToResponse(result), nil
}

// SendStream forwards per-line stripped output through onChunk as the underlying
// process produces it, then emits a final chunk with Done=true.
//
// Stateless path: SpawnArgs.OnOutput is populated so that IOManager delivers each
// new line to onChunk in real time before the process exits. The final Done=true
// chunk is emitted after legacy.Run returns.
//
// Session-bound path: the session has no IOManager hook; the full response is
// collected by session.Send and delivered as a single Done=true chunk (same as
// the previous behaviour — multi-turn streaming is a separate phase).
func (a *CLIPipeAdapter) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
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
	args := messageToSpawnArgs(msg)
	if msg.SystemPrompt != "" && args.Stdin == msg.Content {
		args.Stdin = fmt.Sprintf("System: %s\n\n%s", msg.SystemPrompt, args.Stdin)
	}
	args.OnOutput = func(line string) {
		onChunk(types.Chunk{Content: line + "\n", Done: false})
	}

	result, err := a.legacy.Run(ctx, args)
	// Emit the terminal Done marker regardless of error.
	onChunk(types.Chunk{Done: true})
	if err != nil {
		return nil, err
	}
	return resultToResponse(result), nil
}

// IsAlive returns HealthAlive when the adapter is session-bound and the session
// process is alive, or when the stateless pipe executor reports itself as available.
func (a *CLIPipeAdapter) IsAlive() types.HealthStatus {
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
// (the pipe executor spawns a new process per Run() call and holds no persistent resources).
func (a *CLIPipeAdapter) Close() error {
	if a.session != nil {
		return a.session.Close()
	}
	return nil
}

// Legacy returns the underlying LegacyExecutor for Strangler Fig bridging.
// Used by Swarm.LegacyRun() to call Run(SpawnArgs) through Swarm lifecycle
// management without converting to Message/Response.
func (a *CLIPipeAdapter) Legacy() types.LegacyExecutor {
	return a.legacy
}
