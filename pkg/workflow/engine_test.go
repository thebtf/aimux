package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/dialogue"
	"github.com/thebtf/aimux/pkg/types"
)

// --- mock implementations ---

// mockHandle is a trivial ExecutorHandle.
type mockHandle struct{ name string }

func (h *mockHandle) ExecutorName() string { return h.name }

// mockSender implements ExecutorSender with a configurable response map.
// Key is executor name → response content (or error if prefixed with "ERR:").
type mockSender struct {
	responses map[string]string // executor name → content or "ERR:<msg>"
}

func (m *mockSender) Get(_ context.Context, name string) (ExecutorHandle, error) {
	if m.responses == nil {
		return &mockHandle{name: name}, nil
	}
	if v, ok := m.responses[name]; ok && strings.HasPrefix(v, "ERR:") {
		return nil, fmt.Errorf("sender get: %s", strings.TrimPrefix(v, "ERR:"))
	}
	return &mockHandle{name: name}, nil
}

func (m *mockSender) Send(_ context.Context, h ExecutorHandle, _ types.Message) (*types.Response, error) {
	name := h.ExecutorName()
	if m.responses != nil {
		if v, ok := m.responses[name]; ok {
			if strings.HasPrefix(v, "ERR:") {
				return nil, fmt.Errorf("sender send: %s", strings.TrimPrefix(v, "ERR:"))
			}
			return &types.Response{Content: v}, nil
		}
	}
	return &types.Response{Content: fmt.Sprintf("[%s response]", name)}, nil
}

// mockParticipant implements dialogue.Participant with a fixed response.
type mockParticipant struct {
	name    string
	role    string
	content string
}

func (p *mockParticipant) Name() string { return p.name }
func (p *mockParticipant) Role() string { return p.role }
func (p *mockParticipant) Respond(_ context.Context, _ string, _ []dialogue.DialogueTurn) (string, error) {
	return p.content, nil
}

// mockDialogueRunner implements DialogueRunner backed by a real dialogue.Controller
// but with participant overrides handled by ParticipantFactory.
// We use the real Controller for correctness and drive it directly.
type mockDialogueRunner struct {
	ctrl        *dialogue.Controller
	synthResult string // override synthesized content
}

func newMockDialogueRunner(synthResult string) *mockDialogueRunner {
	return &mockDialogueRunner{
		ctrl:        dialogue.New(),
		synthResult: synthResult,
	}
}

func (r *mockDialogueRunner) NewDialogue(cfg dialogue.DialogueConfig) (*dialogue.Dialogue, error) {
	return r.ctrl.NewDialogue(cfg)
}

func (r *mockDialogueRunner) NextTurn(ctx context.Context, d *dialogue.Dialogue) ([]dialogue.DialogueTurn, error) {
	return r.ctrl.NextTurn(ctx, d)
}

func (r *mockDialogueRunner) Synthesize(ctx context.Context, d *dialogue.Dialogue) (*dialogue.Synthesis, error) {
	syn, err := r.ctrl.Synthesize(ctx, d)
	if err != nil {
		return nil, err
	}
	if r.synthResult != "" {
		syn.Content = r.synthResult
	}
	return syn, nil
}

func (r *mockDialogueRunner) Close(d *dialogue.Dialogue) error {
	return r.ctrl.Close(d)
}

// mockPatternFn is a configurable think pattern dispatcher.
// Maps pattern name → result content.
type mockPatternFn struct {
	results map[string]string // pattern name → result content or "ERR:<msg>"
}

func (m *mockPatternFn) dispatch(name string, _ map[string]any) (map[string]any, error) {
	if m.results != nil {
		if v, ok := m.results[name]; ok {
			if strings.HasPrefix(v, "ERR:") {
				return nil, fmt.Errorf("pattern error: %s", strings.TrimPrefix(v, "ERR:"))
			}
			return map[string]any{"content": v}, nil
		}
	}
	return map[string]any{"content": fmt.Sprintf("[%s result]", name)}, nil
}

