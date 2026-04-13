package executor_test

import (
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
)

func TestCooldownTracker_MarkedModelUnavailableBeforeExpiry(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 10*time.Second)

	if tracker.IsAvailable("codex", "gpt-5.3-codex-spark") {
		t.Error("model should be unavailable while cooldown is active")
	}
}

func TestCooldownTracker_ModelAvailableAfterExpiry(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 50*time.Millisecond)

	time.Sleep(100 * time.Millisecond)

	if !tracker.IsAvailable("codex", "gpt-5.3-codex-spark") {
		t.Error("model should be available after cooldown has expired")
	}
}

func TestCooldownTracker_FilterAvailableRemovesCooledDown(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	tracker.MarkCooledDown("gemini", "gemini-2.5-pro", 10*time.Second)

	models := []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-2.0-flash"}
	available := tracker.FilterAvailable("gemini", models)

	for _, m := range available {
		if m == "gemini-2.5-pro" {
			t.Error("cooled-down model should be excluded from FilterAvailable result")
		}
	}
	if len(available) != 2 {
		t.Errorf("expected 2 available models, got %d", len(available))
	}
}

func TestCooldownTracker_FilterAvailableWithNoCooldowns(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	models := []string{"claude-opus-4-5", "claude-sonnet-4-5", "claude-haiku-4-5"}
	available := tracker.FilterAvailable("claude", models)

	if len(available) != len(models) {
		t.Errorf("expected all %d models available, got %d", len(models), len(available))
	}
	for i, m := range models {
		if available[i] != m {
			t.Errorf("expected model %q at index %d, got %q", m, i, available[i])
		}
	}
}

func TestCooldownTracker_ReMarkExtendsCooldown(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	// Mark with a very short cooldown that would expire soon.
	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 80*time.Millisecond)

	// Re-mark with a longer cooldown before the first one expires.
	time.Sleep(20 * time.Millisecond)
	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 10*time.Second)

	// The original short cooldown (80ms) would have expired by now if not extended.
	time.Sleep(100 * time.Millisecond)

	if tracker.IsAvailable("codex", "gpt-5.3-codex-spark") {
		t.Error("extended cooldown should still be active; re-mark did not extend")
	}
}

func TestCooldownTracker_FilterAvailableEmptyInput(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()
	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 10*time.Second)

	available := tracker.FilterAvailable("codex", []string{})

	if available == nil {
		t.Error("FilterAvailable should return a non-nil slice for empty input")
	}
	if len(available) != 0 {
		t.Errorf("expected 0 models, got %d", len(available))
	}
}

func TestCooldownTracker_ConcurrentAccess(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Half the goroutines mark models as cooled down.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 5*time.Second)
		}()
	}

	// The other half check availability concurrently.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			// Result can be either available or unavailable depending on
			// scheduling; we just verify no race or panic occurs.
			_ = tracker.IsAvailable("codex", "gpt-5.3-codex-spark")
		}()
	}

	wg.Wait()

	// After all goroutines finish, the model must be on cooldown.
	if tracker.IsAvailable("codex", "gpt-5.3-codex-spark") {
		t.Error("model should be on cooldown after concurrent marks")
	}
}

func TestCooldownTracker_IsolationBetweenCLIs(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	// Same model name, different CLIs.
	tracker.MarkCooledDown("codex", "o4-mini", 10*time.Second)

	// The model under a different CLI should remain available.
	if !tracker.IsAvailable("gemini", "o4-mini") {
		t.Error("cooldown for 'codex:o4-mini' should not affect 'gemini:o4-mini'")
	}
}
