package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// newCritiqueServer returns a test server wired with a custom stub executor.
// The stub's run func is called for every Run() invocation.
func newCritiqueServer(t *testing.T, run func(ctx context.Context, args types.SpawnArgs) (*types.Result, error)) *Server {
	t.Helper()
	srv := testServer(t)
	srv.executor = &stubExecutor{run: run}
	return srv
}

// TestHandleCritique_MissingArtifact verifies that omitting the required
// "artifact" field returns a tool error (not a Go error).
func TestHandleCritique_MissingArtifact(t *testing.T) {
	srv := testServer(t)

	req := makeRequest("critique", map[string]any{})
	result, err := srv.handleCritique(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for missing artifact")
	}
}

// TestHandleCritique_UnknownLens verifies that an invalid lens name returns
// a tool error listing valid lenses.
func TestHandleCritique_UnknownLens(t *testing.T) {
	srv := testServer(t)

	req := makeRequest("critique", map[string]any{
		"artifact": "some code",
		"lens":     "nonexistent-lens",
	})
	result, err := srv.handleCritique(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for unknown lens")
	}
}

// TestHandleCritique_ValidLenses verifies that each built-in lens name is accepted.
func TestHandleCritique_ValidLenses(t *testing.T) {
	validLenses := []string{"security", "api-design", "spec-compliance", "adversarial"}

	for _, lens := range validLenses {
		t.Run(lens, func(t *testing.T) {
			srv := newCritiqueServer(t, func(_ context.Context, _ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{
					Content:  `{"findings": [], "summary": "no issues"}`,
					ExitCode: 0,
				}, nil
			})

			req := makeRequest("critique", map[string]any{
				"artifact": "func add(a, b int) int { return a + b }",
				"lens":     lens,
			})
			result, err := srv.handleCritique(context.Background(), req)
			if err != nil {
				t.Fatalf("lens %q: unexpected Go error: %v", lens, err)
			}
			if result.IsError {
				t.Fatalf("lens %q: unexpected tool error: %v", lens, result.Content)
			}
		})
	}
}

// TestHandleCritique_NoLens verifies that omitting "lens" uses the generic reviewer template.
func TestHandleCritique_NoLens(t *testing.T) {
	srv := newCritiqueServer(t, func(_ context.Context, _ types.SpawnArgs) (*types.Result, error) {
		return &types.Result{
			Content:  `{"findings": [], "summary": "all good"}`,
			ExitCode: 0,
		}, nil
	})

	req := makeRequest("critique", map[string]any{
		"artifact": "SELECT * FROM users",
	})
	result, err := srv.handleCritique(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}

	data := parseResult(t, result)
	if _, ok := data["lens"]; ok {
		t.Error("expected no 'lens' key in payload when no lens specified")
	}
}

