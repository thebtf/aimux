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
// It is stateless — each Send call spawns a fresh process via Run().
// PersistentSessions is false because conpty.Start() is not implemented (M6 adds it).
type CLIConPTYAdapter struct {
	legacy types.LegacyExecutor
}

// NewCLIConPTYAdapter creates a new CLIConPTYAdapter wrapping the given legacy executor.
// Accepts types.LegacyExecutor to avoid the import cycle; pass *conpty.Executor directly.
func NewCLIConPTYAdapter(legacy types.LegacyExecutor) *CLIConPTYAdapter {
	return &CLIConPTYAdapter{legacy: legacy}
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

// Send converts msg to SpawnArgs via messageToSpawnArgs, invokes the legacy Run(),
// and returns a unified Response. Metadata key handling and SystemPrompt prepending
// are delegated to messageToSpawnArgs (see adapter_common.go).
func (a *CLIConPTYAdapter) Send(ctx context.Context, msg types.Message) (*types.Response, error) {
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

// IsAlive returns HealthAlive when the underlying executor is available on this
// platform, HealthDead otherwise (e.g., non-Windows host).
func (a *CLIConPTYAdapter) IsAlive() types.HealthStatus {
	if a.legacy.Available() {
		return types.HealthAlive
	}
	return types.HealthDead
}

// Close is a no-op. CLIConPTYAdapter holds no persistent resources.
func (a *CLIConPTYAdapter) Close() error {
	return nil
}

// Legacy returns the underlying LegacyExecutor for Strangler Fig bridging.
func (a *CLIConPTYAdapter) Legacy() types.LegacyExecutor {
	return a.legacy
}
