package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// ── Builder round-trip ──────────────────────────────────────────────────────

func TestBuilder_ImmutableRoundTrip(t *testing.T) {
	t.Parallel()
	original := New("codex", "/work").Build()
	modified := From(original).
		WithHomeOverride(HomeOverrideVirtual).
		Build()

	if original.HomeOverride != HomeOverrideNone {
		t.Errorf("original mutated: HomeOverride=%v want %v", original.HomeOverride, HomeOverrideNone)
	}
	if modified.HomeOverride != HomeOverrideVirtual {
		t.Errorf("modified.HomeOverride=%v want %v", modified.HomeOverride, HomeOverrideVirtual)
	}
}

func TestBuilder_EnvOverridesCopied(t *testing.T) {
	t.Parallel()
	overrides := map[string]string{"KEY": "val"}
	p := New("aider", "/work").WithEnvOverrides(overrides).Build()
	// Mutating the original map must not affect the profile.
	overrides["KEY"] = "changed"
	if p.EnvOverrides["KEY"] != "val" {
		t.Errorf("EnvOverrides not copied: got %q want %q", p.EnvOverrides["KEY"], "val")
	}
}

func TestBuilder_ExtraFlagsCopied(t *testing.T) {
	t.Parallel()
	flags := []string{"--bare"}
	p := New("claude", "/work").WithExtraFlags(flags).Build()
	flags[0] = "--mutated"
	if p.ExtraFlags[0] != "--bare" {
		t.Errorf("ExtraFlags not copied: got %q want %q", p.ExtraFlags[0], "--bare")
	}
}

func TestBuilder_HookComposition(t *testing.T) {
	t.Parallel()
	called := []int{}
	h1 := PreSpawnHook(func(_ CLIRuntimeProfile, _ map[string]string) error {
		called = append(called, 1)
		return nil
	})
	h2 := PreSpawnHook(func(_ CLIRuntimeProfile, _ map[string]string) error {
		called = append(called, 2)
		return nil
	})
	p := New("codex", "/work").WithPreSpawnHook(h1).WithPreSpawnHook(h2).Build()
	if len(p.PreSpawnHooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(p.PreSpawnHooks))
	}
	env := map[string]string{}
	_ = p.PreSpawnHooks[0](p, env)
	_ = p.PreSpawnHooks[1](p, env)
	if len(called) != 2 || called[0] != 1 || called[1] != 2 {
		t.Errorf("hooks called out of order: %v", called)
	}
}

// ── Spawn: env merge ─────────────────────────────────────────────────────────

func TestSpawn_EnvMerge(t *testing.T) {
	t.Parallel()
	base := types.SpawnArgs{
		Command: "codex",
		Env:     map[string]string{"BASE": "base"},
	}
	profile := New("codex", "/work").
		WithEnvOverrides(map[string]string{"OVERRIDE": "val", "BASE": "overridden"}).
		Build()

	out, err := Spawn(profile, base)
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if out.Env["BASE"] != "overridden" {
		t.Errorf("BASE=%q want %q", out.Env["BASE"], "overridden")
	}
	if out.Env["OVERRIDE"] != "val" {
		t.Errorf("OVERRIDE=%q want %q", out.Env["OVERRIDE"], "val")
	}
}

func TestSpawn_UnsetEnvVars(t *testing.T) {
	t.Parallel()
	base := types.SpawnArgs{
		Env: map[string]string{"KEEP": "yes", "REMOVE": "no"},
	}
	profile := New("codex", "/work").
		WithUnsetEnvVars([]string{"REMOVE"}).
		Build()

	out, err := Spawn(profile, base)
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if _, exists := out.Env["REMOVE"]; exists {
		t.Error("REMOVE should have been unset")
	}
	if out.Env["KEEP"] != "yes" {
		t.Errorf("KEEP=%q want %q", out.Env["KEEP"], "yes")
	}
}

