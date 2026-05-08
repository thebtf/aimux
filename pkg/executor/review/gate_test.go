package review

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakePassRunner struct {
	results []PassResult
	err     error
	wait    bool
	target  string
}

func (f *fakePassRunner) Run(ctx context.Context, target string, _ Criteria) ([]PassResult, error) {
	f.target = target
	if f.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.results, f.err
}

func TestGateRunGateAllowPath(t *testing.T) {
	runner := &fakePassRunner{
		results: []PassResult{
			{Name: PassStructural, Summary: "structure clean"},
			{Name: PassBehavioural, Summary: "warning only", Findings: []Finding{
				{Severity: SeverityWarning, Body: "missing edge case assertion"},
			}},
			{Name: PassAdversarial, Summary: "no hostile-input issues"},
		},
	}

	decision, err := NewGate(runner, Criteria{}).RunGate(context.Background(), "HEAD~1..HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionAllow {
		t.Fatalf("decision = %s, want %s", decision.Decision, DecisionAllow)
	}
	if decision.Blocking {
		t.Fatal("Blocking = true, want false for warning-only aggregate")
	}
	if len(decision.PassesCompleted) != 3 {
		t.Fatalf("passes_completed = %#v, want 3 passes", decision.PassesCompleted)
	}
	if runner.target != "HEAD~1..HEAD" {
		t.Fatalf("target = %q, want HEAD~1..HEAD", runner.target)
	}
}

func TestGateRunGateBlockPathIncludesTopThreeErrors(t *testing.T) {
	runner := &fakePassRunner{
		results: []PassResult{
			{Name: PassStructural, Summary: "errors", Findings: []Finding{
				{Severity: SeverityError, File: "a.go", Body: "first"},
				{Severity: SeverityError, File: "b.go", Body: "second"},
				{Severity: SeverityError, File: "c.go", Body: "third"},
				{Severity: SeverityError, File: "d.go", Body: "fourth"},
			}},
		},
	}

	decision, err := NewGate(runner, Criteria{}).RunGate(context.Background(), "HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionBlock {
		t.Fatalf("decision = %s, want %s", decision.Decision, DecisionBlock)
	}
	if !decision.Blocking {
		t.Fatal("Blocking = false, want true for error aggregate")
	}
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(decision.Reason, want) {
			t.Fatalf("reason = %q, want it to include %q", decision.Reason, want)
		}
	}
	if strings.Contains(decision.Reason, "fourth") {
		t.Fatalf("reason = %q, want only top 3 errors", decision.Reason)
	}
}

func TestGateRunGateFailOpenOnPipelineError(t *testing.T) {
	runner := &fakePassRunner{err: errors.New("review pipeline unavailable")}

	decision, err := NewGate(runner, Criteria{}).RunGate(context.Background(), "HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionAllow {
		t.Fatalf("decision = %s, want fail-open %s", decision.Decision, DecisionAllow)
	}
	if !strings.Contains(decision.Reason, "review pipeline unavailable") {
		t.Fatalf("reason = %q, want pipeline error", decision.Reason)
	}
}

func TestGateRunGateFailOpenOnTimeout(t *testing.T) {
	runner := &fakePassRunner{wait: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	decision, err := NewGate(runner, Criteria{}).RunGate(ctx, "HEAD", 300)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionAllow {
		t.Fatalf("decision = %s, want fail-open %s", decision.Decision, DecisionAllow)
	}
	if decision.Reason != "timeout" {
		t.Fatalf("reason = %q, want timeout", decision.Reason)
	}
}

func TestGateRunGateReturnsErrorOnCancellation(t *testing.T) {
	runner := &fakePassRunner{wait: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	decision, err := NewGate(runner, Criteria{}).RunGate(ctx, "HEAD", 300)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunGate error = %v, want context.Canceled", err)
	}
	if decision.Decision != "" {
		t.Fatalf("decision = %#v, want zero decision on cancellation", decision)
	}
}

func TestParseGateDecisionPreservesBlockFixture(t *testing.T) {
	input := `{"findings":[],"summary":"critical issue","decision":"BLOCK","reason":"critical security issue found"}`
	decision, reason := ParseGateDecision(input)
	if decision != "block" {
		t.Fatalf("decision = %q, want block", decision)
	}
	if reason != "critical security issue found" {
		t.Fatalf("reason = %q, want critical security issue found", reason)
	}
}

func TestParseGateDecisionPreservesBlockWithTrailingText(t *testing.T) {
	input := `preamble {"findings":[],"summary":"critical issue","decision":"BLOCK","reason":"must fix"} trailing note`
	decision, reason := ParseGateDecision(input)
	if decision != "block" {
		t.Fatalf("decision = %q, want block", decision)
	}
	if reason != "must fix" {
		t.Fatalf("reason = %q, want must fix", reason)
	}
}

func TestParseGateDecisionFailOpenOnInvalidOutput(t *testing.T) {
	decision, reason := ParseGateDecision("some random output without a decision")
	if decision != "allow" {
		t.Fatalf("decision = %q, want allow", decision)
	}
	if reason == "" {
		t.Fatal("reason is empty, want parse failure detail")
	}
}