// TestHandleCritique_StructuredOutput verifies that structured JSON from the CLI
// is parsed correctly and returned with findings/summary/cli_used/tokens fields.
func TestHandleCritique_StructuredOutput(t *testing.T) {
	cliOutput := `{"findings": [{"severity": "high", "location": "line 5", "issue": "SQL injection", "suggested_fix": "use parameterized queries"}], "summary": "found 1 critical issue"}`

	srv := newCritiqueServer(t, func(_ context.Context, _ types.SpawnArgs) (*types.Result, error) {
		return &types.Result{Content: cliOutput, ExitCode: 0}, nil
	})

	req := makeRequest("critique", map[string]any{
		"artifact": "db.Query(\"SELECT * FROM users WHERE id=\" + userID)",
		"lens":     "security",
	})
	result, err := srv.handleCritique(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}

	data := parseResult(t, result)

	if data["summary"] != "found 1 critical issue" {
		t.Errorf("summary: got %q, want %q", data["summary"], "found 1 critical issue")
	}
	if data["lens"] != "security" {
		t.Errorf("lens: got %q, want security", data["lens"])
	}
	if data["cli_used"] == "" {
		t.Error("cli_used must be non-empty")
	}
	if _, ok := data["tokens"]; !ok {
		t.Error("tokens field missing")
	}

	findings, ok := data["findings"].([]any)
	if !ok {
		t.Fatalf("findings not a slice: %T", data["findings"])
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f, ok := findings[0].(map[string]any)
	if !ok {
		t.Fatalf("finding[0] not a map: %T", findings[0])
	}
	if f["severity"] != "high" {
		t.Errorf("severity: got %q, want high", f["severity"])
	}
}

// TestHandleCritique_MaxFindings verifies that results are capped at max_findings.
func TestHandleCritique_MaxFindings(t *testing.T) {
	// Produce 5 findings from the CLI.
	type finding struct {
		Severity    string `json:"severity"`
		Location    string `json:"location"`
		Issue       string `json:"issue"`
		SuggestedFix string `json:"suggested_fix"`
	}
	type response struct {
		Findings []finding `json:"findings"`
		Summary  string    `json:"summary"`
	}
	findings := make([]finding, 5)
	for i := range findings {
		findings[i] = finding{Severity: "low", Location: "line", Issue: "issue", SuggestedFix: "fix"}
	}
	raw, _ := json.Marshal(response{Findings: findings, Summary: "many issues"})

	srv := newCritiqueServer(t, func(_ context.Context, _ types.SpawnArgs) (*types.Result, error) {
		return &types.Result{Content: string(raw), ExitCode: 0}, nil
	})

	req := makeRequest("critique", map[string]any{
		"artifact":     "lots of code",
		"max_findings": float64(3),
	})
	result, err := srv.handleCritique(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}

	data := parseResult(t, result)
	got, ok := data["findings"].([]any)
	if !ok {
		t.Fatalf("findings not a slice: %T", data["findings"])
	}
	if len(got) != 3 {
		t.Errorf("expected 3 findings (capped), got %d", len(got))
	}
}

// TestHandleCritique_NonJSONFallback verifies that non-JSON CLI output is returned
// as a single "unknown" finding rather than an error.
func TestHandleCritique_NonJSONFallback(t *testing.T) {
	srv := newCritiqueServer(t, func(_ context.Context, _ types.SpawnArgs) (*types.Result, error) {
		return &types.Result{Content: "plain text output, no JSON here", ExitCode: 0}, nil
	})

	req := makeRequest("critique", map[string]any{
		"artifact": "some artifact",
	})
	result, err := srv.handleCritique(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error (fallback should not fail): %v", result.Content)
	}

	data := parseResult(t, result)
	findings, ok := data["findings"].([]any)
	if !ok || len(findings) != 1 {
		t.Fatalf("expected 1 fallback finding, got %T %v", data["findings"], data["findings"])
	}
	f, ok := findings[0].(map[string]any)
	if !ok {
		t.Fatalf("finding[0] not a map")
	}
	if f["severity"] != "unknown" {
		t.Errorf("fallback severity: got %q, want unknown", f["severity"])
	}
}

// TestHandleCritique_ExecutorError verifies that a CLI execution failure
// returns a tool error (not a Go error).
func TestHandleCritique_ExecutorError(t *testing.T) {
	srv := newCritiqueServer(t, func(_ context.Context, _ types.SpawnArgs) (*types.Result, error) {
		return nil, errors.New("subprocess exited with code 1")
	})

	req := makeRequest("critique", map[string]any{
		"artifact": "some code",
	})
	result, err := srv.handleCritique(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true when executor fails")
	}
}

// TestHandleCritique_CLIOverride verifies that the "cli" override parameter
// selects the named CLI profile instead of routing.
func TestHandleCritique_CLIOverride(t *testing.T) {
	var capturedCLI string

	srv := newCritiqueServer(t, func(_ context.Context, args types.SpawnArgs) (*types.Result, error) {
		capturedCLI = args.CLI
		return &types.Result{Content: `{"findings": [], "summary": "ok"}`, ExitCode: 0}, nil
	})

	req := makeRequest("critique", map[string]any{
		"artifact": "some code",
		"cli":      "gemini",
	})
	result, err := srv.handleCritique(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	if capturedCLI != "gemini" {
		t.Errorf("expected CLI=gemini, got %q", capturedCLI)
	}
}

// TestParseCritiqueOutput_WithPreamble verifies that parseCritiqueOutput handles
// CLI output that contains text before the JSON payload.
func TestParseCritiqueOutput_WithPreamble(t *testing.T) {
	output := `Here is the analysis:
{"findings": [{"severity": "medium", "location": "main.go:10", "issue": "error ignored", "suggested_fix": "handle error"}], "summary": "1 issue found"}`

	findings, summary, err := parseCritiqueOutput(output, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if summary != "1 issue found" {
		t.Errorf("summary: got %q, want %q", summary, "1 issue found")
	}
}

// TestParseCritiqueOutput_Empty verifies that empty output returns an error.
func TestParseCritiqueOutput_Empty(t *testing.T) {
	_, _, err := parseCritiqueOutput("", 10)
	if err == nil {
		t.Fatal("expected error for empty output")
	}
}

// TestParseCritiqueOutput_MaxFindings verifies truncation at the specified limit.
func TestParseCritiqueOutput_MaxFindings(t *testing.T) {
	output := `{"findings": [{"severity": "low", "location": "a", "issue": "1", "suggested_fix": ""}, {"severity": "low", "location": "b", "issue": "2", "suggested_fix": ""}, {"severity": "low", "location": "c", "issue": "3", "suggested_fix": ""}], "summary": "three"}`

	findings, _, err := parseCritiqueOutput(output, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 2 {
		t.Errorf("expected 2 (capped), got %d", len(findings))
	}
}
