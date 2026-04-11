package orchestrator

import (
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// legacyResolver implements types.CLIResolver but NOT types.ModelledCLIResolver.
// It records the last CLI and prompt it received for assertion.
type legacyResolver struct {
	lastCLI    string
	lastPrompt string
	returnErr  error
}

func (r *legacyResolver) ResolveSpawnArgs(cli, prompt string) (types.SpawnArgs, error) {
	r.lastCLI = cli
	r.lastPrompt = prompt
	if r.returnErr != nil {
		return types.SpawnArgs{}, r.returnErr
	}
	return types.SpawnArgs{
		CLI:     cli,
		Command: cli,
		Args:    []string{"-p", prompt},
	}, nil
}

// modelledResolver implements both interfaces. Records which path was taken.
type modelledResolver struct {
	legacyResolver
	optsPathCalled   bool
	legacyPathCalled bool
	capturedModel    string
	capturedEffort   string
}

func (r *modelledResolver) ResolveSpawnArgs(cli, prompt string) (types.SpawnArgs, error) {
	r.legacyPathCalled = true
	return r.legacyResolver.ResolveSpawnArgs(cli, prompt)
}

func (r *modelledResolver) ResolveSpawnArgsWithOpts(cli, prompt, model, effort string) (types.SpawnArgs, error) {
	r.optsPathCalled = true
	r.capturedModel = model
	r.capturedEffort = effort
	r.lastCLI = cli
	r.lastPrompt = prompt
	return types.SpawnArgs{
		CLI:     cli,
		Command: cli,
		Args:    []string{"-p", prompt},
	}, nil
}

func TestResolveOrFallbackWithOpts_UsesModelledResolverWhenModelSet(t *testing.T) {
	r := &modelledResolver{}
	args := resolveOrFallbackWithOpts(r, "codex", "hello", "/cwd", 30, "gpt-5.3-codex-spark", "")

	if !r.optsPathCalled {
		t.Error("modelled path should be called when model is non-empty")
	}
	if r.legacyPathCalled {
		t.Error("legacy path must NOT be called when modelled path was taken")
	}
	if r.capturedModel != "gpt-5.3-codex-spark" {
		t.Errorf("captured model = %q, want gpt-5.3-codex-spark", r.capturedModel)
	}
	if args.CWD != "/cwd" {
		t.Errorf("cwd not propagated to args: %q", args.CWD)
	}
	if args.TimeoutSeconds != 30 {
		t.Errorf("timeout not propagated: %d", args.TimeoutSeconds)
	}
}

func TestResolveOrFallbackWithOpts_UsesModelledResolverWhenEffortSet(t *testing.T) {
	r := &modelledResolver{}
	_ = resolveOrFallbackWithOpts(r, "codex", "hello", "", 0, "", "high")

	if !r.optsPathCalled {
		t.Error("modelled path should be called when effort is non-empty")
	}
	if r.capturedEffort != "high" {
		t.Errorf("captured effort = %q, want high", r.capturedEffort)
	}
}

func TestResolveOrFallbackWithOpts_FallbackWhenUnmodelled(t *testing.T) {
	r := &legacyResolver{}
	args := resolveOrFallbackWithOpts(r, "codex", "hello", "/cwd", 30, "spark", "high")

	// Legacy resolver doesn't implement ModelledCLIResolver; opts must be
	// silently dropped and the legacy path used.
	if r.lastCLI != "codex" {
		t.Errorf("legacy ResolveSpawnArgs should have been called; lastCLI = %q", r.lastCLI)
	}
	if args.CWD != "/cwd" || args.TimeoutSeconds != 30 {
		t.Errorf("cwd/timeout not applied: cwd=%q timeout=%d", args.CWD, args.TimeoutSeconds)
	}
}

func TestResolveOrFallbackWithOpts_EmptyOptsUsesLegacyPathEvenWithModelledResolver(t *testing.T) {
	r := &modelledResolver{}
	_ = resolveOrFallbackWithOpts(r, "codex", "hello", "", 0, "", "")

	if r.optsPathCalled {
		t.Error("modelled path must NOT be called when both model and effort are empty")
	}
	if !r.legacyPathCalled {
		t.Error("legacy path should be called for backward compatibility when no opts")
	}
}

func TestResolveOrFallbackWithOpts_NilResolverLegacyFallback(t *testing.T) {
	args := resolveOrFallbackWithOpts(nil, "codex", "hello", "/work", 60, "spark", "high")

	if args.CLI != "codex" {
		t.Errorf("CLI = %q, want codex", args.CLI)
	}
	if args.Command != "codex" {
		t.Errorf("Command = %q, want codex", args.Command)
	}
	if len(args.Args) != 2 || args.Args[0] != "-p" || args.Args[1] != "hello" {
		t.Errorf("Args = %v, want [-p hello]", args.Args)
	}
	if args.CWD != "/work" {
		t.Errorf("CWD = %q, want /work", args.CWD)
	}
	if args.TimeoutSeconds != 60 {
		t.Errorf("TimeoutSeconds = %d, want 60", args.TimeoutSeconds)
	}
}

func TestResolveOrFallbackWithOpts_ResolverErrorFallsBack(t *testing.T) {
	r := &legacyResolver{returnErr: errStub("boom")}
	args := resolveOrFallbackWithOpts(r, "codex", "hello", "/work", 30, "", "")

	// When resolver returns an error, helper falls back to the bare spawn args
	// construction (Command = cli, Args = ["-p", prompt]).
	if args.Command != "codex" {
		t.Errorf("fallback Command = %q, want codex", args.Command)
	}
	if len(args.Args) != 2 || args.Args[0] != "-p" {
		t.Errorf("fallback Args = %v, want [-p hello]", args.Args)
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }
