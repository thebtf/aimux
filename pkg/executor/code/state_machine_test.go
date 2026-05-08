package code

import (
	"testing"

	"github.com/thebtf/aimux/pkg/executor/types"
)

func TestMachineApplyGateDonePath(t *testing.T) {
	m := newTestMachine(t, Config{})

	advanceAll(t, m,
		step{to: StateDriver, reason: "pair selected"},
		step{to: StateNavigator, reason: "driver diff ready"},
		step{to: StateApply, reason: "navigator approved driver diff"},
		step{to: StateGate, reason: "diff applied"},
		step{to: StateDone, reason: "gate passed"},
	)

	if got := m.State(); got != StateDone {
		t.Fatalf("state = %s, want %s", got, StateDone)
	}
	assertTransitionLog(t, m.Metadata(), []Transition{
		{From: StatePrep, To: StateDriver, Reason: "pair selected", Round: 0},
		{From: StateDriver, To: StateNavigator, Reason: "driver diff ready", Round: 0},
		{From: StateNavigator, To: StateApply, Reason: "navigator approved driver diff", Round: 0},
		{From: StateApply, To: StateGate, Reason: "diff applied", Round: 0},
		{From: StateGate, To: StateDone, Reason: "gate passed", Round: 0},
	})
}

func TestMachineReviseGateDonePath(t *testing.T) {
	m := newTestMachine(t, Config{})

	advanceAll(t, m,
		step{to: StateDriver},
		step{to: StateNavigator},
		step{to: StateRevise, reason: "navigator edited diff"},
		step{to: StateGate, reason: "revised diff applied"},
		step{to: StateDone, reason: "gate passed"},
	)

	if got := m.State(); got != StateDone {
		t.Fatalf("state = %s, want %s", got, StateDone)
	}
	assertLastTransition(t, m.Metadata(), Transition{
		From:   StateGate,
		To:     StateDone,
		Reason: "gate passed",
		Round:  0,
	})
}

func TestMachineRetryDriverLoopIncrementsRounds(t *testing.T) {
	m := newTestMachine(t, Config{MaxRounds: 3})

	advanceAll(t, m,
		step{to: StateDriver},
		step{to: StateNavigator},
		step{to: StateRetry, reason: "needs narrower diff"},
	)
	if got := m.Rounds(); got != 0 {
		t.Fatalf("rounds before RETRY -> DRIVER = %d, want 0", got)
	}

	advanceAll(t, m, step{to: StateDriver, reason: "retrying with navigator feedback"})

	if got := m.Rounds(); got != 1 {
		t.Fatalf("rounds after RETRY -> DRIVER = %d, want 1", got)
	}
	if got := m.State(); got != StateDriver {
		t.Fatalf("state = %s, want %s", got, StateDriver)
	}
	assertLastTransition(t, m.Metadata(), Transition{
		From:   StateRetry,
		To:     StateDriver,
		Reason: "retrying with navigator feedback",
		Round:  1,
	})
}

func TestMachineMaxRoundsReachedEscalates(t *testing.T) {
	m := newTestMachine(t, Config{MaxRounds: 1})
	advanceAll(t, m,
		step{to: StateDriver},
		step{to: StateNavigator},
		step{to: StateRetry, reason: "first retry"},
		step{to: StateDriver, reason: "retry loop"},
		step{to: StateNavigator, reason: "retry diff ready"},
	)

	err := m.Advance(StateRetry, "")
	if err == nil {
		t.Fatal("Advance(RETRY) after max rounds returned nil, want CapabilityMismatch")
	}
	if err.Code != types.CLIErrorCodeCapabilityMismatch {
		t.Fatalf("error code = %s, want %s", err.Code, types.CLIErrorCodeCapabilityMismatch)
	}
	if got := m.State(); got != StateEscalate {
		t.Fatalf("state = %s, want %s", got, StateEscalate)
	}
	assertLastTransition(t, m.Metadata(), Transition{
		From:   StateNavigator,
		To:     StateEscalate,
		Reason: "max rounds reached",
		Round:  1,
	})
}

