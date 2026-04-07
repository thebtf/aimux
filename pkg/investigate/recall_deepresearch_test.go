package investigate

import (
	"testing"

	"github.com/thebtf/aimux/pkg/tools/deepresearch"
)

// TestRecallReport_DeepresearchCache verifies that RecallReport finds entries
// persisted in the deepresearch disk cache when no investigation report matches.
func TestRecallReport_DeepresearchCache(t *testing.T) {
	dir := setupReportsDir(t) // sets up .agent/reports/ (from report_recall_test.go helpers)

	// Write a fake deepresearch cache entry to disk.
	err := deepresearch.SaveEntryToDisk(dir, "distributed tracing with OpenTelemetry", "summary", "gemini-2.0-flash", nil, "OpenTelemetry provides distributed tracing capabilities...")
	if err != nil {
		t.Fatalf("SaveEntryToDisk: %v", err)
	}

	// Query that matches the deepresearch topic but not any investigation report.
	result, err := RecallReport(dir, "opentelemetry")
	if err != nil {
		t.Fatalf("RecallReport: %v", err)
	}
	if result == nil {
		t.Fatal("expected deepresearch cache hit, got nil")
	}
	if result.Topic != "distributed tracing with OpenTelemetry" {
		t.Errorf("Topic = %q, want %q", result.Topic, "distributed tracing with OpenTelemetry")
	}
	if result.Content == "" {
		t.Error("expected non-empty content from deepresearch cache entry")
	}
}

// TestRecallReport_DeepresearchCacheContentMatch verifies content-based matching
// against the deepresearch cache entry body.
func TestRecallReport_DeepresearchCacheContentMatch(t *testing.T) {
	dir := setupReportsDir(t)

	// Topic doesn't contain query, but content does.
	err := deepresearch.SaveEntryToDisk(dir, "go concurrency patterns", "summary", "gemini-2.0-flash", nil, "The errgroup package provides goroutine lifecycle management.")
	if err != nil {
		t.Fatalf("SaveEntryToDisk: %v", err)
	}

	result, err := RecallReport(dir, "errgroup")
	if err != nil {
		t.Fatalf("RecallReport: %v", err)
	}
	if result == nil {
		t.Fatal("expected content match from deepresearch cache, got nil")
	}
	if result.Filename != "deepresearch-cache" {
		t.Errorf("expected source deepresearch-cache, got %q", result.Filename)
	}
}

// TestRecallReport_InvestigationBeforeDeepresearch verifies investigation reports
// take priority over deepresearch cache entries.
func TestRecallReport_InvestigationBeforeDeepresearch(t *testing.T) {
	dir := setupReportsDir(t)

	// Write both an investigation report and a deepresearch cache entry for same topic.
	writeReport(t, dir, "investigate-memory-leak-2026-04-01T10-00-00.md", "# Memory Leak\n\nInvestigation findings about memory leak.")
	err := deepresearch.SaveEntryToDisk(dir, "memory leak patterns", "summary", "gemini-2.0-flash", nil, "Memory leaks occur when...")
	if err != nil {
		t.Fatalf("SaveEntryToDisk: %v", err)
	}

	result, err := RecallReport(dir, "memory leak")
	if err != nil {
		t.Fatalf("RecallReport: %v", err)
	}
	if result == nil {
		t.Fatal("expected a result")
	}
	// Should prefer investigation report (filename starts with investigate-)
	if result.Filename == "deepresearch-cache" {
		t.Error("expected investigation report to take priority over deepresearch cache")
	}
}

// TestRecallReport_NoCacheDir verifies graceful handling when deepresearch cache dir absent.
func TestRecallReport_NoCacheDir(t *testing.T) {
	dir := setupReportsDir(t)
	// No .agent/deepresearch/ created, no investigation reports matching query.

	result, err := RecallReport(dir, "quantum computing")
	if err != nil {
		t.Fatalf("RecallReport: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil when no cache dir and no reports, got %v", result)
	}
}
