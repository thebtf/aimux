package workers

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/thebtf/aimux/loom"
)

// SubprocessSpawn is the aimux-agnostic spawn descriptor produced by a SpawnResolver.
// SubprocessBase does not care how it was resolved — only what to run.
// Env uses map[string]string for clean interop; the default runner converts to
// os/exec.Cmd.Env format (KEY=VALUE) internally.
//
// Meta is an opaque extension slot for runner implementations that need to carry
// additional data beyond what SubprocessSpawn expresses (e.g. the full
// types.SpawnArgs for the aimux executor). The default os/exec runner ignores Meta.
type SubprocessSpawn struct {
	Command string
	Args    []string
	CWD     string
	Env     map[string]string // optional; merged on top of process environment
	Stdin   string            // optional stdin content
	Meta    map[string]any    // opaque; ignored by default runner, available to custom runners
}

// SpawnResolver builds a SubprocessSpawn from a task at execution time.
// Implementations may perform CLI resolution, env merging, etc.
type SpawnResolver interface {
	Resolve(ctx context.Context, task *loom.Task) (SubprocessSpawn, error)
}

// SubprocessRunner is the backend that actually spawns the process.
// The default implementation uses os/exec. aimux passes its full-featured
// executor here to get ConPTY/PTY/Pipe selection, model fallback, etc.
type SubprocessRunner interface {
	Run(ctx context.Context, spawn SubprocessSpawn) (stdout string, exitCode int, err error)
}

// SubprocessBase is a composable base for workers that execute a subprocess.
// Workers embed or hold a SubprocessBase and provide a SpawnResolver + loom.WorkerType.
//
//	func (w *CLIWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
//	    return w.base.Run(ctx, task)
//	}
type SubprocessBase struct {
	Resolver SpawnResolver
	// Runner is the subprocess backend. If nil, DefaultRunner() is used (os/exec).
	Runner SubprocessRunner
}

// Run executes the task as a subprocess.
func (b *SubprocessBase) Run(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	spawn, err := b.Resolver.Resolve(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("subprocess: resolve: %w", err)
	}

	// Derive timeout context if task.Timeout > 0 and the parent context has no
	// deadline. If the parent already carries a deadline (e.g. the engine's
	// per-dispatch timeout), we honour that rather than applying a second,
	// potentially longer, budget that would silently reset the parent's intent.
	runCtx := ctx
	if task.Timeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			runCtx, cancel = context.WithTimeout(ctx, time.Duration(task.Timeout)*time.Second)
			defer cancel()
		}
	}

	runner := b.Runner
	if runner == nil {
		runner = DefaultRunner()
	}

	start := time.Now()
	stdout, exitCode, runErr := runner.Run(runCtx, spawn)
	duration := time.Since(start).Milliseconds()

	result := &loom.WorkerResult{
		Content: stdout,
		Metadata: map[string]any{
			"exit_code": exitCode,
		},
		DurationMS: duration,
	}

	// Preserve context cancellation semantics: if runCtx was cancelled, surface it
	// so the engine can distinguish cancellation from a process error.
	if runCtx.Err() != nil {
		return result, fmt.Errorf("subprocess: %w", runCtx.Err())
	}
	if runErr != nil {
		return result, fmt.Errorf("subprocess: run: %w", runErr)
	}
	return result, nil
}

// execRunner implements SubprocessRunner using os/exec.
type execRunner struct{}

// DefaultRunner returns a SubprocessRunner backed by os/exec.
func DefaultRunner() SubprocessRunner { return execRunner{} }

// Run executes spawn.Command with spawn.Args using os/exec.CommandContext.
// stdout and stderr are merged into a single output buffer.
func (execRunner) Run(ctx context.Context, spawn SubprocessSpawn) (string, int, error) {
	cmd := exec.CommandContext(ctx, spawn.Command, spawn.Args...)
	if spawn.CWD != "" {
		cmd.Dir = spawn.CWD
	}
	if len(spawn.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range spawn.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	if spawn.Stdin != "" {
		cmd.Stdin = bytes.NewBufferString(spawn.Stdin)
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return out.String(), exitCode, err
}