func TestValidateTransitionRejectsIllegalMoves(t *testing.T) {
	err := ValidateTransition(StatePrep, StateGate, 0, DefaultMaxRounds)
	if err == nil {
		t.Fatal("ValidateTransition(PREP, GATE) returned nil, want error")
	}
	if err.Code != types.CLIErrorCodeUserInputError {
		t.Fatalf("error code = %s, want %s", err.Code, types.CLIErrorCodeUserInputError)
	}

	m := newTestMachine(t, Config{})
	err = m.Advance(StateGate, "stub validator would accept this")
	if err == nil {
		t.Fatal("Advance(PREP -> GATE) returned nil, want illegal-move error")
	}
	if got := m.State(); got != StatePrep {
		t.Fatalf("state after illegal transition = %s, want %s", got, StatePrep)
	}
	if _, ok := m.Metadata()[MetadataTransitionsKey]; ok {
		t.Fatal("illegal transition was recorded in metadata")
	}
}

func TestRecordTransitionDoesNotMutateCallerMetadata(t *testing.T) {
	original := map[string]any{
		"existing": MetadataTransitionsKey,
		MetadataTransitionsKey: []any{
			map[string]any{"from": string(StatePrep), "to": string(StateDriver), "round": 0},
		},
	}

	next := RecordTransition(original, Transition{
		From:   StateDriver,
		To:     StateNavigator,
		Reason: "driver done",
		Round:  0,
	})

	if len(original[MetadataTransitionsKey].([]any)) != 1 {
		t.Fatalf("original transition log mutated: %#v", original[MetadataTransitionsKey])
	}
	assertTransitionLog(t, next, []Transition{
		{From: StatePrep, To: StateDriver, Round: 0},
		{From: StateDriver, To: StateNavigator, Reason: "driver done", Round: 0},
	})
}

func TestMachineMetadataReturnsDefensiveTransitionLog(t *testing.T) {
	m := newTestMachine(t, Config{})
	advanceAll(t, m, step{to: StateDriver, reason: "pair selected"})

	metadata := m.Metadata()
	log := metadata[MetadataTransitionsKey].([]any)
	entry := log[0].(map[string]any)
	entry["to"] = string(StateError)

	assertLastTransition(t, m.Metadata(), Transition{
		From:   StatePrep,
		To:     StateDriver,
		Reason: "pair selected",
		Round:  0,
	})
}

type step struct {
	to     State
	reason string
}

func newTestMachine(t *testing.T, cfg Config) *Machine {
	t.Helper()
	m, err := NewMachine(cfg)
	if err != nil {
		t.Fatalf("NewMachine returned error: %v", err)
	}
	return m
}

func advanceAll(t *testing.T, m *Machine, steps ...step) {
	t.Helper()
	for _, s := range steps {
		if err := m.Advance(s.to, s.reason); err != nil {
			t.Fatalf("Advance(%s) returned error: %v", s.to, err)
		}
	}
}

func assertLastTransition(t *testing.T, metadata map[string]any, want Transition) {
	t.Helper()
	log := transitionLogFromMetadata(t, metadata)
	if len(log) == 0 {
		t.Fatal("transition log is empty")
	}
	assertTransition(t, log[len(log)-1], want)
}

func assertTransitionLog(t *testing.T, metadata map[string]any, want []Transition) {
	t.Helper()
	got := transitionLogFromMetadata(t, metadata)
	if len(got) != len(want) {
		t.Fatalf("transition log length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		assertTransition(t, got[i], want[i])
	}
}

func transitionLogFromMetadata(t *testing.T, metadata map[string]any) []map[string]any {
	t.Helper()
	raw, ok := metadata[MetadataTransitionsKey]
	if !ok {
		t.Fatal("metadata missing transition log")
	}
	log, ok := raw.([]any)
	if !ok {
		t.Fatalf("transition log type = %T, want []any", raw)
	}
	entries := make([]map[string]any, 0, len(log))
	for i, rawEntry := range log {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			t.Fatalf("transition log entry %d type = %T, want map[string]any", i, rawEntry)
		}
		entries = append(entries, entry)
	}
	return entries
}

func assertTransition(t *testing.T, got map[string]any, want Transition) {
	t.Helper()
	if got["from"] != string(want.From) {
		t.Fatalf("transition from = %#v, want %s", got["from"], want.From)
	}
	if got["to"] != string(want.To) {
		t.Fatalf("transition to = %#v, want %s", got["to"], want.To)
	}
	gotReason, _ := got["reason"].(string)
	if gotReason != want.Reason {
		t.Fatalf("transition reason = %#v, want %q", got["reason"], want.Reason)
	}
	if got["round"] != want.Round {
		t.Fatalf("transition round = %#v, want %d", got["round"], want.Round)
	}
}
