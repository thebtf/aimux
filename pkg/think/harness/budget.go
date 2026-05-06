package harness

import "time"

type BudgetProfile struct {
	SoftStepLimit          int           `json:"soft_step_limit"`
	SoftDuration           time.Duration `json:"soft_duration"`
	HardStepLimit          int           `json:"hard_step_limit"`
	HardDuration           time.Duration `json:"hard_duration"`
	MaxConsecutiveNoDelta  int           `json:"max_consecutive_no_delta"`
	SynchronousHardCapMode bool          `json:"synchronous_hard_cap_mode"`
}

type BudgetState struct {
	StartedAt          time.Time `json:"started_at,omitempty"`
	Now                time.Time `json:"now,omitempty"`
	StepCount          int       `json:"step_count"`
	ConsecutiveNoDelta int       `json:"consecutive_no_delta,omitempty"`
	MeaningfulDelta    bool      `json:"meaningful_delta,omitempty"`
	LastDelta          string    `json:"last_delta,omitempty"`
	ExpectedNextDelta  string    `json:"expected_next_delta,omitempty"`
}

type BudgetReview struct {
	Action           StopAction `json:"action"`
	CanExtend        bool       `json:"can_extend"`
	StopReason       string     `json:"stop_reason,omitempty"`
	BudgetState      string     `json:"budget_state"`
	ResumableSummary string     `json:"resumable_summary,omitempty"`
	NextPrompt       string     `json:"next_prompt,omitempty"`
}

func BalancedBudgetProfile() BudgetProfile {
	return BudgetProfile{
		SoftStepLimit:          8,
		SoftDuration:           10 * time.Minute,
		HardStepLimit:          24,
		HardDuration:           30 * time.Minute,
		MaxConsecutiveNoDelta:  2,
		SynchronousHardCapMode: true,
	}
}

func ReviewBudget(profile BudgetProfile, state BudgetState) BudgetReview {
	if profile.SoftStepLimit <= 0 {
		profile.SoftStepLimit = 8
	}
	if profile.SoftDuration <= 0 {
		profile.SoftDuration = 10 * time.Minute
	}
	if profile.HardStepLimit <= 0 {
		profile.HardStepLimit = 24
	}
	if profile.HardDuration <= 0 {
		profile.HardDuration = 30 * time.Minute
	}
	if profile.MaxConsecutiveNoDelta <= 0 {
		profile.MaxConsecutiveNoDelta = 2
	}

	elapsed := elapsedBudgetTime(state)
	if state.StepCount >= profile.HardStepLimit || elapsed >= profile.HardDuration {
		return BudgetReview{
			Action:           StopHalt,
			CanExtend:        false,
			StopReason:       "budget_exhausted",
			BudgetState:      "hard_cap_exhausted",
			ResumableSummary: budgetSummary(state, elapsed),
			NextPrompt:       "Stop this synchronous thinking run and resume from the summary in a new run if more work is needed.",
		}
	}

	if state.ConsecutiveNoDelta >= profile.MaxConsecutiveNoDelta {
		return BudgetReview{
			Action:      StopRedirect,
			CanExtend:   false,
			BudgetState: "no_delta_redirect",
			NextPrompt:  "Change cognitive move, compress the current state, or request finalization instead of repeating the same step.",
		}
	}

	softExceeded := state.StepCount > profile.SoftStepLimit || elapsed > profile.SoftDuration
	if softExceeded {
		if state.MeaningfulDelta {
			return BudgetReview{
				Action:      StopContinue,
				CanExtend:   true,
				BudgetState: "soft_extended",
				NextPrompt:  "Continue only if the next step names the expected new ledger, gate, objection, or move delta.",
			}
		}
		return BudgetReview{
			Action:      StopRedirect,
			CanExtend:   false,
			BudgetState: "soft_exhausted_without_delta",
			NextPrompt:  "Do not continue unchanged after soft budget exhaustion; redirect, compress, or finalize.",
		}
	}

	return BudgetReview{
		Action:      StopContinue,
		CanExtend:   false,
		BudgetState: "within_budget",
		NextPrompt:  "Continue while each step produces a visible artifact delta.",
	}
}

func elapsedBudgetTime(state BudgetState) time.Duration {
	if state.StartedAt.IsZero() {
		return 0
	}
	now := state.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if now.Before(state.StartedAt) {
		return 0
	}
	return now.Sub(state.StartedAt)
}

func budgetSummary(state BudgetState, elapsed time.Duration) string {
	if state.LastDelta != "" {
		return "Hard budget exhausted after visible progress: " + state.LastDelta
	}
	if state.ExpectedNextDelta != "" {
		return "Hard budget exhausted before expected delta: " + state.ExpectedNextDelta
	}
	return "Hard budget exhausted after the recorded synchronous thinking run; resume with the current trace and ledger."
}
