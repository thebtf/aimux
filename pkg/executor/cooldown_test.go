package executor_test

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
)

func TestCooldownTracker_MarkedModelUnavailableBeforeExpiry(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 10*time.Second, "")

	if tracker.IsAvailable("codex", "gpt-5.3-codex-spark") {
		t.Error("model should be unavailable while cooldown is active")
	}
}

func TestCooldownTracker_ModelAvailableAfterExpiry(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 50*time.Millisecond, "")

	time.Sleep(100 * time.Millisecond)

	if !tracker.IsAvailable("codex", "gpt-5.3-codex-spark") {
		t.Error("model should be available after cooldown has expired")
	}
}

func TestCooldownTracker_FilterAvailableRemovesCooledDown(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	tracker.MarkCooledDown("gemini", "gemini-2.5-pro", 10*time.Second, "")

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
	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 80*time.Millisecond, "")

	// Re-mark with a longer cooldown before the first one expires.
	time.Sleep(20 * time.Millisecond)
	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 10*time.Second, "")

	// The original short cooldown (80ms) would have expired by now if not extended.
	time.Sleep(100 * time.Millisecond)

	if tracker.IsAvailable("codex", "gpt-5.3-codex-spark") {
		t.Error("extended cooldown should still be active; re-mark did not extend")
	}
}

func TestCooldownTracker_FilterAvailableEmptyInput(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()
	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 10*time.Second, "")

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
			tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 5*time.Second, "")
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
	tracker.MarkCooledDown("codex", "o4-mini", 10*time.Second, "")

	// The model under a different CLI should remain available.
	if !tracker.IsAvailable("gemini", "o4-mini") {
		t.Error("cooldown for 'codex:o4-mini' should not affect 'gemini:o4-mini'")
	}
}

// --- T011: SetDuration / Flush / List / INFO log ---

// TestCooldownTracker_SetDuration verifies that SetDuration overrides the
// cooldown duration applied by the next MarkCooledDown call for a specific
// (cli, model) pair. The override is not retroactive.
func TestCooldownTracker_SetDuration(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	// Override: 50 ms (very short so we can verify expiry quickly).
	tracker.SetDuration("codex", "gpt-5.3-codex-spark", 50*time.Millisecond)

	// Mark with a long caller-provided duration — SetDuration override should win.
	tracker.MarkCooledDown("codex", "gpt-5.3-codex-spark", 10*time.Second, "")

	// Immediately after: must be on cooldown.
	if tracker.IsAvailable("codex", "gpt-5.3-codex-spark") {
		t.Error("SetDuration: model should be unavailable immediately after MarkCooledDown")
	}

	// After the short override expires: must be available again.
	time.Sleep(120 * time.Millisecond)
	if !tracker.IsAvailable("codex", "gpt-5.3-codex-spark") {
		t.Error("SetDuration: model should be available after the 50ms override expired (caller duration 10s was overridden)")
	}
}

// TestCooldownTracker_Flush verifies that Flush removes an active cooldown entry
// immediately, making the model available before the natural expiry.
func TestCooldownTracker_Flush(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	tracker.MarkCooledDown("gemini", "gemini-2.5-pro", 10*time.Second, "")

	// Verify it is on cooldown.
	if tracker.IsAvailable("gemini", "gemini-2.5-pro") {
		t.Fatal("Flush pre-condition: model should be unavailable")
	}

	// Flush the entry.
	if err := tracker.Flush("gemini", "gemini-2.5-pro"); err != nil {
		t.Fatalf("Flush: unexpected error: %v", err)
	}

	// Must be available now.
	if !tracker.IsAvailable("gemini", "gemini-2.5-pro") {
		t.Error("Flush: model should be available after explicit flush")
	}
}

// TestCooldownTracker_Flush_NonExistent verifies that Flush returns an error
// when no entry exists for the given (cli, model) pair.
func TestCooldownTracker_Flush_NonExistent(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	err := tracker.Flush("codex", "nonexistent-model")
	if err == nil {
		t.Error("Flush on unknown key: expected non-nil error, got nil")
	}
}

// TestCooldownTracker_List verifies that List returns all active (non-expired)
// entries and excludes expired ones.
func TestCooldownTracker_List(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	// Two active entries.
	tracker.MarkCooledDown("codex", "model-a", 10*time.Second, "")
	tracker.MarkCooledDown("gemini", "model-b", 10*time.Second, "")
	// One entry that expires immediately.
	tracker.MarkCooledDown("claude", "model-c", 1*time.Millisecond, "")

	// Wait for model-c to expire.
	time.Sleep(20 * time.Millisecond)

	entries := tracker.List()

	// Only model-a and model-b should be present (model-c expired).
	if len(entries) != 2 {
		t.Errorf("List: expected 2 active entries, got %d: %v", len(entries), entries)
	}

	// Verify CLI and Model fields are populated.
	for _, e := range entries {
		if e.CLI == "" || e.Model == "" {
			t.Errorf("List: entry has empty CLI or Model: %+v", e)
		}
		if e.ExpiresAt.IsZero() {
			t.Errorf("List: entry has zero ExpiresAt: %+v", e)
		}
	}
}

// TestCooldownTracker_List_Empty verifies that List returns nil (or empty slice)
// when there are no active entries.
func TestCooldownTracker_List_Empty(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	entries := tracker.List()
	if len(entries) != 0 {
		t.Errorf("List on empty tracker: expected 0 entries, got %d", len(entries))
	}
}

// TestCooldownTracker_MarkCooledDown_INFOLog verifies that MarkCooledDown emits
// an INFO log line containing cli, model, and duration fields.
// The test redirects the standard logger's output to a bytes.Buffer.
func TestCooldownTracker_MarkCooledDown_INFOLog(t *testing.T) {
	var buf bytes.Buffer

	// Redirect standard logger to buffer; restore after test.
	oldFlags := log.Flags()
	oldWriter := log.Writer()
	log.SetOutput(&buf)
	log.SetFlags(0) // strip timestamps for deterministic comparison
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	})

	tracker := executor.NewModelCooldownTracker()
	tracker.MarkCooledDown("mytest-cli", "my-model", 30*time.Second, "some-stderr")

	logOutput := buf.String()

	// Must contain INFO level indicator.
	if !strings.Contains(logOutput, "INFO") {
		t.Errorf("MarkCooledDown: log line missing 'INFO' prefix: %q", logOutput)
	}
	// Must include the CLI name.
	if !strings.Contains(logOutput, "mytest-cli") {
		t.Errorf("MarkCooledDown: log line missing cli name 'mytest-cli': %q", logOutput)
	}
	// Must include the model name.
	if !strings.Contains(logOutput, "my-model") {
		t.Errorf("MarkCooledDown: log line missing model name 'my-model': %q", logOutput)
	}
	// Must include duration.
	if !strings.Contains(logOutput, "30s") {
		t.Errorf("MarkCooledDown: log line missing duration '30s': %q", logOutput)
	}
}

// TestCooldownTracker_TriggerStderrStoredAndRetrieved verifies that the
// triggerStderr argument to MarkCooledDown is stored in the CooldownEntry
// and returned by List.
func TestCooldownTracker_TriggerStderrStoredAndRetrieved(t *testing.T) {
	tracker := executor.NewModelCooldownTracker()

	tracker.MarkCooledDown("codex", "model-x", 10*time.Second, "rate limit: 5000/day exceeded")

	entries := tracker.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	// TriggerStderr is stored (potentially redacted but non-empty for non-secret input).
	if entries[0].TriggerStderr == "" {
		t.Error("CooldownEntry.TriggerStderr should be non-empty when non-empty triggerStderr was passed")
	}
}
