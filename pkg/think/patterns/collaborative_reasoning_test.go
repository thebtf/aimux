package patterns

import (
	"testing"
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
