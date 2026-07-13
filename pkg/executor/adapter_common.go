package executor

import (
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// messageToSpawnArgs converts an ExecutorV2 Message to a legacy SpawnArgs.
//
// Metadata keys recognized:
//   - "command" (string)               — executable path/name
//   - "args"    ([]string or []any)    — command-line arguments
//   - "cwd"     (string)               — working directory
//   - "timeout" (int/int64/float64)    — timeout in seconds
//   - "stdin"   (string)               — data piped to the process stdin
//   - "completion_pattern" (string)    — regex to detect completion
//   - "env"     (map[string]any or map[string]string) — extra env vars
func messageToSpawnArgs(msg types.Message) types.SpawnArgs {
	return types.SpawnArgsFromMessage(msg)
}

// resultToResponse converts a legacy types.Result to an ExecutorV2 Response.
func resultToResponse(r *types.Result) *types.Response {
	if r == nil {
		return nil
	}
	return &types.Response{
		Content:  r.Content,
		Stderr:   r.Stderr,
		ExitCode: r.ExitCode,
		Partial:  r.Partial,
		Error:    r.Error,
		Duration: time.Duration(r.DurationMS) * time.Millisecond,
	}
}
