package session_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/session"
)

// newRunningJob is a helper that creates a JobManager, creates a job, starts it,
// and returns (manager, jobID). The job is in "running" state so AppendProgress works.
func newRunningJob(t *testing.T) (*session.JobManager, string) {
	t.Helper()
	m := session.NewJobManager()
	j := m.Create("sess-test", "codex")
	m.StartJob(j.ID, 1234)
	return m, j.ID
}

// --- T001: LastOutputLine tests ---

func TestAppendProgress_LastOutputLine_SingleLine(t *testing.T) {
	m, id := newRunningJob(t)
	m.AppendProgress(id, "foo")
	m.AppendProgress(id, "bar baz")

	snap := m.GetSnapshot(id)
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	if snap.LastOutputLine != "bar baz" {
		t.Errorf("LastOutputLine = %q, want %q", snap.LastOutputLine, "bar baz")
	}
}

func TestAppendProgress_LastOutputLine_MultiLineInput(t *testing.T) {
	// AppendProgress called with embedded newlines — last non-empty segment wins.
	m, id := newRunningJob(t)
	m.AppendProgress(id, "line A\nline B\nline C")

	snap := m.GetSnapshot(id)
	if snap.LastOutputLine != "line C" {
		t.Errorf("LastOutputLine = %q, want %q", snap.LastOutputLine, "line C")
	}
}

func TestAppendProgress_LastOutputLine_WhitespaceOnlyLine(t *testing.T) {
	// If the new line is whitespace-only, LastOutputLine retains previous value.
	m, id := newRunningJob(t)
	m.AppendProgress(id, "real content")
	m.AppendProgress(id, "   ")

	snap := m.GetSnapshot(id)
	if snap.LastOutputLine != "real content" {
		t.Errorf("LastOutputLine = %q, want %q (should retain previous on whitespace-only append)", snap.LastOutputLine, "real content")
	}
}

func TestAppendProgress_LastOutputLine_Truncated150Bytes(t *testing.T) {
	// 150-byte ASCII line must be truncated to exactly 100 bytes.
	long := make([]byte, 150)
	for i := range long {
		long[i] = 'x'
	}
	m, id := newRunningJob(t)
	m.AppendProgress(id, string(long))

	snap := m.GetSnapshot(id)
	if len(snap.LastOutputLine) > 100 {
		t.Errorf("LastOutputLine len=%d, want <=100", len(snap.LastOutputLine))
	}
	if len(snap.LastOutputLine) != 100 {
		t.Errorf("LastOutputLine len=%d, want exactly 100", len(snap.LastOutputLine))
	}
}

func TestAppendProgress_LastOutputLine_RussianTruncation(t *testing.T) {
	// Each Cyrillic letter = 2 bytes. 55 letters = 110 bytes. Must truncate to 50 letters (100 bytes).
	// No codepoint splitting allowed.
	russian := ""
	for i := 0; i < 55; i++ {
		russian += "а"
	}
	m, id := newRunningJob(t)
	m.AppendProgress(id, russian)

	snap := m.GetSnapshot(id)
	if len(snap.LastOutputLine) > 100 {
		t.Errorf("Russian LastOutputLine len=%d, want <=100", len(snap.LastOutputLine))
	}
	// Verify rune count is exactly 50 (no codepoint split).
	runeCount := 0
	for range snap.LastOutputLine {
		runeCount++
	}
	if runeCount != 50 {
		t.Errorf("Russian rune count=%d, want 50 (no codepoint split)", runeCount)
	}
}

func TestAppendProgress_LastOutputLine_EmptyBuffer(t *testing.T) {
	// No AppendProgress calls → LastOutputLine should be "".
	m := session.NewJobManager()
	j := m.Create("sess-test", "codex")
	m.StartJob(j.ID, 1234)

	snap := m.GetSnapshot(j.ID)
	if snap.LastOutputLine != "" {
		t.Errorf("LastOutputLine = %q, want empty string for zero-progress job", snap.LastOutputLine)
	}
}

// --- T002: ProgressLines tests ---

func TestAppendProgress_ProgressLines_Empty(t *testing.T) {
	m := session.NewJobManager()
	j := m.Create("sess-test", "codex")
	m.StartJob(j.ID, 1234)

	snap := m.GetSnapshot(j.ID)
	if snap.ProgressLines != 0 {
		t.Errorf("ProgressLines = %d, want 0 for empty buffer", snap.ProgressLines)
	}
}

func TestAppendProgress_ProgressLines_FiveLines(t *testing.T) {
	m, id := newRunningJob(t)
	m.AppendProgress(id, "line 1")
	m.AppendProgress(id, "line 2")
	m.AppendProgress(id, "line 3")
	m.AppendProgress(id, "line 4")
	m.AppendProgress(id, "line 5")

	snap := m.GetSnapshot(id)
	// Each call contributes 1 line (no embedded newlines) → 5 total.
	if snap.ProgressLines != 5 {
		t.Errorf("ProgressLines = %d, want 5 for 5 appended lines", snap.ProgressLines)
	}
}

func TestAppendProgress_ProgressLines_EmbeddedNewlines(t *testing.T) {
	// Appending "a\nb\nc" counts as 1 (base) + 2 (embedded '\n') = 3 lines.
	m, id := newRunningJob(t)
	m.AppendProgress(id, "a\nb\nc")

	snap := m.GetSnapshot(id)
	// 1 call + 2 embedded newlines = 3.
	if snap.ProgressLines != 3 {
		t.Errorf("ProgressLines = %d, want 3 for 'a\\nb\\nc'", snap.ProgressLines)
	}
}

func TestAppendProgress_ProgressLines_MonotonicallyIncreasing(t *testing.T) {
	m, id := newRunningJob(t)
	prev := 0
	for i := 0; i < 10; i++ {
		m.AppendProgress(id, "line")
		snap := m.GetSnapshot(id)
		if snap.ProgressLines < prev {
			t.Errorf("ProgressLines decreased from %d to %d at iteration %d", prev, snap.ProgressLines, i)
		}
		prev = snap.ProgressLines
	}
}

// Swap-body guard: verifies that returning false from AppendProgress would fail tests.
func TestAppendProgress_SwapBodyGuard(t *testing.T) {
	m, id := newRunningJob(t)
	ok := m.AppendProgress(id, "test line")
	if !ok {
		t.Error("AppendProgress returned false for a running job — stub guard triggered")
	}
	snap := m.GetSnapshot(id)
	if snap.LastOutputLine == "" {
		t.Error("LastOutputLine is empty after AppendProgress — stub guard triggered")
	}
}
