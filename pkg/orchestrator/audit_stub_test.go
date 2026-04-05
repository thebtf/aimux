package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/types"
)

// TestAuditStub_ScannerPromptContainsStubRules verifies the audit scanner
// prompt includes all 8 STUB-* rule IDs for the stubs-quality category.
func TestAuditStub_ScannerPromptContainsStubRules(t *testing.T) {
	var capturedPrompt string

	scannerMock := &promptCapturingExecutor{
		capturedPrompt: &capturedPrompt,
		response: "FINDING: [HIGH] STUB-DISCARD — computed value discarded (main.go:42)\n" +
			"FINDING: [CRITICAL] STUB-PASSTHROUGH — params built then discarded (server.go:456)\n",
	}

	audit := orchestrator.NewAuditPipeline(scannerMock)

	_, err := audit.Execute(context.Background(), types.StrategyParams{
		Prompt: "Audit codebase",
		CLIs:   []string{"codex"},
		CWD:    "/tmp/test",
		Extra: map[string]any{
			"mode":              "quick",
			"parallel_scanners": 1,
		},
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify scanner prompt contains STUB-* rule IDs
	stubRules := []string{
		"STUB-DISCARD",
		"STUB-HARDCODED",
		"STUB-TODO",
		"STUB-NOOP",
		"STUB-PASSTHROUGH",
		"STUB-TEST-STRUCTURAL",
		"STUB-COVERAGE-ZERO",
		"STUB-INTERFACE-EMPTY",
	}

	for _, rule := range stubRules {
		if !strings.Contains(capturedPrompt, rule) {
			t.Errorf("scanner prompt missing rule %q", rule)
		}
	}
}

// TestAuditStub_FindingsContainStubRuleIDs verifies that stub findings
// from scanner output are parsed with STUB-* rule IDs preserved.
func TestAuditStub_FindingsContainStubRuleIDs(t *testing.T) {
	scannerMock := &mockExecutor{
		runResult: &types.Result{
			Content: "FINDING: [HIGH] STUB-DISCARD — _ = pairParams in server.go (server.go:456)\n" +
				"FINDING: [CRITICAL] STUB-PASSTHROUGH — params computed then discarded (server.go:458)\n" +
				"FINDING: [MEDIUM] STUB-TODO — TODO comment in implementation (utils.go:12)\n",
			ExitCode: 0,
		},
	}

	audit := orchestrator.NewAuditPipeline(scannerMock)

	result, err := audit.Execute(context.Background(), types.StrategyParams{
		Prompt: "Audit codebase",
		CLIs:   []string{"codex"},
		CWD:    "/tmp/test",
		Extra: map[string]any{
			"mode":              "quick",
			"parallel_scanners": 1,
		},
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Result should contain the STUB-* findings
	if !strings.Contains(result.Content, "STUB-DISCARD") {
		t.Error("result missing STUB-DISCARD finding")
	}
	if !strings.Contains(result.Content, "STUB-PASSTHROUGH") {
		t.Error("result missing STUB-PASSTHROUGH finding")
	}
	if !strings.Contains(result.Content, "CRITICAL") {
		t.Error("result missing CRITICAL severity for STUB-PASSTHROUGH")
	}
}

// TestAuditStub_CleanCodeZeroFindings verifies clean code produces no stub findings.
func TestAuditStub_CleanCodeZeroFindings(t *testing.T) {
	scannerMock := &mockExecutor{
		runResult: &types.Result{
			Content:  "Scan complete. No findings.",
			ExitCode: 0,
		},
	}

	audit := orchestrator.NewAuditPipeline(scannerMock)

	result, err := audit.Execute(context.Background(), types.StrategyParams{
		Prompt: "Audit codebase",
		CLIs:   []string{"codex"},
		CWD:    "/tmp/test",
		Extra: map[string]any{
			"mode":              "quick",
			"parallel_scanners": 1,
		},
	})

	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if strings.Contains(result.Content, "STUB-") {
		t.Error("clean code should have zero STUB-* findings")
	}
	if !strings.Contains(result.Content, "Findings: 0") {
		t.Logf("Result: %s", result.Content[:min(200, len(result.Content))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
