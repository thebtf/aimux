package investigate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupReportsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	reportsDir := filepath.Join(dir, ".agent", "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeReport(t *testing.T, cwd, filename, content string) {
	t.Helper()
	fpath := filepath.Join(cwd, ".agent", "reports", filename)
	if err := os.WriteFile(fpath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListReports_Empty(t *testing.T) {
	dir := setupReportsDir(t)
	reports, err := ListReports(dir)
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(reports) != 0 {
		t.Errorf("expected 0 reports, got %d", len(reports))
	}
}

func TestListReports_NoDir(t *testing.T) {
	dir := t.TempDir() // No .agent/reports/ created
	reports, err := ListReports(dir)
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if reports != nil {
		t.Errorf("expected nil, got %v", reports)
	}
}

func TestListReports_WithReports(t *testing.T) {
	dir := setupReportsDir(t)
	writeReport(t, dir, "investigate-memory-leak-2026-04-01T10-00-00.md", "# Report 1")
	writeReport(t, dir, "investigate-auth-bug-2026-04-05T15-30-00.md", "# Report 2")
	writeReport(t, dir, "not-investigate.md", "# Ignored")

	reports, err := ListReports(dir)
	if err != nil {
		t.Fatalf("ListReports: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(reports))
	}

	// Newest first
	if reports[0].Topic == "" {
		t.Error("expected non-empty topic")
	}
	if reports[0].Date < reports[1].Date {
		t.Errorf("expected newest first: %s >= %s", reports[0].Date, reports[1].Date)
	}
}

func TestRecallReport_ExactTopic(t *testing.T) {
	dir := setupReportsDir(t)
	writeReport(t, dir, "investigate-memory-leak-2026-04-01T10-00-00.md", "# Memory Leak Investigation\n\nFindings about memory leak.")

	result, err := RecallReport(dir, "memory")
	if err != nil {
		t.Fatalf("RecallReport: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestRecallReport_ContentMatch(t *testing.T) {
	dir := setupReportsDir(t)
	writeReport(t, dir, "investigate-issue-42-2026-04-01T10-00-00.md", "# Investigation\n\nThe root cause was a race condition in the scheduler.")

	// Topic "issue 42" won't match "race condition", but content search should find it
	result, err := RecallReport(dir, "race condition")
	if err != nil {
		t.Fatalf("RecallReport: %v", err)
	}
	if result == nil {
		t.Fatal("expected result from content search, got nil")
	}
}

func TestRecallReport_NoMatch(t *testing.T) {
	dir := setupReportsDir(t)
	writeReport(t, dir, "investigate-auth-bug-2026-04-01T10-00-00.md", "# Auth Bug")

	result, err := RecallReport(dir, "nonexistent topic xyz")
	if err != nil {
		t.Fatalf("RecallReport: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for no match, got %v", result.Filename)
	}
}

func TestRecallReport_EmptyQuery(t *testing.T) {
	dir := setupReportsDir(t)
	_, err := RecallReport(dir, "")
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestCleanupExpiredReports_RemovesOld(t *testing.T) {
	dir := setupReportsDir(t)
	oldFile := filepath.Join(dir, ".agent", "reports", "investigate-old-2020-01-01T00-00-00.md")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set mtime to 1 year ago
	oldTime := time.Now().Add(-365 * 24 * time.Hour)
	os.Chtimes(oldFile, oldTime, oldTime)

	newFile := filepath.Join(dir, ".agent", "reports", "investigate-new-2026-04-01T00-00-00.md")
	if err := os.WriteFile(newFile, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	deleted, err := CleanupExpiredReports(dir, 180)
	if err != nil {
		t.Fatalf("CleanupExpiredReports: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// Verify old file is gone, new file remains
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old file should be deleted")
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Error("new file should remain")
	}
}

func TestCleanupExpiredReports_KeepsRecent(t *testing.T) {
	dir := setupReportsDir(t)
	writeReport(t, dir, "investigate-recent-2026-04-01T00-00-00.md", "recent")

	deleted, err := CleanupExpiredReports(dir, 180)
	if err != nil {
		t.Fatalf("CleanupExpiredReports: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}
}
