package harness

import (
	"testing"
	"time"
)

func TestBalancedBudgetProfileHardCaps(t *testing.T) {
	profile := BalancedBudgetProfile()

	if profile.HardStepLimit != 24 {
		t.Fatalf("hard step limit = %d, want 24", profile.HardStepLimit)
	}
	if profile.HardDuration != 30*time.Minute {
		t.Fatalf("hard duration = %v, want 30m", profile.HardDuration)
	}
}

func TestBudgetSoftLimitExtendsOnlyOnMeaningfulDelta(t *testing.T) {
	now := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	profile := BalancedBudgetProfile()

	extended := ReviewBudget(profile, BudgetState{
		StartedAt:       now.Add(-11 * time.Minute),
		Now:             now,
		StepCount:       9,
		MeaningfulDelta: true,
	})
	if extended.Action != StopContinue || !extended.CanExtend {
		t.Fatalf("meaningful delta should extend soft budget: %+v", extended)
	}

	blocked := ReviewBudget(profile, BudgetState{
		StartedAt:       now.Add(-11 * time.Minute),
		Now:             now,
		StepCount:       9,
		MeaningfulDelta: false,
	})
	if blocked.Action == StopContinue || blocked.CanExtend {
		t.Fatalf("no-delta soft budget should not continue unchanged: %+v", blocked)
	}
}

func TestBudgetNoDeltaRedirectsAfterTwoSteps(t *testing.T) {
	decision := ReviewBudget(BalancedBudgetProfile(), BudgetState{
		ConsecutiveNoDelta: 2,
	})

	if decision.Action != StopRedirect {
		t.Fatalf("two no-delta steps action = %q, want redirect", decision.Action)
	}
}

func TestBudgetHardCapStopsWithResumableSummary(t *testing.T) {
	now := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	decision := ReviewBudget(BalancedBudgetProfile(), BudgetState{
		StartedAt: now.Add(-31 * time.Minute),
		Now:       now,
		StepCount: 24,
	})

	if decision.Action != StopHalt {
		t.Fatalf("hard cap action = %q, want stop", decision.Action)
	}
	if decision.StopReason != "budget_exhausted" {
		t.Fatalf("stop reason = %q, want budget_exhausted", decision.StopReason)
	}
	if decision.CanExtend {
		t.Fatal("hard cap must not be extendable")
	}
	if decision.ResumableSummary == "" {
		t.Fatal("hard cap must include resumable summary")
	}
}
