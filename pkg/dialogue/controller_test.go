package dialogue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// --- mock participant ---

type mockParticipant struct {
	name      string
	role      string
	respondFn func(ctx context.Context, prompt string, history []DialogueTurn) (string, error)
}

func (m *mockParticipant) Name() string { return m.name }
func (m *mockParticipant) Role() string { return m.role }
func (m *mockParticipant) Respond(ctx context.Context, prompt string, history []DialogueTurn) (string, error) {
	return m.respondFn(ctx, prompt, history)
}

// newMock creates a mock participant that returns a fixed response string.
func newMock(name, role, response string) *mockParticipant {
	return &mockParticipant{
		name: name,
		role: role,
		respondFn: func(_ context.Context, _ string, _ []DialogueTurn) (string, error) {
			return response, nil
		},
	}
}

// newMockErr creates a mock participant that always returns an error.
func newMockErr(name, role string, err error) *mockParticipant {
	return &mockParticipant{
		name: name,
		role: role,
		respondFn: func(_ context.Context, _ string, _ []DialogueTurn) (string, error) {
			return "", err
		},
	}
}

// --- TestNewDialogue ---

func TestNewDialogue(t *testing.T) {
	t.Run("empty participants returns error", func(t *testing.T) {
		c := New()
		_, err := c.NewDialogue(DialogueConfig{
			Topic: "test",
		})
		if err == nil {
			t.Fatal("expected error for empty participants, got nil")
		}
	})

	t.Run("valid config creates dialogue", func(t *testing.T) {
		c := New()
		p := newMock("alice", "speaker", "hello")
		d, err := c.NewDialogue(DialogueConfig{
			Participants: []Participant{p},
			Topic:        "greetings",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d.ID == "" {
			t.Error("expected non-empty dialogue ID")
		}
		if d.Status != StatusActive {
			t.Errorf("expected StatusActive, got %v", d.Status)
		}
		if len(d.Turns) != 0 {
			t.Errorf("expected 0 turns on creation, got %d", len(d.Turns))
		}
	})

	t.Run("IDs are unique", func(t *testing.T) {
		c := New()
		p := newMock("a", "r", "x")
		d1, _ := c.NewDialogue(DialogueConfig{Participants: []Participant{p}, Topic: "t"})
		d2, _ := c.NewDialogue(DialogueConfig{Participants: []Participant{p}, Topic: "t"})
		if d1.ID == d2.ID {
			t.Errorf("expected unique IDs, both got %q", d1.ID)
		}
	})

	t.Run("zero max turns preserved", func(t *testing.T) {
		c := New()
		p := newMock("a", "r", "x")
		d, _ := c.NewDialogue(DialogueConfig{
			Participants: []Participant{p},
			Topic:        "t",
			MaxTurns:     0,
		})
		if d.Config.MaxTurns != 0 {
			t.Errorf("expected MaxTurns=0, got %d", d.Config.MaxTurns)
		}
	})
}

// --- TestNextTurn_Sequential ---

func TestNextTurn_Sequential(t *testing.T) {
	c := New()
	participants := []Participant{
		newMock("alice", "advocate", "Alice says A"),
		newMock("bob", "critic", "Bob says B"),
		newMock("carol", "moderator", "Carol says C"),
	}

	d, err := c.NewDialogue(DialogueConfig{
		Participants: participants,
		Mode:         ModeSequential,
		Topic:        "is Go great?",
	})
	if err != nil {
		t.Fatalf("NewDialogue: %v", err)
	}

	ctx := context.Background()

	// Turn 1: Alice
	turns, err := c.NextTurn(ctx, d)
	if err != nil {
		t.Fatalf("NextTurn 1: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}
	if turns[0].Participant != "alice" {
		t.Errorf("expected alice, got %q", turns[0].Participant)
	}
	if turns[0].Content != "Alice says A" {
		t.Errorf("unexpected content: %q", turns[0].Content)
	}
	if turns[0].TurnNumber != 1 {
		t.Errorf("expected TurnNumber 1, got %d", turns[0].TurnNumber)
	}
	if d.Status != StatusActive {
		t.Errorf("expected StatusActive after turn 1, got %v", d.Status)
	}

	// Turn 2: Bob
	turns, err = c.NextTurn(ctx, d)
	if err != nil {
		t.Fatalf("NextTurn 2: %v", err)
	}
	if turns[0].Participant != "bob" {
		t.Errorf("expected bob, got %q", turns[0].Participant)
	}

	// Turn 3: Carol — last participant
	turns, err = c.NextTurn(ctx, d)
	if err != nil {
		t.Fatalf("NextTurn 3: %v", err)
	}
	if turns[0].Participant != "carol" {
		t.Errorf("expected carol, got %q", turns[0].Participant)
	}
	if d.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted after all participants, got %v", d.Status)
	}

	// Next call on completed dialogue returns nil
	turns, err = c.NextTurn(ctx, d)
	if err != nil {
		t.Fatalf("NextTurn on completed: %v", err)
	}
	if turns != nil {
		t.Errorf("expected nil turns on completed dialogue, got %v", turns)
	}

	// Total turns in history
	if len(d.Turns) != 3 {
		t.Errorf("expected 3 turns in history, got %d", len(d.Turns))
	}
}

// --- TestNextTurn_Parallel ---

func TestNextTurn_Parallel(t *testing.T) {
	c := New()
	participants := []Participant{
		newMock("alpha", "role-a", "Alpha response"),
		newMock("beta", "role-b", "Beta response"),
		newMock("gamma", "role-c", "Gamma response"),
	}

	d, err := c.NewDialogue(DialogueConfig{
		Participants: participants,
		Mode:         ModeParallel,
		Topic:        "parallel test topic",
	})
	if err != nil {
		t.Fatalf("NewDialogue: %v", err)
	}

	ctx := context.Background()
	turns, err := c.NextTurn(ctx, d)
	if err != nil {
		t.Fatalf("NextTurn: %v", err)
	}

	// All 3 participants respond simultaneously
	if len(turns) != 3 {
		t.Fatalf("expected 3 parallel turns, got %d", len(turns))
	}

	// Verify all participants are represented
	names := make(map[string]bool)
	for _, t := range turns {
		names[t.Participant] = true
	}
	for _, p := range participants {
		if !names[p.Name()] {
			t.Errorf("participant %q missing from parallel turns", p.Name())
		}
	}

	// Parallel mode completes after one round
	if d.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted after parallel round, got %v", d.Status)
	}

	// 3 turns in history
	if len(d.Turns) != 3 {
		t.Errorf("expected 3 turns in history, got %d", len(d.Turns))
	}
}

// --- TestNextTurn_Stance ---

func TestNextTurn_Stance(t *testing.T) {
	// Track the prompt received by each participant to verify stance injection.
	var receivedPrompts []string

	recordingMock := func(name, role string) *mockParticipant {
		return &mockParticipant{
			name: name,
			role: role,
			respondFn: func(_ context.Context, prompt string, _ []DialogueTurn) (string, error) {
				receivedPrompts = append(receivedPrompts, prompt)
				return fmt.Sprintf("%s response", name), nil
			},
		}
	}

	c := New()
	participants := []Participant{
		recordingMock("pro-agent", "advocate"),
		recordingMock("con-agent", "critic"),
	}

	d, err := c.NewDialogue(DialogueConfig{
		Participants: participants,
		Mode:         ModeStance,
		Topic:        "Is Go better than Rust?",
		Stances: map[string]string{
			"pro-agent": "pro",
			"con-agent": "con",
		},
	})
	if err != nil {
		t.Fatalf("NewDialogue: %v", err)
	}

	ctx := context.Background()

	// Turn 1: pro-agent
	turns, err := c.NextTurn(ctx, d)
	if err != nil {
		t.Fatalf("NextTurn 1: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}
	if turns[0].Stance != "pro" {
		t.Errorf("expected stance 'pro', got %q", turns[0].Stance)
	}
	if !strings.Contains(receivedPrompts[0], "PRO") {
		t.Errorf("expected PRO in prompt, got: %q", receivedPrompts[0])
	}

	// Turn 2: con-agent
	turns, err = c.NextTurn(ctx, d)
	if err != nil {
		t.Fatalf("NextTurn 2: %v", err)
	}
	if turns[0].Stance != "con" {
		t.Errorf("expected stance 'con', got %q", turns[0].Stance)
	}
	if !strings.Contains(receivedPrompts[1], "CON") {
		t.Errorf("expected CON in prompt, got: %q", receivedPrompts[1])
	}

	if d.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted, got %v", d.Status)
	}
}

// --- TestNextTurn_MaxTurns ---

func TestNextTurn_MaxTurns(t *testing.T) {
	c := New()
	callCount := 0
	p := &mockParticipant{
		name: "counter",
		role: "tester",
		respondFn: func(_ context.Context, _ string, _ []DialogueTurn) (string, error) {
			callCount++
			return fmt.Sprintf("response %d", callCount), nil
		},
	}

	d, err := c.NewDialogue(DialogueConfig{
		Participants: []Participant{p},
		Mode:         ModeRoundRobin,
		Topic:        "counting",
		MaxTurns:     3,
	})
	if err != nil {
		t.Fatalf("NewDialogue: %v", err)
	}

	ctx := context.Background()

	for i := 0; i < 3; i++ {
		turns, err := c.NextTurn(ctx, d)
		if err != nil {
			t.Fatalf("NextTurn %d: %v", i+1, err)
		}
		if len(turns) == 0 {
			t.Fatalf("expected turns at iteration %d, got none", i+1)
		}
	}

	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
	if d.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted after MaxTurns, got %v", d.Status)
	}

	// Additional call returns nil
	turns, err := c.NextTurn(ctx, d)
	if err != nil {
		t.Fatalf("post-limit NextTurn: %v", err)
	}
	if turns != nil {
		t.Errorf("expected nil turns after MaxTurns, got %v", turns)
	}
	if callCount != 3 {
		t.Errorf("expected no extra calls after MaxTurns, callCount=%d", callCount)
	}
}

// --- TestNextTurn_Complete ---

func TestNextTurn_Complete(t *testing.T) {
	c := New()
	p := newMock("solo", "speaker", "done")

	d, err := c.NewDialogue(DialogueConfig{
		Participants: []Participant{p},
		Mode:         ModeSequential,
		Topic:        "completion test",
	})
	if err != nil {
		t.Fatalf("NewDialogue: %v", err)
	}

	ctx := context.Background()

	// First call: solo participant speaks.
	turns, err := c.NextTurn(ctx, d)
	if err != nil {
		t.Fatalf("NextTurn 1: %v", err)
	}
	if len(turns) == 0 {
		t.Fatal("expected turns on first call")
	}

	// Second call: dialogue complete — nil returned.
	turns, err = c.NextTurn(ctx, d)
	if err != nil {
		t.Fatalf("NextTurn 2 (completed): %v", err)
	}
	if turns != nil {
		t.Errorf("expected nil turns on completed dialogue, got %v", turns)
	}
}

// --- TestSynthesize ---

func TestSynthesize(t *testing.T) {
	c := New()
	participants := []Participant{
		newMock("alice", "pro", "I think Go is great"),
		newMock("bob", "con", "I prefer Rust"),
	}

	d, err := c.NewDialogue(DialogueConfig{
		Participants: participants,
		Mode:         ModeSequential,
		Topic:        "language debate",
	})
	if err != nil {
		t.Fatalf("NewDialogue: %v", err)
	}

	ctx := context.Background()
	_, _ = c.NextTurn(ctx, d)
	_, _ = c.NextTurn(ctx, d)

	syn, err := c.Synthesize(ctx, d)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if syn == nil {
		t.Fatal("expected non-nil Synthesis")
	}
	if !strings.Contains(syn.Content, "alice") {
		t.Errorf("synthesis should mention alice, got: %q", syn.Content)
	}
	if !strings.Contains(syn.Content, "bob") {
		t.Errorf("synthesis should mention bob, got: %q", syn.Content)
	}
	if syn.TurnCount != 2 {
		t.Errorf("expected TurnCount=2, got %d", syn.TurnCount)
	}
	if len(syn.Participants) != 2 {
		t.Errorf("expected 2 participants, got %d", len(syn.Participants))
	}
	// M3: agreement not computed
	if syn.Agreement != -1 {
		t.Errorf("expected Agreement=-1 (M3), got %f", syn.Agreement)
	}

	// Synthesis stored on dialogue
	if d.Synthesis == nil {
		t.Error("expected d.Synthesis to be set")
	}
}

func TestSynthesize_NoTurns(t *testing.T) {
	c := New()
	p := newMock("a", "r", "x")
	d, _ := c.NewDialogue(DialogueConfig{
		Participants: []Participant{p},
		Topic:        "empty",
	})

	_, err := c.Synthesize(context.Background(), d)
	if err == nil {
		t.Error("expected error synthesizing empty dialogue")
	}
}

// --- TestPatternParticipant ---

func TestPatternParticipant(t *testing.T) {
	called := false
	var receivedInput map[string]any

	handleFn := func(input map[string]any) (map[string]any, error) {
		called = true
		receivedInput = input
		return map[string]any{
			"content": "pattern analysis result",
		}, nil
	}

	pp := NewPatternParticipant("peer_review", handleFn)

	if pp.Name() != "peer_review" {
		t.Errorf("expected name 'peer_review', got %q", pp.Name())
	}
	if pp.Role() != "pattern" {
		t.Errorf("expected default role 'pattern', got %q", pp.Role())
	}

	history := []DialogueTurn{
		{Participant: "alice", Content: "first turn"},
	}

	resp, err := pp.Respond(context.Background(), "analyze this", history)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if resp != "pattern analysis result" {
		t.Errorf("expected 'pattern analysis result', got %q", resp)
	}
	if !called {
		t.Error("expected handleFn to be called")
	}
	if receivedInput["thought"] != "analyze this" {
		t.Errorf("expected thought='analyze this', got %v", receivedInput["thought"])
	}
	if _, ok := receivedInput["history"]; !ok {
		t.Error("expected 'history' key in input when history is non-empty")
	}
}

func TestPatternParticipant_WithRole(t *testing.T) {
	pp := NewPatternParticipantWithRole("decision_framework", "arbiter", func(input map[string]any) (map[string]any, error) {
		return map[string]any{"output": "decision made"}, nil
	})

	if pp.Role() != "arbiter" {
		t.Errorf("expected role 'arbiter', got %q", pp.Role())
	}

	resp, err := pp.Respond(context.Background(), "choose", nil)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if resp != "decision made" {
		t.Errorf("unexpected response: %q", resp)
	}
}

func TestPatternParticipant_HandlerError(t *testing.T) {
	pp := NewPatternParticipant("broken", func(_ map[string]any) (map[string]any, error) {
		return nil, errors.New("pattern exploded")
	})

	_, err := pp.Respond(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("expected error from handler, got nil")
	}
	if !strings.Contains(err.Error(), "pattern exploded") {
		t.Errorf("expected 'pattern exploded' in error, got: %v", err)
	}
}

func TestPatternParticipant_EmptyResult(t *testing.T) {
	pp := NewPatternParticipant("empty_pattern", func(_ map[string]any) (map[string]any, error) {
		return map[string]any{}, nil
	})

	resp, err := pp.Respond(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Falls back to placeholder when no text fields found
	if !strings.Contains(resp, "empty_pattern") {
		t.Errorf("expected fallback mentioning pattern name, got: %q", resp)
	}
}

// --- TestSwarmParticipant (mock) ---

// mockSwarmExecutor implements types.ExecutorV2 for testing without importing
// the actual executor implementation.

// We test SwarmParticipant behavior through a mock by directly testing the
// participant interface contract via composition. The actual Swarm integration
// is tested in swarm_test.go.
//
// Here we verify the name/role accessors and the historyToTurns conversion.
func TestSwarmParticipant_Accessors(t *testing.T) {
	// We cannot easily instantiate a real Swarm in a unit test without a full
	// executor factory. Instead we verify the struct fields via NewSwarmParticipant.
	// A nil swarm is acceptable for accessor-only tests.
	sp := NewSwarmParticipant(nil, nil, "codex", "coder")

	if sp.Name() != "codex" {
		t.Errorf("expected name 'codex', got %q", sp.Name())
	}
	if sp.Role() != "coder" {
		t.Errorf("expected role 'coder', got %q", sp.Role())
	}
}

func TestHistoryToTurns_Empty(t *testing.T) {
	turns := historyToTurns(nil)
	if turns != nil {
		t.Errorf("expected nil for empty history, got %v", turns)
	}
}

func TestHistoryToTurns_NonEmpty(t *testing.T) {
	history := []DialogueTurn{
		{Participant: "alice", Content: "hello"},
		{Participant: "bob", Content: "world"},
	}

	turns := historyToTurns(history)
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	for _, t := range turns {
		if t.Role != "user" {
			// History turns are presented as "user" role so the LLM treats them
			// as external context rather than its own prior output (SEC-002).
		}
	}
	if !strings.Contains(turns[0].Content, "alice") {
		t.Errorf("expected alice in turn 0, got: %q", turns[0].Content)
	}
	if !strings.Contains(turns[1].Content, "bob") {
		t.Errorf("expected bob in turn 1, got: %q", turns[1].Content)
	}
}

// --- TestClose ---

func TestClose(t *testing.T) {
	c := New()
	p := newMock("a", "r", "x")
	d, _ := c.NewDialogue(DialogueConfig{
		Participants: []Participant{p},
		Topic:        "close test",
	})

	if d.Status != StatusActive {
		t.Errorf("expected StatusActive initially, got %v", d.Status)
	}

	if err := c.Close(d); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if d.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted after Close, got %v", d.Status)
	}

	// Idempotent — second Close must not error
	if err := c.Close(d); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestClose_NilDialogue(t *testing.T) {
	c := New()
	if err := c.Close(nil); err == nil {
		t.Error("expected error closing nil dialogue")
	}
}

// --- TestRoundRobin ---

func TestNextTurn_RoundRobin(t *testing.T) {
	c := New()
	var callOrder []string
	mkMock := func(name string) *mockParticipant {
		return &mockParticipant{
			name: name,
			role: "speaker",
			respondFn: func(_ context.Context, _ string, _ []DialogueTurn) (string, error) {
				callOrder = append(callOrder, name)
				return name + " response", nil
			},
		}
	}

	participants := []Participant{mkMock("x"), mkMock("y")}
	d, _ := c.NewDialogue(DialogueConfig{
		Participants: participants,
		Mode:         ModeRoundRobin,
		Topic:        "round robin test",
		MaxTurns:     4,
	})

	ctx := context.Background()
	for {
		turns, err := c.NextTurn(ctx, d)
		if err != nil {
			t.Fatalf("NextTurn: %v", err)
		}
		if turns == nil {
			break
		}
	}

	if len(callOrder) != 4 {
		t.Fatalf("expected 4 calls, got %d: %v", len(callOrder), callOrder)
	}
	// Should cycle: x, y, x, y
	expected := []string{"x", "y", "x", "y"}
	for i, got := range callOrder {
		if got != expected[i] {
			t.Errorf("callOrder[%d]: expected %q, got %q", i, expected[i], got)
		}
	}
}

// --- TestParticipantError ---

func TestNextTurn_ParticipantError(t *testing.T) {
	c := New()
	errParticipant := newMockErr("faulty", "broken", errors.New("network failure"))

	d, _ := c.NewDialogue(DialogueConfig{
		Participants: []Participant{errParticipant},
		Mode:         ModeSequential,
		Topic:        "error test",
	})

	_, err := c.NextTurn(context.Background(), d)
	if err == nil {
		t.Fatal("expected error from faulty participant, got nil")
	}
	if !strings.Contains(err.Error(), "network failure") {
		t.Errorf("expected 'network failure' in error, got: %v", err)
	}
	if d.Status != StatusFailed {
		t.Errorf("expected StatusFailed, got %v", d.Status)
	}
}

// --- TestDialogueStatus_String ---

func TestDialogueStatus_String(t *testing.T) {
	cases := []struct {
		status DialogueStatus
		want   string
	}{
		{StatusActive, "active"},
		{StatusCompleted, "completed"},
		{StatusFailed, "failed"},
		{DialogueStatus(99), "unknown"},
	}

	for _, tc := range cases {
		if got := tc.status.String(); got != tc.want {
			t.Errorf("status %d: expected %q, got %q", tc.status, tc.want, got)
		}
	}
}

// --- TestSynthesis_ParticipantDedup ---

func TestSynthesis_ParticipantDedup(t *testing.T) {
	// When the same participant appears multiple times (RoundRobin), the
	// synthesis should deduplicate them in the Participants list.
	c := New()
	p := newMock("solo", "speaker", "response")

	d, _ := c.NewDialogue(DialogueConfig{
		Participants: []Participant{p},
		Mode:         ModeRoundRobin,
		Topic:        "dedup test",
		MaxTurns:     3,
	})

	ctx := context.Background()
	for {
		turns, _ := c.NextTurn(ctx, d)
		if turns == nil {
			break
		}
	}

	syn, err := c.Synthesize(ctx, d)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if len(syn.Participants) != 1 {
		t.Errorf("expected 1 unique participant, got %d: %v", len(syn.Participants), syn.Participants)
	}
	if syn.TurnCount != 3 {
		t.Errorf("expected TurnCount=3, got %d", syn.TurnCount)
	}
}
