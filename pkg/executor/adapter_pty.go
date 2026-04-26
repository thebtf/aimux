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
// Capabilities:
//   - Streaming: false — output is collected and returned in one Response.
//   - PersistentSessions: false — each Send spawns a new process (M6 adds it).
//
// Thread safety: CLIPTYAdapter is safe for concurrent use; the underlying
// executor spawns an independent process per Run() call.
type CLIPTYAdapter struct {
	legacy types.LegacyExecutor
}

// NewCLIPTYAdapter creates a CLIPTYAdapter wrapping the given legacy executor.
// Accepts types.LegacyExecutor to avoid the import cycle; pass *pty.Executor directly.
func NewCLIPTYAdapter(legacy types.LegacyExecutor) *CLIPTYAdapter {
	return &CLIPTYAdapter{legacy: legacy}
}

// Info returns static metadata for the PTY executor adapter.
func (a *CLIPTYAdapter) Info() types.ExecutorInfo {
	return types.ExecutorInfo{
		Name: "pty",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			Streaming:          false,
			PersistentSessions: false,
		},
	}
}

// Send converts msg to SpawnArgs, delegates to the legacy Run(), and maps the
// Result to a Response.
//
// Recognised Metadata keys (via messageToSpawnArgs):
//   - "command" (string)   — executable path/name
//   - "args"    ([]string) — command-line arguments
//   - "cwd"     (string)   — working directory
//   - "timeout" (int/int64/float64) — timeout in seconds
//   - "env"     (map[string]string or map[string]any) — extra env vars
//   - "stdin"   (string)   — override stdin (default: msg.Content)
//   - "completion_pattern" (string) — regex to detect process completion
func (a *CLIPTYAdapter) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
	args := messageToSpawnArgs(msg)

	// Resolve command/args from Metadata if provided.
	if msg.Metadata != nil {
		if v, ok := msg.Metadata["command"]; ok {
			if s, ok := v.(string); ok {
				args.Command = s
			}
		}
		if v, ok := msg.Metadata["args"]; ok {
			if sl, ok := v.([]string); ok {
				args.Args = sl
			}
		}
	}

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

// IsAlive returns HealthAlive when the PTY executor reports itself as available
// on the current platform, and HealthDead otherwise.
func (a *CLIPTYAdapter) IsAlive() types.HealthStatus {
	if a.legacy.Available() {
		return types.HealthAlive
	}
	return types.HealthDead
}

// Close is a no-op: the PTY executor spawns a new process per Run() call and
// holds no persistent resources.
func (a *CLIPTYAdapter) Close() error {
	return nil
}
