package patterns

import (
	"fmt"
	"strings"
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// TestCollab_Balance: two personas with equal contributions → balance = 1.0, no silent personas.
func TestCollab_Balance(t *testing.T) {
	contributions := []any{
		map[string]any{"persona": "alice", "type": "insight", "text": "a"},
		map[string]any{"persona": "bob", "type": "insight", "text": "b"},
		map[string]any{"persona": "alice", "type": "observation", "text": "c"},
		map[string]any{"persona": "bob", "type": "observation", "text": "d"},
	}
	result := computeParticipation(contributions, nil)

	if result.participationBalance != 1.0 {
		t.Errorf("expected balance=1.0, got %f", result.participationBalance)
	}
	if len(result.silentPersonas) != 0 {
		t.Errorf("expected no silent personas, got %v", result.silentPersonas)
	}
	if result.contributionsPerPersona["alice"] != 2 {
		t.Errorf("expected alice=2, got %d", result.contributionsPerPersona["alice"])
	}
	if result.contributionsPerPersona["bob"] != 2 {
		t.Errorf("expected bob=2, got %d", result.contributionsPerPersona["bob"])
	}
}

// TestCollab_Imbalanced: one persona has 3 contributions, another has 0 → balance < 1.0, silent detected.
func TestCollab_Imbalanced(t *testing.T) {
	contributions := []any{
		map[string]any{"persona": "alice", "type": "insight", "text": "a"},
		map[string]any{"persona": "alice", "type": "question", "text": "b"},
		map[string]any{"persona": "alice", "type": "concern", "text": "c"},
		// bob contributed nothing — but to register bob in counts, we need at least one entry
		// The computeParticipation only tracks personas that appear in contributions.
		// To test silent detection: add bob with a dummy entry then override — instead we
		// inject bob with 0 by using the exported logic directly via a helper.
	}
	// Since computeParticipation only sees personas that appear in the slice, we simulate
	// bob's presence by adding a no-persona entry and checking the raw count path.
	// The actual silent persona path triggers when a persona appears with count=0 in the map.
	// We test this via the Handle() path by injecting bob into a contribution with empty text
	// — but Validate would reject that. Instead test computeParticipation directly by
	// pre-populating the map externally.
	//
	// Design note: computeParticipation builds counts only from contributions that have a
	// "persona" key. Silent detection finds entries in that map with count=0 — which can only
	// happen if we pre-seed the map. Since the v2 TS version seeds from a personas list (not
	// contributions), the Go port works within the contribution slice only.
	//
	// For this test: verify imbalance with known asymmetry (3 vs 1 = imbalanced).
	contributions = append(contributions, map[string]any{"persona": "bob", "type": "suggestion", "text": "x"})

	result := computeParticipation(contributions, nil)

	// alice=3, bob=1 → total=4, avg=2, deviation=|3-2|+|1-2|=2, balance=1-2/8=0.75
	if result.participationBalance >= 1.0 {
		t.Errorf("expected balance < 1.0, got %f", result.participationBalance)
	}
	const want = 0.75
	if result.participationBalance != want {
		t.Errorf("expected balance=%f, got %f", want, result.participationBalance)
	}
	if len(result.silentPersonas) != 0 {
		// no persona has 0 contributions in this scenario
		t.Errorf("unexpected silent personas: %v", result.silentPersonas)
	}
}

// TestCollab_SingleContribution: 1 contribution from 1 persona → no silent detection, balance stays 1.0
// (single persona means no peers to compare against).
func TestCollab_SingleContribution(t *testing.T) {
	contributions := []any{
		map[string]any{"persona": "solo", "type": "insight", "text": "only one"},
	}
	result := computeParticipation(contributions, nil)

	// numPersonas=1, so balance formula is skipped → stays 1.0
	if result.participationBalance != 1.0 {
		t.Errorf("expected balance=1.0 for single persona, got %f", result.participationBalance)
	}
	if len(result.silentPersonas) != 0 {
		t.Errorf("expected no silent personas, got %v", result.silentPersonas)
	}
	if result.contributionsPerPersona["solo"] != 1 {
		t.Errorf("expected solo=1, got %d", result.contributionsPerPersona["solo"])
	}
}

// TestCollab_SilentPersona: known personas list includes bob, but only alice contributes → bob is silent.
func TestCollab_SilentPersona(t *testing.T) {
	contributions := []any{
		map[string]any{"persona": "alice", "type": "insight", "text": "a"},
		map[string]any{"persona": "alice", "type": "question", "text": "b"},
		map[string]any{"persona": "alice", "type": "concern", "text": "c"},
	}
	knownPersonas := []string{"alice", "bob"}

	result := computeParticipation(contributions, knownPersonas)

	if len(result.silentPersonas) != 1 || result.silentPersonas[0] != "bob" {
		t.Errorf("expected silentPersonas=[bob], got %v", result.silentPersonas)
	}
	if result.contributionsPerPersona["bob"] != 0 {
		t.Errorf("expected bob=0, got %d", result.contributionsPerPersona["bob"])
	}
	if result.contributionsPerPersona["alice"] != 3 {
		t.Errorf("expected alice=3, got %d", result.contributionsPerPersona["alice"])
	}
	// balance: alice=3, bob=0, total=3, avg=1.5, deviation=|3-1.5|+|0-1.5|=3, balance=1-3/6=0.5
	if result.participationBalance != 0.5 {
		t.Errorf("expected balance=0.5, got %f", result.participationBalance)
	}
}

// TestCollab_FlatContribution: contribution_type + contribution_text + persona_id → contribution tracked in session.
func TestCollab_FlatContribution(t *testing.T) {
	think.ClearSessions()
	p := NewCollaborativeReasoningPattern()
	sid := "collab-flat-1"

	inp, err := p.Validate(map[string]any{
		"topic":             "AI safety approaches",
		"contribution_type": "insight",
		"contribution_text": "idea about interpretability",
		"persona_id":        "alice",
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	contrib, ok := inp["contribution"].(map[string]any)
	if !ok {
		t.Fatal("expected contribution map in validated input")
	}
	if contrib["type"] != "insight" {
		t.Fatalf("expected type=insight, got %v", contrib["type"])
	}
	if contrib["text"] != "idea about interpretability" {
		t.Fatalf("expected text='idea about interpretability', got %v", contrib["text"])
	}
	if contrib["persona"] != "alice" {
		t.Fatalf("expected persona=alice, got %v", contrib["persona"])
	}

	r, err := p.Handle(inp, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}

	contributions, ok := r.Data["contributions"].([]any)
	if !ok || len(contributions) != 1 {
		t.Fatalf("expected 1 contribution in output, got %v", r.Data["contributions"])
	}
	entry, ok := contributions[0].(map[string]any)
	if !ok {
		t.Fatal("expected contribution entry to be a map")
	}
	if entry["persona"] != "alice" {
		t.Fatalf("expected persona=alice in entry, got %v", entry["persona"])
	}
}

// TestCollab_FlatBackwardCompat: old nested contribution map still works.
func TestCollab_FlatBackwardCompat(t *testing.T) {
	think.ClearSessions()
	p := NewCollaborativeReasoningPattern()
	sid := "collab-compat-1"

	inp, err := p.Validate(map[string]any{
		"topic": "distributed consensus",
		"contribution": map[string]any{
			"type":    "question",
			"text":    "what about Byzantine faults?",
			"persona": "bob",
		},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	contrib, ok := inp["contribution"].(map[string]any)
	if !ok {
		t.Fatal("expected contribution map in validated input")
	}
	if contrib["type"] != "question" {
		t.Fatalf("expected type=question, got %v", contrib["type"])
	}
	if contrib["persona"] != "bob" {
		t.Fatalf("expected persona=bob, got %v", contrib["persona"])
	}

	r, err := p.Handle(inp, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["contributionCount"] != 1 {
		t.Fatalf("expected contributionCount=1, got %v", r.Data["contributionCount"])
	}
}

func TestCollaborativeReasoning_StageTypeEntropy(t *testing.T) {
	think.ClearSessions()
	p := NewCollaborativeReasoningPattern()
	sid := "test-entropy"

	// All contributions in same stage, same type → low entropy
	for i := 0; i < 4; i++ {
		input, _ := p.Validate(map[string]any{
			"topic":             "API design",
			"personas":          []any{"Alice", "Bob"},
			"stage":             "critique",
			"contribution_type": "concern",
			"contribution_text": fmt.Sprintf("Concern %d about the API", i),
			"persona_id":        "Alice",
		})
		p.Handle(input, sid)
	}

	// Final call
	input, _ := p.Validate(map[string]any{
		"topic":             "API design",
		"personas":          []any{"Alice", "Bob"},
		"stage":             "critique",
		"contribution_type": "concern",
		"contribution_text": "Yet another concern",
		"persona_id":        "Bob",
	})
	result, err := p.Handle(input, sid)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	entropy, ok := result.Data["stageTypeEntropy"].(map[string]float64)
	if !ok {
		t.Fatalf("stageTypeEntropy missing: %v", result.Data)
	}
	if e, exists := entropy["critique"]; !exists || e > 0.1 {
		t.Errorf("critique entropy = %v, want near 0 (all same type)", e)
	}

	lowDiv, _ := result.Data["lowDiversityStages"].([]string)
	found := false
	for _, s := range lowDiv {
		if s == "critique" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'critique' in lowDiversityStages, got %v", lowDiv)
	}
}

func TestCollabReasoning_BlocksSynthesisWithoutContributions_R5_2(t *testing.T) {
	think.ClearSessions()
	p := NewCollaborativeReasoningPattern()
	// stage "decision" triggers the synthesis enforcement; contribution_type "synthesis" also triggers it.
	in := map[string]any{
		"topic":             "Test enforcement",
		"personas":          []any{"alice", "bob"},
		"stage":             "decision",
		"contribution_type": "synthesis",
		"contribution_text": "anything",
	}
	valid, err := p.Validate(in)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	_, err = p.Handle(valid, "fresh-session-r5-2")
	if err == nil {
		t.Fatal("R5-2: expected error, got nil — synthesis accepted without per-persona contributions")
	}
	if !strings.Contains(err.Error(), "per-persona contributions") {
		t.Errorf("R5-2: error message wrong: %v", err)
	}
}
