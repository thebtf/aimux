package review

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const testReviewSecret = "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

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
	if !decision.ReviewComplete {
		t.Fatal("ReviewComplete = false, want true after all required passes")
	}
	if decision.ConfidenceScore != 1 {
		t.Fatalf("ConfidenceScore = %v, want 1 for complete review", decision.ConfidenceScore)
	}
	if len(decision.PassesCompleted) != 3 {
		t.Fatalf("passes_completed = %#v, want 3 passes", decision.PassesCompleted)
	}
	if runner.target != "HEAD~1..HEAD" {
		t.Fatalf("target = %q, want HEAD~1..HEAD", runner.target)
	}
}

func TestGateRunGateBoundsAndRedactsAllowSummaryReason(t *testing.T) {
	runner := &fakePassRunner{results: []PassResult{
		{Name: PassStructural, Summary: "structure clean " + testReviewSecret + " " + strings.Repeat("界", 300)},
		{Name: PassBehavioural, Summary: "behaviour clean"},
		{Name: PassAdversarial, Summary: "adversarial clean"},
	}}

	decision, err := NewGate(runner, Criteria{}).RunGate(context.Background(), "HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionAllow || !decision.ReviewComplete || decision.ConfidenceScore != 1 {
		t.Fatalf("decision = %#v, want complete allow", decision)
	}
	assertSafeBoundedReviewReason(t, decision.Reason)
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
			{Name: PassBehavioural, Summary: "behaviour clean"},
			{Name: PassAdversarial, Summary: "adversarial clean"},
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
	if !decision.ReviewComplete {
		t.Fatal("ReviewComplete = false, want true when all passes completed with findings")
	}
	if decision.ConfidenceScore != 1 {
		t.Fatalf("ConfidenceScore = %v, want 1 for complete blocking review", decision.ConfidenceScore)
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

func TestGateRunGateBoundsAndRedactsBlockingFindingReason(t *testing.T) {
	runner := &fakePassRunner{results: []PassResult{
		{Name: PassStructural, Summary: "blocking finding", Findings: []Finding{{
			Severity: SeverityError,
			File:     "security.go",
			Body:     "credential leaked " + testReviewSecret + " " + strings.Repeat("界", 300),
		}}},
		{Name: PassBehavioural, Summary: "behaviour clean"},
		{Name: PassAdversarial, Summary: "adversarial clean"},
	}}

	decision, err := NewGate(runner, Criteria{}).RunGate(context.Background(), "HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionBlock || !decision.Blocking || !decision.ReviewComplete || decision.ConfidenceScore != 1 {
		t.Fatalf("decision = %#v, want complete blocking review", decision)
	}
	assertSafeBoundedReviewReason(t, decision.Reason)
}

func TestGateRunGateBlocksOnPipelineError(t *testing.T) {
	runner := &fakePassRunner{err: errors.New("all review backends unavailable after retries")}

	decision, err := NewGate(runner, Criteria{}).RunGate(context.Background(), "HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionBlock {
		t.Fatalf("decision = %s, want fail-closed %s", decision.Decision, DecisionBlock)
	}
	if !decision.Blocking {
		t.Fatal("Blocking = false, want true when the review pipeline is unavailable")
	}
	if decision.ReviewComplete {
		t.Fatal("ReviewComplete = true, want false when the review pipeline is unavailable")
	}
	if decision.ConfidenceScore != 0 {
		t.Fatalf("ConfidenceScore = %v, want 0 when review is unavailable", decision.ConfidenceScore)
	}
	if len(decision.PassesCompleted) != 0 {
		t.Fatalf("passes_completed = %#v, want none", decision.PassesCompleted)
	}
	if !strings.Contains(decision.Reason, "all review backends unavailable after retries") {
		t.Fatalf("reason = %q, want pipeline error", decision.Reason)
	}
	if decision.Summary != decision.Reason {
		t.Fatalf("summary = %q, want operational failure summary %q", decision.Summary, decision.Reason)
	}
}

func TestGateRunGateBoundsAndRedactsPipelineErrorReason(t *testing.T) {
	rawError := "review backend rejected credentials " + testReviewSecret + " " + strings.Repeat("界", 300)
	runner := &fakePassRunner{err: errors.New(rawError)}

	decision, err := NewGate(runner, Criteria{}).RunGate(context.Background(), "HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionBlock || !decision.Blocking || decision.ReviewComplete || decision.ConfidenceScore != 0 {
		t.Fatalf("decision = %#v, want incomplete fail-closed block", decision)
	}
	assertSafeBoundedReviewReason(t, decision.Reason)
	if decision.Summary != decision.Reason {
		t.Fatalf("summary = %q, want sanitized bounded reason %q", decision.Summary, decision.Reason)
	}
}

func TestGateRunGateBlocksOnTimeout(t *testing.T) {
	runner := &fakePassRunner{wait: true}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	decision, err := NewGate(runner, Criteria{}).RunGate(ctx, "HEAD", 300)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionBlock {
		t.Fatalf("decision = %s, want fail-closed %s", decision.Decision, DecisionBlock)
	}
	if decision.Reason != "timeout" {
		t.Fatalf("reason = %q, want timeout", decision.Reason)
	}
	if !decision.Blocking {
		t.Fatal("Blocking = false, want true on timeout")
	}
	if decision.ReviewComplete {
		t.Fatal("ReviewComplete = true, want false on timeout")
	}
	if decision.ConfidenceScore != 0 {
		t.Fatalf("ConfidenceScore = %v, want 0 on timeout", decision.ConfidenceScore)
	}
}

func TestGateRunGateBlocksWhenNoPassCompletes(t *testing.T) {
	decision, err := NewGate(&fakePassRunner{}, Criteria{}).RunGate(context.Background(), "HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionBlock || !decision.Blocking {
		t.Fatalf("decision = %#v, want fail-closed block", decision)
	}
	if decision.ReviewComplete {
		t.Fatal("ReviewComplete = true, want false with zero passes")
	}
	if len(decision.PassesCompleted) != 0 {
		t.Fatalf("passes_completed = %#v, want none", decision.PassesCompleted)
	}
	if !strings.Contains(decision.Reason, "zero passes") {
		t.Fatalf("reason = %q, want zero-pass failure", decision.Reason)
	}
	if strings.Contains(decision.Summary, "No review findings") {
		t.Fatalf("summary = %q, must not imply a clean review", decision.Summary)
	}
}

func TestGateRunGateBlocksOnIncompletePassSet(t *testing.T) {
	runner := &fakePassRunner{results: []PassResult{
		{Name: PassStructural, Summary: "structure clean"},
		{Name: PassBehavioural, Summary: "behaviour clean"},
	}}

	decision, err := NewGate(runner, Criteria{}).RunGate(context.Background(), "HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionBlock || !decision.Blocking {
		t.Fatalf("decision = %#v, want fail-closed block", decision)
	}
	if decision.ReviewComplete {
		t.Fatal("ReviewComplete = true, want false for incomplete pass set")
	}
	if len(decision.PassesCompleted) != 2 {
		t.Fatalf("passes_completed = %#v, want completed passes preserved", decision.PassesCompleted)
	}
	if !strings.Contains(decision.Reason, "adversarial") {
		t.Fatalf("reason = %q, want missing pass named", decision.Reason)
	}
}

func TestGateRunGateRejectsDuplicatePassIdentity(t *testing.T) {
	runner := &fakePassRunner{results: []PassResult{
		{Name: PassStructural, Summary: "first structural result"},
		{Name: PassStructural, Summary: "duplicate structural result"},
		{Name: PassBehavioural, Summary: "behaviour clean"},
	}}

	decision, err := NewGate(runner, Criteria{}).RunGate(context.Background(), "HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionBlock || !decision.Blocking || decision.ReviewComplete {
		t.Fatalf("decision = %#v, want incomplete fail-closed block", decision)
	}
	if !strings.Contains(decision.Reason, "adversarial") {
		t.Fatalf("reason = %q, want missing mandatory pass identity", decision.Reason)
	}
}

func TestGateRunGatePreservesPartialResultsWhenLaterPassFails(t *testing.T) {
	runner := &fakePassRunner{
		results: []PassResult{{Name: PassStructural, Summary: "structure clean"}},
		err:     errors.New("behavioural review pass: Auth(AuthorizationRequired) after retries"),
	}

	decision, err := NewGate(runner, Criteria{}).RunGate(context.Background(), "HEAD", 30)
	if err != nil {
		t.Fatalf("RunGate error = %v, want nil", err)
	}
	if decision.Decision != DecisionBlock || !decision.Blocking {
		t.Fatalf("decision = %#v, want fail-closed block", decision)
	}
	if decision.ReviewComplete {
		t.Fatal("ReviewComplete = true, want false after a later pass fails")
	}
	if len(decision.PassesCompleted) != 1 || decision.PassesCompleted[0] != PassStructural {
		t.Fatalf("passes_completed = %#v, want structural partial result", decision.PassesCompleted)
	}
	if !strings.Contains(decision.Reason, "Auth(AuthorizationRequired)") {
		t.Fatalf("reason = %q, want original worker error", decision.Reason)
	}
	if !strings.Contains(decision.Summary, "structural: structure clean") {
		t.Fatalf("summary = %q, want successful partial pass summary preserved", decision.Summary)
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

func TestParseGateDecisionPreservesAllowFixture(t *testing.T) {
	decision, reason := ParseGateDecision(`{"findings":[],"summary":"clean","decision":"ALLOW","reason":"all checks passed"}`)
	if decision != "allow" {
		t.Fatalf("decision = %q, want allow", decision)
	}
	if reason != "all checks passed" {
		t.Fatalf("reason = %q, want all checks passed", reason)
	}
}

func TestParseGateDecisionBoundsAndRedactsLegacyReason(t *testing.T) {
	rawReason := "legacy reviewer reason " + testReviewSecret + " " + strings.Repeat("界", 300)
	input := "{\"decision\":\"ALLOW\",\"reason\":\"" + rawReason + "\"}"
	decision, reason := ParseGateDecision(input)
	if decision != "allow" {
		t.Fatalf("decision = %q, want allow", decision)
	}
	assertSafeBoundedReviewReason(t, reason)
}

func TestParseGateDecisionFailsClosedOnInvalidOutput(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "missing JSON", content: "some random output without a decision"},
		{name: "malformed JSON", content: `{"decision":"ALLOW"`},
		{name: "unknown decision", content: `{"decision":"MAYBE"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, reason := ParseGateDecision(tt.content)
			if decision != "block" {
				t.Fatalf("decision = %q, want fail-closed block", decision)
			}
			if reason == "" {
				t.Fatal("reason is empty, want parse failure detail")
			}
		})
	}
}

func assertSafeBoundedReviewReason(t *testing.T, reason string) {
	t.Helper()
	if strings.Contains(reason, testReviewSecret) {
		t.Fatalf("reason leaked raw secret: %q", reason)
	}
	if !strings.Contains(reason, "[REDACTED:openai-key-project]") {
		t.Fatalf("reason = %q, want redaction marker", reason)
	}
	if len(reason) > PublicReasonMaxBytes {
		t.Fatalf("reason length = %d bytes, want <= %d", len(reason), PublicReasonMaxBytes)
	}
	if !strings.HasSuffix(reason, PublicReasonTruncationMarker) {
		t.Fatalf("reason = %q, want explicit truncation marker", reason)
	}
	if !utf8.ValidString(reason) {
		t.Fatalf("reason is not valid UTF-8: %q", reason)
	}
}
