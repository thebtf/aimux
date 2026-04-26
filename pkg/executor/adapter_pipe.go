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
// SendStream is emulated by calling Send and delivering the full response as a
// single terminal chunk (Done=true), because the pipe executor buffers all output.
//
// Capabilities:
//   - Streaming: false — output is collected and returned in one Response.
//   - PersistentSessions: true — pipe.Start() implements multi-turn sessions.
//
// Thread safety: CLIPipeAdapter is safe for concurrent use; Run() spawns an
// independent process per call.
type CLIPipeAdapter struct {
	legacy types.LegacyExecutor
}

// NewCLIPipeAdapter creates a CLIPipeAdapter wrapping the given legacy executor.
// Accepts types.LegacyExecutor to avoid the import cycle; pass *pipe.Executor directly.
func NewCLIPipeAdapter(legacy types.LegacyExecutor) *CLIPipeAdapter {
	return &CLIPipeAdapter{legacy: legacy}
}

// Info returns static metadata for the pipe executor adapter.
func (a *CLIPipeAdapter) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "pipe",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			PersistentSessions: true,
			Streaming:          false,
		},
	}
}

// Send converts msg to SpawnArgs, delegates to the legacy Run(), and maps the
// Result to a Response. The caller must populate msg.Metadata["command"] and
// msg.Metadata["args"] so the adapter can build the SpawnArgs.
//
// Recognised Metadata keys (beyond adapter_common defaults):
//   - "command" (string)          — executable path/name (required)
//   - "args"    ([]string/[]any)  — command-line arguments
//
// SystemPrompt, when non-empty, is prepended to the stdin payload.
func (a *CLIPipeAdapter) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	args := messageToSpawnArgs(msg)

	// SystemPrompt: prepend to stdin so the CLI receives full context.
	if msg.SystemPrompt != "" && args.Stdin == msg.Content {
		args.Stdin = fmt.Sprintf("System: %s\n\n%s", msg.SystemPrompt, msg.Content)
	}

	result, err := a.legacy.Run(ctx, args)
	if err != nil {
		return nil, err
	}
	return resultToResponse(result), nil
}

// SendStream calls Send and delivers the complete response as a single chunk
// with Done=true. Pipe executor does not support incremental streaming.
func (a *CLIPipeAdapter) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	resp, err := a.Send(ctx, msg)
	if err != nil {
		return nil, err
	}
	onChunk(types.Chunk{Content: resp.Content, Done: true})
	return resp, nil
}

// IsAlive returns HealthAlive when the pipe executor reports itself as available
// (always true for pipe — it works on all platforms), HealthDead otherwise.
func (a *CLIPipeAdapter) IsAlive() types.HealthStatus {
	if a.legacy.Available() {
		return types.HealthAlive
	}
	return types.HealthDead
}

// Close is a no-op: the pipe executor spawns a new process per Run() call and
// holds no persistent resources at the adapter level.
func (a *CLIPipeAdapter) Close() error {
	return nil
}

// Legacy returns the underlying LegacyExecutor for Strangler Fig bridging.
// Used by Swarm.LegacyRun() to call Run(SpawnArgs) through Swarm lifecycle
// management without converting to Message/Response.
func (a *CLIPipeAdapter) Legacy() types.LegacyExecutor {
	return a.legacy
}
