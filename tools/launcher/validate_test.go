package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateReportSemantics(t *testing.T) {
	dir := t.TempDir()
	run := validateRun{
		StartedAt:  time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
		ReportPath: filepath.Join(dir, "report.md"),
		RunDir:     filepath.Join(dir, "logs"),
		Results: []ScenarioResult{
			{Name: "pass scenario", Status: StatusPass, Command: "launcher ok", LogPath: "ok.jsonl", Evidence: []string{"content changed"}},
			{Name: "blocked scenario", Status: StatusBlocked, Command: "launcher blocked", Blocker: "missing env var OPENAI_API_KEY"},
			{Name: "fail scenario", Status: StatusFail, Command: "launcher fail", Error: "assertion mismatch"},
		},
	}
	if err := writeValidationReport(run); err != nil {
		t.Fatalf("writeValidationReport: %v", err)
	}
	content, err := os.ReadFile(run.ReportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	text := string(content)
	for _, want := range []string{"PASS=1 BLOCKED=1 FAIL=1", "missing env var OPENAI_API_KEY", "assertion mismatch", "content changed", "launcher ok"} {
		if !strings.Contains(text, want) {
			t.Fatalf("report missing %q:\n%s", want, text)
		}
	}
	if got := countByStatus(run.Results, StatusFail); got != 1 {
		t.Fatalf("FAIL count = %d, want 1", got)
	}
	if got := countByStatus(run.Results, StatusBlocked); got != 1 {
		t.Fatalf("BLOCKED count = %d, want 1", got)
	}
}

func TestParseCSV(t *testing.T) {
	got := parseCSV("codex, claude,,gemini ")
	want := []string{"codex", "claude", "gemini"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestValidationExitCodeTreatsBlockedAsNonZeroByDefault(t *testing.T) {
	results := []ScenarioResult{{Name: "blocked", Status: StatusBlocked}}
	if got := validationExitCode(results, false); got != 2 {
		t.Fatalf("exit code=%d want 2", got)
	}
	if got := validationExitCode(results, true); got != 0 {
		t.Fatalf("exit code with allowBlocked=%d want 0", got)
	}
	results = append(results, ScenarioResult{Name: "fail", Status: StatusFail})
	if got := validationExitCode(results, true); got != 1 {
		t.Fatalf("FAIL must take precedence, exit code=%d want 1", got)
	}
}

func TestInspectANSIProof(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ansi.jsonl")
	content := `{"seq":1,"ts":"2026-05-02T00:00:00Z","kind":"stdout","payload":{"stream":"raw","bytes_hex":"1b5b33326d41494d55585f414e53495f4f4b1b5b306d"}}` + "\n" +
		`{"seq":2,"ts":"2026-05-02T00:00:00Z","kind":"stdout","payload":{"stream":"line","content":"AIMUX_ANSI_OK"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, stripped, err := inspectANSIProof(path)
	if err != nil {
		t.Fatal(err)
	}
	if !raw || !stripped {
		t.Fatalf("raw=%v stripped=%v", raw, stripped)
	}
}

func TestMissingKindsPropagatesScanErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.jsonl")
	if err := os.WriteFile(path, []byte("{not-json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := missingKinds(path, []string{KindComplete})
	if err == nil {
		t.Fatal("missingKinds returned nil error for malformed JSONL")
	}
}

func TestTurnContentContainsAgentSentinel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turns.jsonl")
	content := `{"seq":1,"ts":"2026-05-02T00:00:00Z","kind":"turn","payload":{"role":"user","content":"prompt","turn_id":1}}` + "\n" +
		`{"seq":2,"ts":"2026-05-02T00:00:00Z","kind":"turn","payload":{"role":"agent","content":"AIMUX_SESSION_OK","turn_id":1}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := hasTurnRoles(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected user and agent turn roles")
	}
	found, err := turnContentContains(path, "agent", "AIMUX_SESSION_OK")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected agent sentinel")
	}
}

func TestSafeCLINameRejectsPathTraversal(t *testing.T) {
	if safe, err := safeCLIName("codex"); err != nil || safe != "codex" {
		t.Fatalf("safeCLIName(codex)=(%q,%v), want codex,nil", safe, err)
	}
	for _, value := range []string{"../codex", `..\\codex`, "bad/name", "bad name", ".."} {
		if _, err := safeCLIName(value); err == nil {
			t.Fatalf("safeCLIName(%q) returned nil error", value)
		}
	}
}

func TestSyntheticProfileUsesTempSearchPath(t *testing.T) {
	profile := syntheticProfile("synthetic-ansi", "synthetic-ansi", "C:/tmp/synthetic-bin", "")
	if !strings.Contains(profile, "search_paths:") || !strings.Contains(profile, "C:/tmp/synthetic-bin") {
		t.Fatalf("profile does not include temp search path:\n%s", profile)
	}
	if strings.Contains(profile, "config/cli.d") {
		t.Fatalf("profile should not reference live config tree:\n%s", profile)
	}
}

func TestManualTUIRecipeMatchesInteractiveMode(t *testing.T) {
	recipe := strings.Join(manualTUIRecipe(validateOptions{
		ConfigDir: "config",
		CLIScope:  []string{"codex", "claude", "gemini"},
	}, "logs"), "\n")
	if strings.Contains(recipe, "Type `/dump`") {
		t.Fatalf("interactive TUI recipe must not instruct /dump input:\n%s", recipe)
	}
	for _, want := range []string{"`/help`", "`/quit`", "automated stdin-driven session scenario covers `/dump` evidence"} {
		if !strings.Contains(recipe, want) {
			t.Fatalf("manual TUI recipe missing %q:\n%s", want, recipe)
		}
	}
}

func TestClassifyAPIBlockerDoesNotHideGenericModelFailures(t *testing.T) {
	result := classifyAPIBlocker("openai", "launcher api", "api.jsonl", "", "launcher internal model parse failure", 1, errors.New("exit status 1"))
	if result.Status != StatusFail {
		t.Fatalf("status=%s want FAIL: %#v", result.Status, result)
	}
	result = classifyAPIBlocker("openai", "launcher api", "api.jsonl", "", "401 Unauthorized {\"message\":\"key not allowed for model\"}", 1, errors.New("exit status 1"))
	if result.Status != StatusBlocked {
		t.Fatalf("status=%s want BLOCKED: %#v", result.Status, result)
	}
}

func TestExternalTimeoutBlockerIncludesErrorDetail(t *testing.T) {
	result := classifyExternalBlocker("real CLI claude", "launcher cli", "cli.jsonl", "", "", -1, errors.New("context deadline exceeded: signal killed"))
	if result.Status != StatusBlocked {
		t.Fatalf("status=%s want BLOCKED: %#v", result.Status, result)
	}
	if !strings.Contains(result.Blocker, "context deadline exceeded") {
		t.Fatalf("blocker missing timeout detail: %#v", result)
	}
}

func TestExternalBlockerDoesNotHideGenericNotFoundFailures(t *testing.T) {
	result := classifyExternalBlocker("real CLI codex", "launcher cli", "cli.jsonl", "", "semantic assertion not found in response", 1, errors.New("exit status 1"))
	if result.Status != StatusFail {
		t.Fatalf("generic not found status=%s want FAIL: %#v", result.Status, result)
	}
	result = classifyExternalBlocker("real CLI missing", "launcher cli", "cli.jsonl", "", "command not found", 127, errors.New("exit status 127"))
	if result.Status != StatusBlocked {
		t.Fatalf("command not found status=%s want BLOCKED: %#v", result.Status, result)
	}
}