// participantFactory produces mockParticipants for dialogue steps.
func participantFactory(responses map[string]string) ParticipantFactory {
	return func(name string) (dialogue.Participant, error) {
		content := fmt.Sprintf("[%s dialogue response]", name)
		if responses != nil {
			if v, ok := responses[name]; ok {
				content = v
			}
		}
		return &mockParticipant{name: name, role: "reviewer", content: content}, nil
	}
}

// --- helpers ---

func newTestEngine(
	senderResponses map[string]string,
	synthResult string,
	patternResults map[string]string,
	partResponses map[string]string,
) *Engine {
	pf := &mockPatternFn{results: patternResults}
	return New(
		&mockSender{responses: senderResponses},
		newMockDialogueRunner(synthResult),
		pf.dispatch,
		participantFactory(partResponses),
	)
}

// --- tests ---

func TestEngine_Execute_EmptySteps(t *testing.T) {
	e := newTestEngine(nil, "", nil, nil)
	result, err := e.Execute(context.Background(), []WorkflowStep{}, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %q; want %q", result.Status, "completed")
	}
	if len(result.Steps) != 0 {
		t.Errorf("steps = %d; want 0", len(result.Steps))
	}
}

func TestEngine_Execute_SingleExec(t *testing.T) {
	e := newTestEngine(
		map[string]string{"analyze": "analysis output"},
		"", nil, nil,
	)

	steps := []WorkflowStep{
		{
			Name:   "analyze",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Analyze: %s",
			},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{Topic: "my code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %q; want %q", result.Status, "completed")
	}
	if len(result.Steps) != 1 {
		t.Fatalf("steps len = %d; want 1", len(result.Steps))
	}
	sr := result.Steps[0]
	if sr.Name != "analyze" {
		t.Errorf("step name = %q; want %q", sr.Name, "analyze")
	}
	if sr.Status != "completed" {
		t.Errorf("step status = %q; want %q", sr.Status, "completed")
	}
	if sr.Content != "analysis output" {
		t.Errorf("step content = %q; want %q", sr.Content, "analysis output")
	}
}