func TestSpawn_BaseMutationSafety(t *testing.T) {
	t.Parallel()
	base := types.SpawnArgs{
		Command: "codex",
		Args:    []string{"exec"},
		Env:     map[string]string{"A": "1"},
	}
	profile := New("codex", "/work").
		WithEnvOverrides(map[string]string{"B": "2"}).
		WithExtraFlags([]string{"--ephemeral"}).
		Build()

	_, err := Spawn(profile, base)
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	// base must be unchanged.
	if len(base.Args) != 1 {
		t.Errorf("base.Args mutated: len=%d want 1", len(base.Args))
	}
	if _, ok := base.Env["B"]; ok {
		t.Error("base.Env mutated: key B should not exist")
	}
}

// ── Spawn: CODEX_HOME redirect ───────────────────────────────────────────────

func TestSpawn_CLIHomeEnvVar(t *testing.T) {
	t.Parallel()
	profile := New("codex", "/work").
		WithHomeOverride(HomeOverrideVirtual).
		WithCLIHomeEnvVar("CODEX_HOME").
		WithVirtualHomeDir("/tmp/codex-virtual").
		Build()

	out, err := Spawn(profile, types.SpawnArgs{Command: "codex"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if out.Env["CODEX_HOME"] != "/tmp/codex-virtual" {
		t.Errorf("CODEX_HOME=%q want %q", out.Env["CODEX_HOME"], "/tmp/codex-virtual")
	}
}

// ── Spawn: DangerIsolated=false does NOT set HOME/USERPROFILE ───────────────

func TestSpawn_NoDangerIsolated_NoHomeOverride(t *testing.T) {
	t.Parallel()
	profile := New("gemini", "/work").
		WithHomeOverride(HomeOverrideVirtual).
		WithVirtualHomeDir("/tmp/gemini-home").
		Build() // DangerIsolated=false, CLIHomeEnvVar=""

	out, err := Spawn(profile, types.SpawnArgs{Command: "gemini"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if _, ok := out.Env["HOME"]; ok {
		t.Error("HOME should NOT be set when DangerIsolated=false and CLIHomeEnvVar=\"\"")
	}
	if _, ok := out.Env["USERPROFILE"]; ok {
		t.Error("USERPROFILE should NOT be set when DangerIsolated=false and CLIHomeEnvVar=\"\"")
	}
}

// ── Spawn: DangerIsolated=true sets HOME/USERPROFILE ────────────────────────

func TestSpawn_DangerIsolated_SetsHomeOrUserprofile(t *testing.T) {
	t.Parallel()
	profile := New("gemini", "/work").
		WithHomeOverride(HomeOverrideVirtual).
		WithVirtualHomeDir("/tmp/gemini-isolated").
		WithDangerIsolated(true).
		Build()

	out, err := Spawn(profile, types.SpawnArgs{Command: "gemini"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if runtime.GOOS == "windows" {
		if out.Env["USERPROFILE"] != "/tmp/gemini-isolated" {
			t.Errorf("USERPROFILE=%q want %q", out.Env["USERPROFILE"], "/tmp/gemini-isolated")
		}
	} else {
		if out.Env["HOME"] != "/tmp/gemini-isolated" {
			t.Errorf("HOME=%q want %q", out.Env["HOME"], "/tmp/gemini-isolated")
		}
	}
}

func TestSpawn_DangerIsolated_RequiresVirtualHomeDir(t *testing.T) {
	t.Parallel()
	profile := New("gemini", "/work").
		WithHomeOverride(HomeOverrideVirtual).
		WithDangerIsolated(true).
		Build() // VirtualHomeDir is empty — must error

	_, err := Spawn(profile, types.SpawnArgs{Command: "gemini"})
	if err == nil {
		t.Error("expected error when DangerIsolated=true and VirtualHomeDir is empty")
	}
}

// ── Spawn: ExtraFlags appended ───────────────────────────────────────────────

func TestSpawn_ExtraFlagsAppended(t *testing.T) {
	t.Parallel()
	base := types.SpawnArgs{
		Command: "codex",
		Args:    []string{"exec", "prompt"},
	}
	profile := New("codex", "/work").
		WithExtraFlags([]string{"--ephemeral", "--ignore-user-config"}).
		Build()

	out, err := Spawn(profile, base)
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if len(out.Args) != 4 {
		t.Fatalf("Args len=%d want 4: %v", len(out.Args), out.Args)
	}
	if out.Args[2] != "--ephemeral" || out.Args[3] != "--ignore-user-config" {
		t.Errorf("ExtraFlags not at end: %v", out.Args)
	}
}

// ── Spawn: WorkDir applied ───────────────────────────────────────────────────

func TestSpawn_WorkDir(t *testing.T) {
	t.Parallel()
	profile := New("codex", "/my/project").Build()
	out, err := Spawn(profile, types.SpawnArgs{Command: "codex", CWD: "/old"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if out.CWD != "/my/project" {
		t.Errorf("CWD=%q want %q", out.CWD, "/my/project")
	}
}

// ── Spawn: PreSpawnHook env modification ─────────────────────────────────────

func TestSpawn_PreSpawnHookModifiesEnv(t *testing.T) {
	t.Parallel()
	hook := PreSpawnHook(func(_ CLIRuntimeProfile, env map[string]string) error {
		env["HOOK_KEY"] = "hook_val"
		return nil
	})
	profile := New("codex", "/work").WithPreSpawnHook(hook).Build()

	out, err := Spawn(profile, types.SpawnArgs{Command: "codex"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if out.Env["HOOK_KEY"] != "hook_val" {
		t.Errorf("HOOK_KEY=%q want %q", out.Env["HOOK_KEY"], "hook_val")
	}
}

// ── Per-CLI profile field correctness ────────────────────────────────────────

func TestDefaultCodexProfile(t *testing.T) {
	t.Parallel()
	p := DefaultCodexProfile("/work")
	if p.CLIName != "codex" {
		t.Errorf("CLIName=%q want codex", p.CLIName)
	}
	if p.HomeOverride != HomeOverrideVirtual {
		t.Errorf("HomeOverride=%v want HomeOverrideVirtual", p.HomeOverride)
	}
	if p.CLIHomeEnvVar != "CODEX_HOME" {
		t.Errorf("CLIHomeEnvVar=%q want CODEX_HOME", p.CLIHomeEnvVar)
	}
	if p.AuthScope != AuthScopePassThrough {
		t.Errorf("AuthScope=%v want AuthScopePassThrough", p.AuthScope)
	}
	if len(p.AuthFiles) == 0 || p.AuthFiles[0] != "auth.json" {
		t.Errorf("AuthFiles=%v want [auth.json]", p.AuthFiles)
	}
	if p.DangerIsolated {
		t.Error("DangerIsolated should be false for codex (uses CODEX_HOME, no HOME redirect needed)")
	}
}

func TestIsolatedCodexProfile(t *testing.T) {
	t.Parallel()
	p := IsolatedCodexProfile("/work")
	if p.MCPMode != MCPModeNone {
		t.Errorf("MCPMode=%v want MCPModeNone", p.MCPMode)
	}
	if p.StateScope != StateScopeEphemeral {
		t.Errorf("StateScope=%v want StateScopeEphemeral", p.StateScope)
	}
	if p.InstructionMode != InstructionModeReplace {
		t.Errorf("InstructionMode=%v want InstructionModeReplace", p.InstructionMode)
	}
	found := false
	for _, f := range p.ExtraFlags {
		if f == "--ephemeral" {
			found = true
		}
	}
	if !found {
		t.Errorf("ExtraFlags missing --ephemeral: %v", p.ExtraFlags)
	}
}

func TestDefaultClaudeProfile(t *testing.T) {
	t.Parallel()
	p := DefaultClaudeProfile("/work")
	if p.CLIName != "claude" {
		t.Errorf("CLIName=%q want claude", p.CLIName)
	}
	if p.HomeOverride != HomeOverrideNone {
		t.Errorf("HomeOverride=%v want HomeOverrideNone", p.HomeOverride)
	}
	if p.DangerIsolated {
		t.Error("DangerIsolated should be false for claude default profile")
	}
}

func TestIsolatedClaudeProfile(t *testing.T) {
	t.Parallel()
	p := IsolatedClaudeProfile("/work")
	flags := map[string]bool{}
	for _, f := range p.ExtraFlags {
		flags[f] = true
	}
	if !flags["--bare"] {
		t.Errorf("ExtraFlags missing --bare: %v", p.ExtraFlags)
	}
	if !flags["--strict-mcp-config"] {
		t.Errorf("ExtraFlags missing --strict-mcp-config: %v", p.ExtraFlags)
	}
	if !flags["--no-session-persistence"] {
		t.Errorf("ExtraFlags missing --no-session-persistence: %v", p.ExtraFlags)
	}
	if p.MCPMode != MCPModeNone {
		t.Errorf("MCPMode=%v want MCPModeNone", p.MCPMode)
	}
	if p.StateScope != StateScopeEphemeral {
		t.Errorf("StateScope=%v want StateScopeEphemeral", p.StateScope)
	}
}

func TestDefaultGeminiProfile(t *testing.T) {
	t.Parallel()
	p := DefaultGeminiProfile("/work")
	if p.CLIName != "gemini" {
		t.Errorf("CLIName=%q want gemini", p.CLIName)
	}
	if p.HomeOverride != HomeOverrideNone {
		t.Errorf("HomeOverride=%v want HomeOverrideNone", p.HomeOverride)
	}
	if p.DangerIsolated {
		t.Error("DangerIsolated should be false for gemini (Strategy B default)")
	}
	if p.CLIHomeEnvVar != "" {
		t.Errorf("CLIHomeEnvVar=%q want empty (no GEMINI_HOME exists)", p.CLIHomeEnvVar)
	}
}

func TestDegradedGeminiProfile(t *testing.T) {
	t.Parallel()
	p := DegradedGeminiProfile("/work")
	if p.DangerIsolated {
		t.Error("DangerIsolated must be false for degraded gemini profile (Strategy B)")
	}
	if p.InstructionMode != InstructionModeOverlayOnly {
		t.Errorf("InstructionMode=%v want InstructionModeOverlayOnly", p.InstructionMode)
	}
	if p.MCPMode != MCPModeNone {
		t.Errorf("MCPMode=%v want MCPModeNone", p.MCPMode)
	}
	if p.AuthScope != AuthScopeIsolated {
		t.Errorf("AuthScope=%v want AuthScopeIsolated", p.AuthScope)
	}
}

// ── EphemeralCleanupHook ─────────────────────────────────────────────────────

func TestEphemeralCleanupHook_RemovesDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Create a file inside to verify the whole tree is removed.
	if err := os.WriteFile(filepath.Join(dir, "testfile"), []byte("data"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	profile := New("codex", "/work").
		WithStateScope(StateScopeEphemeral).
		WithVirtualHomeDir(dir).
		Build()

	if err := EphemeralCleanupHook(profile, 0); err != nil {
		t.Fatalf("EphemeralCleanupHook error: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dir %q should have been removed", dir)
	}
}

func TestEphemeralCleanupHook_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profile := New("codex", "/work").
		WithStateScope(StateScopeEphemeral).
		WithVirtualHomeDir(dir).
		Build()

	_ = EphemeralCleanupHook(profile, 0) // first call removes
	// Second call on already-removed dir must return nil (os.RemoveAll is idempotent).
	if err := EphemeralCleanupHook(profile, 0); err != nil {
		t.Errorf("second EphemeralCleanupHook call should not error: %v", err)
	}
}

func TestEphemeralCleanupHook_SkipsPassThrough(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profile := New("codex", "/work").
		WithStateScope(StateScopePassThrough). // NOT ephemeral
		WithVirtualHomeDir(dir).
		Build()

	if err := EphemeralCleanupHook(profile, 0); err != nil {
		t.Fatalf("EphemeralCleanupHook error: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir %q should NOT have been removed for PassThrough scope", dir)
	}
}

// ── Spawn: EnvList merged into resolved env map ──────────────────────────────

func TestSpawn_EnvListMerged(t *testing.T) {
	t.Parallel()
	base := types.SpawnArgs{
		Command: "codex",
		EnvList: []string{"LIST_KEY=list_val", "SHARED=from_list"},
		Env:     map[string]string{"MAP_KEY": "map_val", "SHARED": "from_map"},
	}
	profile := New("codex", "/work").Build()

	out, err := Spawn(profile, base)
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	if out.Env["LIST_KEY"] != "list_val" {
		t.Errorf("LIST_KEY=%q want list_val (from EnvList)", out.Env["LIST_KEY"])
	}
	if out.Env["MAP_KEY"] != "map_val" {
		t.Errorf("MAP_KEY=%q want map_val (from Env map)", out.Env["MAP_KEY"])
	}
	// Env map wins over EnvList (map is applied first, list iteration order is stable).
	// Both are present; test that neither was silently dropped.
	if out.Env["SHARED"] == "" {
		t.Error("SHARED should be set from either Env or EnvList")
	}
	if out.EnvList != nil {
		t.Error("EnvList should be cleared when Env map is set")
	}
}

// ── Spawn: CLIHomeEnvVar with empty VirtualHomeDir errors ────────────────────

func TestSpawn_CLIHomeEnvVar_RequiresVirtualHomeDir(t *testing.T) {
	t.Parallel()
	profile := New("codex", "/work").
		WithHomeOverride(HomeOverrideVirtual).
		WithCLIHomeEnvVar("CODEX_HOME").
		Build() // VirtualHomeDir is empty — must error

	_, err := Spawn(profile, types.SpawnArgs{Command: "codex"})
	if err == nil {
		t.Error("expected error when CLIHomeEnvVar is set but VirtualHomeDir is empty")
	}
}

// ── EphemeralCleanupHook: refuses to remove root paths ───────────────────────

func TestEphemeralCleanupHook_RefusesRootPaths(t *testing.T) {
	t.Parallel()

	roots := []string{"/", "."}
	for _, root := range roots {
		profile := New("codex", "/work").
			WithStateScope(StateScopeEphemeral).
			WithVirtualHomeDir(root).
			Build()
		if err := EphemeralCleanupHook(profile, 0); err != nil {
			t.Errorf("EphemeralCleanupHook(%q): unexpected error: %v", root, err)
		}
		// The hook must not remove the root — it should still exist.
		if _, statErr := os.Stat(root); os.IsNotExist(statErr) {
			t.Errorf("root path %q was removed by EphemeralCleanupHook", root)
		}
	}
}

// ── RunPostExitHooks ─────────────────────────────────────────────────────────

func TestRunPostExitHooks_AllRun(t *testing.T) {
	t.Parallel()
	ran := []int{}
	h1 := PostExitHook(func(_ CLIRuntimeProfile, _ int) error { ran = append(ran, 1); return nil })
	h2 := PostExitHook(func(_ CLIRuntimeProfile, _ int) error { ran = append(ran, 2); return nil })
	p := New("codex", "/work").WithPostExitHook(h1).WithPostExitHook(h2).Build()

	if err := RunPostExitHooks(p, 0); err != nil {
		t.Fatalf("RunPostExitHooks error: %v", err)
	}
	if len(ran) != 2 || ran[0] != 1 || ran[1] != 2 {
		t.Errorf("hooks did not all run in order: %v", ran)
	}
}
