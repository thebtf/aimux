package server

import (
	"context"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// TestHandleStatus_ToolDescription_MentionsProgressTail verifies that the
// status tool description mentions progress_tail and progress_lines (T008 AC).
func TestHandleStatus_ToolDescription_MentionsProgressTail(t *testing.T) {
	srv := testServer(t)
	desc := srv.ToolDescription("status")
	if desc == "" {
		t.Fatal("status tool description is empty")
	}
	if !strings.Contains(desc, "progress_tail") {
		t.Errorf("status tool description does not mention 'progress_tail': %q", desc)
	}
	if !strings.Contains(desc, "progress_lines") {
		t.Errorf("status tool description does not mention 'progress_lines': %q", desc)
	}
	// Swap-body guard: if description were empty/stub, both checks above catch it.
}

// TestHandleStatus_ProgressTailFields verifies that handleStatus includes
// progress_tail and progress_lines in the response for every job status.
// AC for T003: fields present regardless of status value (running/completed/failed).
func TestHandleStatus_ProgressTailFields_Running(t *testing.T) {
	srv := testServer(t)
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 0)

	// Append some progress so LastOutputLine is set.
	srv.jobs.AppendProgress(job.ID, "foo")
	srv.jobs.AppendProgress(job.ID, "bar baz")

	req := makeRequest("status", map[string]any{"job_id": job.ID})
	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	data := parseResult(t, result)

	tail, tailOK := data["progress_tail"]
	if !tailOK {
		t.Error("progress_tail field missing from running status response")
	}
	if tailStr, _ := tail.(string); tailStr != "bar baz" {
		t.Errorf("progress_tail = %q, want %q", tailStr, "bar baz")
	}

	lines, linesOK := data["progress_lines"]
	if !linesOK {
		t.Error("progress_lines field missing from running status response")
	}
	// Two AppendProgress calls, no embedded newlines → ProgressLines = 2.
	if linesF, _ := lines.(float64); linesF != 2 {
		t.Errorf("progress_lines = %v, want 2", lines)
	}
}

func TestHandleStatus_ProgressTailFields_Completed(t *testing.T) {
	srv := testServer(t)
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 0)
	srv.jobs.AppendProgress(job.ID, "processing 1/10")
	srv.jobs.AppendProgress(job.ID, "processing 10/10")
	srv.jobs.AppendProgress(job.ID, "done")
	srv.jobs.CompleteJob(job.ID, "output", 0)

	req := makeRequest("status", map[string]any{"job_id": job.ID})
	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	data := parseResult(t, result)
	if _, ok := data["progress_tail"]; !ok {
		t.Error("progress_tail field missing from completed status response")
	}
	if tailStr, _ := data["progress_tail"].(string); tailStr != "done" {
		t.Errorf("progress_tail = %q, want %q", tailStr, "done")
	}
	if _, ok := data["progress_lines"]; !ok {
		t.Error("progress_lines field missing from completed status response")
	}
	// 3 AppendProgress calls, no embedded newlines → ProgressLines = 3.
	if linesF, _ := data["progress_lines"].(float64); linesF != 3 {
		t.Errorf("progress_lines = %v, want 3", data["progress_lines"])
	}
}

func TestHandleStatus_ProgressTailFields_Failed(t *testing.T) {
	srv := testServer(t)
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 0)
	srv.jobs.AppendProgress(job.ID, "partial output")
	srv.jobs.FailJob(job.ID, types.NewExecutorError("test failure", nil, ""))

	req := makeRequest("status", map[string]any{"job_id": job.ID})
	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	data := parseResult(t, result)
	if _, ok := data["progress_tail"]; !ok {
		t.Error("progress_tail field missing from failed status response")
	}
	if _, ok := data["progress_lines"]; !ok {
		t.Error("progress_lines field missing from failed status response")
	}
}

func TestHandleStatus_ProgressTailFields_ZeroProgress(t *testing.T) {
	// Job with no progress writes: progress_tail="" and progress_lines=0.
	// Both fields are explicitly set in the result map from handleStatus even for
	// zero values, so they must appear in the JSON response.
	srv := testServer(t)
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 0)

	req := makeRequest("status", map[string]any{"job_id": job.ID})
	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	data := parseResult(t, result)

	// progress_tail must be present (empty string for zero-progress job).
	if _, ok := data["progress_tail"]; !ok {
		t.Error("progress_tail field missing from zero-progress status response")
	}
	if tailStr, _ := data["progress_tail"].(string); tailStr != "" {
		t.Errorf("progress_tail = %q, want empty string for zero-progress job", tailStr)
	}

	// progress_lines must be present (0 for zero-progress job).
	if _, ok := data["progress_lines"]; !ok {
		t.Error("progress_lines field missing from zero-progress status response")
	}
	if linesF, _ := data["progress_lines"].(float64); linesF != 0 {
		t.Errorf("progress_lines = %v, want 0 for zero-progress job", data["progress_lines"])
	}
}

// TestHandleStatus_ProgressTail_FieldsFilter verifies that fields=progress_tail
// filter works (T004 AC: 'fields=progress_tail filter works').
func TestHandleStatus_ProgressTail_FieldsFilter(t *testing.T) {
	srv := testServer(t)
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 0)
	srv.jobs.AppendProgress(job.ID, "hello world")

	req := makeRequest("status", map[string]any{
		"job_id": job.ID,
		"fields": "progress_tail",
	})
	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus fields=progress_tail: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", parseResult(t, result))
	}

	data := parseResult(t, result)
	if _, ok := data["progress_tail"]; !ok {
		t.Error("progress_tail missing from fields=progress_tail filtered response")
	}
}

// TestHandleStatus_ProgressTail_Truncation verifies the 100-byte cap is enforced
// end-to-end through handleStatus (status response never has progress_tail > 100 bytes).
func TestHandleStatus_ProgressTail_Truncation(t *testing.T) {
	srv := testServer(t)
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 0)

	// 150-byte single line.
	longLine := strings.Repeat("z", 150)
	srv.jobs.AppendProgress(job.ID, longLine)

	req := makeRequest("status", map[string]any{"job_id": job.ID})
	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	data := parseResult(t, result)
	tail, _ := data["progress_tail"].(string)
	if len(tail) > 100 {
		t.Errorf("progress_tail len=%d exceeds 100-byte cap", len(tail))
	}
	if len(tail) != 100 {
		t.Errorf("progress_tail len=%d, want exactly 100 for 150-byte input", len(tail))
	}
}

// TestHandleStatus_ProgressTail_SwapBodyGuard is the swap-body guard for T003.
// If handleStatus were stubbed to return nil/empty, progress_tail would be absent
// or empty even after appending a non-empty line.
func TestHandleStatus_ProgressTail_SwapBodyGuard(t *testing.T) {
	srv := testServer(t)
	sess := srv.sessions.Create("codex", types.SessionModeOnceStateful, "/tmp")
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 0)
	srv.jobs.AppendProgress(job.ID, "guard line")

	req := makeRequest("status", map[string]any{"job_id": job.ID})
	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if result == nil {
		t.Fatal("handleStatus returned nil — stub guard triggered")
	}
	data := parseResult(t, result)
	tail, _ := data["progress_tail"].(string)
	if tail != "guard line" {
		t.Errorf("progress_tail = %q, want %q — stub guard: value must reflect last append", tail, "guard line")
	}
	lines, _ := data["progress_lines"].(float64)
	if lines != 1 {
		t.Errorf("progress_lines = %v, want 1 — stub guard: must count appended lines", lines)
	}
}
