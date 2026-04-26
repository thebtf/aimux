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
	args := types.SpawnArgs{
		Stdin: msg.Content,
	}

	if msg.Metadata == nil {
		return args
	}

	if v, ok := msg.Metadata["command"]; ok {
		if s, ok := v.(string); ok {
			args.Command = s
		}
	}

	if v, ok := msg.Metadata["args"]; ok {
		switch sl := v.(type) {
		case []string:
			args.Args = sl
		case []any:
			strs := make([]string, 0, len(sl))
			for _, item := range sl {
				if s, ok := item.(string); ok {
					strs = append(strs, s)
				}
			}
			args.Args = strs
		}
	}

	if v, ok := msg.Metadata["cwd"]; ok {
		if s, ok := v.(string); ok {
			args.CWD = s
		}
	}

	if v, ok := msg.Metadata["stdin"]; ok {
		if s, ok := v.(string); ok {
			args.Stdin = s
		}
	}

	if v, ok := msg.Metadata["timeout"]; ok {
		switch n := v.(type) {
		case int:
			args.TimeoutSeconds = n
		case int64:
			args.TimeoutSeconds = int(n)
		case float64:
			args.TimeoutSeconds = int(n)
		}
	}

	if v, ok := msg.Metadata["completion_pattern"]; ok {
		if s, ok := v.(string); ok {
			args.CompletionPattern = s
		}
	}

	if v, ok := msg.Metadata["env"]; ok {
		switch m := v.(type) {
		case map[string]string:
			args.Env = m
		case map[string]any:
			env := make(map[string]string, len(m))
			for k, val := range m {
				if s, ok := val.(string); ok {
					env[k] = s
				}
			}
			args.Env = env
		}
	}

	return args
}

// resultToResponse converts a legacy types.Result to an ExecutorV2 Response.
func resultToResponse(r *types.Result) *types.Response {
	return &types.Response{
		Content:  r.Content,
		ExitCode: r.ExitCode,
		Duration: time.Duration(r.DurationMS) * time.Millisecond,
	}
}