func TestEngine_Execute_Dialogue(t *testing.T) {
	synthContent := "synthesized dialogue output"
	e := newTestEngine(
		nil,
		synthContent,
		nil,
		map[string]string{
			"codex":  "codex review",
			"claude": "claude review",
		},
	)

	steps := []WorkflowStep{
		{
			Name:   "review",
			Action: ActionDialogue,
			Config: map[string]any{
				"participants": []string{"codex", "claude"},
				"mode":         "parallel",
				"max_turns":    2,
				"prompt":       "Review this: %s",
			},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{Topic: "code review input"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %q; want %q", result.Status, "completed")
	}
	if len(result.Steps) != 1 {
		t.Fatalf("steps len = %d; want 1", len(result.Steps))
	}
	if result.Steps[0].Content != synthContent {
		t.Errorf("step content = %q; want %q", result.Steps[0].Content, synthContent)
	}
}

func TestEngine_Execute_ThinkPattern(t *testing.T) {
	e := newTestEngine(
		nil,
		"",
		map[string]string{"decision_framework": "framework assessment result"},
		nil,
	)

	steps := []WorkflowStep{
		{
			Name:   "assess",
			Action: ActionThinkPattern,
			Config: map[string]any{
				"pattern":   "decision_framework",
				"input_key": "findings",
			},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{Topic: "decision topic"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %q; want %q", result.Status, "completed")
	}
	if len(result.Steps) != 1 {
		t.Fatalf("steps len = %d; want 1", len(result.Steps))
	}
	if result.Steps[0].Content != "framework assessment result" {
		t.Errorf("step content = %q; want %q", result.Steps[0].Content, "framework assessment result")
	}
}

func TestEngine_Execute_GateFails(t *testing.T) {
	// The analyze step returns output containing "CRITICAL" — gate must block.
	e := newTestEngine(
		map[string]string{"analyze": "Found CRITICAL security vulnerability in authentication module"},
		"", nil, nil,
	)

	steps := []WorkflowStep{
		{
			Name:   "analyze",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "analyze",
				"prompt": "Analyze: %s",
			},
		},
		{
			Name:   "gate",
			Action: ActionGate,
			Config: map[string]any{"require": "no_critical_issues"},
		},
		{
			Name:   "validate",
			Action: ActionSingleExec,
			Config: map[string]any{
				"role":   "codereview",
				"prompt": "Validate: %s",
			},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "gated" {
		t.Errorf("status = %q; want %q", result.Status, "gated")
	}
	// Validate step must NOT have run.
	for _, sr := range result.Steps {
		if sr.Name == "validate" {
			t.Error("validate step should not have executed after gate failed")
		}
	}
	// Gate step result must be present.
	var foundGate bool
	for _, sr := range result.Steps {
		if sr.Name == "gate" {
			foundGate = true
			if sr.Status != "gated" {
				t.Errorf("gate step status = %q; want %q", sr.Status, "gated")
			}
		}
	}
	if !foundGate {
		t.Error("gate step result not found in results")
	}
}

func TestEngine_Execute_GatePasses(t *testing.T) {
	// Output without "CRITICAL" — gate passes, validate runs.
	e := newTestEngine(
		map[string]string{
			"analyze":    "minor style issues found",
			"codereview": "final validated assessment",
		},
		"", nil, nil,
	)

	steps := []WorkflowStep{
		{
			Name:   "analyze",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "analyze", "prompt": "Analyze: %s"},
		},
		{
			Name:   "gate",
			Action: ActionGate,
			Config: map[string]any{"require": "no_critical_issues"},
		},
		{
			Name:   "validate",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "codereview", "prompt": "Validate: %s"},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %q; want %q", result.Status, "completed")
	}
	if len(result.Steps) != 3 {
		t.Errorf("steps len = %d; want 3", len(result.Steps))
	}
}

func TestEngine_Execute_StepFails(t *testing.T) {
	e := newTestEngine(
		map[string]string{"analyze": "ERR:executor crashed"},
		"", nil, nil,
	)

	steps := []WorkflowStep{
		{
			Name:   "analyze",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "analyze", "prompt": "Analyze: %s"},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %q; want %q", result.Status, "failed")
	}
	if len(result.Steps) != 1 {
		t.Fatalf("steps len = %d; want 1", len(result.Steps))
	}
	if result.Steps[0].Status != "failed" {
		t.Errorf("step status = %q; want %q", result.Steps[0].Status, "failed")
	}
}

func TestEngine_Execute_AllSteps(t *testing.T) {
	// Full 5-step CodeReview workflow with all mocks wired.
	synthContent := "dialogue synthesis: no critical issues found"
	e := newTestEngine(
		map[string]string{
			"analyze":    "code looks mostly clean, minor naming issues",
			"codereview": "final assessment: code approved with minor suggestions",
		},
		synthContent,
		map[string]string{"decision_framework": "decision: approve with comments"},
		map[string]string{
			"codex":  "codex: no serious issues",
			"claude": "claude: logic looks correct",
		},
	)

	steps := CodeReviewSteps()
	result, err := e.Execute(context.Background(), steps, WorkflowInput{
		Topic: "review my Go code",
		Files: []string{"main.go", "handler.go"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %q; want %q", result.Status, "completed")
	}
	if len(result.Steps) != 5 {
		t.Errorf("steps len = %d; want 5", len(result.Steps))
	}

	nameOrder := []string{"analyze", "review", "assess", "gate", "validate"}
	for i, sr := range result.Steps {
		if sr.Name != nameOrder[i] {
			t.Errorf("step[%d].Name = %q; want %q", i, sr.Name, nameOrder[i])
		}
		if sr.Status != "completed" {
			t.Errorf("step[%d].Status = %q; want %q", i, sr.Status, "completed")
		}
		if sr.Duration < 0 {
			t.Errorf("step[%d].Duration is negative", i)
		}
	}

	if result.Summary == "" {
		t.Error("summary should not be empty")
	}
}

func TestEngine_Execute_AllSteps_GateBlocks(t *testing.T) {
	// Analyze step returns CRITICAL — gate must block the validate step.
	e := newTestEngine(
		map[string]string{"analyze": "CRITICAL: SQL injection vulnerability found"},
		"synthesis with CRITICAL mention",
		map[string]string{"decision_framework": "CRITICAL issues must be fixed"},
		map[string]string{
			"codex":  "codex: CRITICAL SQL injection",
			"claude": "claude: CRITICAL flaw",
		},
	)

	steps := CodeReviewSteps()
	result, err := e.Execute(context.Background(), steps, WorkflowInput{Topic: "review vulnerable code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "gated" {
		t.Errorf("status = %q; want %q", result.Status, "gated")
	}
	// Steps up to and including gate should be recorded.
	if len(result.Steps) < 4 {
		t.Errorf("expected at least 4 steps (analyze, review, assess, gate); got %d", len(result.Steps))
	}
	// validate must not appear.
	for _, sr := range result.Steps {
		if sr.Name == "validate" {
			t.Error("validate step must not run when gated")
		}
	}
}

func TestEngine_Execute_ContextCancelled(t *testing.T) {
	// Use a sender that respects context cancellation.
	cancelled := &cancelSender{}
	e := New(cancelled, newMockDialogueRunner(""), nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	steps := []WorkflowStep{
		{
			Name:   "analyze",
			Action: ActionSingleExec,
			Config: map[string]any{"cli": "codex"},
		},
	}

	result, err := e.Execute(ctx, steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	// The step must have failed due to context cancellation.
	if result.Status != "failed" {
		t.Errorf("status = %q; want %q", result.Status, "failed")
	}
}

// cancelSender returns context.Canceled from Send.
type cancelSender struct{}

func (c *cancelSender) Get(_ context.Context, name string) (ExecutorHandle, error) {
	return &mockHandle{name: name}, nil
}
func (c *cancelSender) Send(ctx context.Context, _ ExecutorHandle, _ types.Message) (*types.Response, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return &types.Response{Content: "ok"}, nil
}

func TestEngine_Execute_Parallel(t *testing.T) {
	e := newTestEngine(
		map[string]string{
			"codex":  "codex parallel response",
			"claude": "claude parallel response",
		},
		"", nil, nil,
	)

	steps := []WorkflowStep{
		{
			Name:   "parallel_review",
			Action: ActionParallel,
			Config: map[string]any{
				"clis":   []string{"codex", "claude"},
				"prompt": "Review: %s",
			},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{Topic: "parallel test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %q; want %q", result.Status, "completed")
	}
	content := result.Steps[0].Content
	if !strings.Contains(content, "codex parallel response") {
		t.Errorf("parallel content missing codex response: %q", content)
	}
	if !strings.Contains(content, "claude parallel response") {
		t.Errorf("parallel content missing claude response: %q", content)
	}
}

func TestEngine_Execute_StepDurationRecorded(t *testing.T) {
	e := newTestEngine(
		map[string]string{"analyze": "output"},
		"", nil, nil,
	)

	steps := []WorkflowStep{
		{
			Name:   "analyze",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "analyze"},
		},
	}

	result, _ := e.Execute(context.Background(), steps, WorkflowInput{})
	if len(result.Steps) == 0 {
		t.Fatal("no steps recorded")
	}
	if result.Steps[0].Duration < 0 {
		t.Error("duration must be >= 0")
	}
}

func TestCodeReviewSteps_Structure(t *testing.T) {
	steps := CodeReviewSteps()

	if len(steps) != 5 {
		t.Fatalf("CodeReviewSteps returned %d steps; want 5", len(steps))
	}

	expected := []struct {
		name   string
		action StepAction
	}{
		{"analyze", ActionSingleExec},
		{"review", ActionDialogue},
		{"assess", ActionThinkPattern},
		{"gate", ActionGate},
		{"validate", ActionSingleExec},
	}

	for i, want := range expected {
		got := steps[i]
		if got.Name != want.name {
			t.Errorf("step[%d].Name = %q; want %q", i, got.Name, want.name)
		}
		if got.Action != want.action {
			t.Errorf("step[%d].Action = %v; want %v", i, got.Action, want.action)
		}
		if got.Config == nil {
			t.Errorf("step[%d].Config is nil", i)
		}
	}

	// Verify critical config fields.
	reviewStep := steps[1]
	parts, err := configStringSlice(reviewStep.Config, "participants")
	if err != nil || len(parts) != 2 {
		t.Errorf("review step participants: got %v, err %v; want 2 participants", parts, err)
	}

	gateStep := steps[3]
	if configString(gateStep.Config, "require") != "no_critical_issues" {
		t.Errorf("gate step require = %q; want %q", configString(gateStep.Config, "require"), "no_critical_issues")
	}

	assessStep := steps[2]
	if configString(assessStep.Config, "pattern") != "decision_framework" {
		t.Errorf("assess step pattern = %q; want %q", configString(assessStep.Config, "pattern"), "decision_framework")
	}
}

func TestEngine_PatternFn_Error(t *testing.T) {
	e := newTestEngine(
		nil, "",
		map[string]string{"decision_framework": "ERR:pattern crashed"},
		nil,
	)

	steps := []WorkflowStep{
		{
			Name:   "assess",
			Action: ActionThinkPattern,
			Config: map[string]any{"pattern": "decision_framework"},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %q; want %q", result.Status, "failed")
	}
}

func TestEngine_MissingExecutorName(t *testing.T) {
	e := newTestEngine(nil, "", nil, nil)

	steps := []WorkflowStep{
		{
			Name:   "bad_step",
			Action: ActionSingleExec,
			Config: map[string]any{},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %q; want %q", result.Status, "failed")
	}
}

func TestEngine_UnknownAction(t *testing.T) {
	e := newTestEngine(nil, "", nil, nil)

	steps := []WorkflowStep{
		{
			Name:   "unknown",
			Action: StepAction(99),
			Config: map[string]any{},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %q; want %q", result.Status, "failed")
	}
}

func TestEngine_UnknownGateCondition(t *testing.T) {
	// Unknown condition passes (conservative default).
	e := newTestEngine(
		map[string]string{"codereview": "validated"},
		"", nil, nil,
	)

	steps := []WorkflowStep{
		{
			Name:   "gate",
			Action: ActionGate,
			Config: map[string]any{"require": "unknown_condition"},
		},
		{
			Name:   "validate",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "codereview"},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("status = %q; want %q", result.Status, "completed")
	}
}

func TestEngine_Execute_StepTimeout(t *testing.T) {
	// Sender that blocks until context is done.
	slow := &slowSender{delay: 100 * time.Millisecond}
	e := New(slow, newMockDialogueRunner(""), nil, nil)

	steps := []WorkflowStep{
		{
			Name:    "slow_step",
			Action:  ActionSingleExec,
			Config:  map[string]any{"cli": "codex"},
			Timeout: 1 * time.Millisecond, // very short timeout
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %q; want %q", result.Status, "failed")
	}
	if len(result.Steps) == 0 || result.Steps[0].Status != "failed" {
		t.Error("timed-out step should be marked failed")
	}
}

// slowSender blocks Send until context expires.
type slowSender struct{ delay time.Duration }

func (s *slowSender) Get(_ context.Context, name string) (ExecutorHandle, error) {
	return &mockHandle{name: name}, nil
}

func (s *slowSender) Send(ctx context.Context, _ ExecutorHandle, _ types.Message) (*types.Response, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(s.delay):
		return &types.Response{Content: "ok"}, nil
	}
}

// Verify that errors.Is works through the wrapping chain (basic error propagation check).
func TestEngine_ErrorWrapping(t *testing.T) {
	sentinel := errors.New("sentinel error")
	e := New(&errSender{err: sentinel}, newMockDialogueRunner(""), nil, nil)

	steps := []WorkflowStep{
		{
			Name:   "step",
			Action: ActionSingleExec,
			Config: map[string]any{"cli": "codex"},
		},
	}

	result, err := e.Execute(context.Background(), steps, WorkflowInput{})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status = %q; want %q", result.Status, "failed")
	}
	// Sentinel text must appear in the step content (wrapped error string).
	if !strings.Contains(result.Steps[0].Content, sentinel.Error()) {
		t.Errorf("step content %q does not contain sentinel error %q", result.Steps[0].Content, sentinel.Error())
	}
}

type errSender struct{ err error }

func (s *errSender) Get(_ context.Context, name string) (ExecutorHandle, error) {
	return &mockHandle{name: name}, nil
}
func (s *errSender) Send(_ context.Context, _ ExecutorHandle, _ types.Message) (*types.Response, error) {
	return nil, s.err
}
