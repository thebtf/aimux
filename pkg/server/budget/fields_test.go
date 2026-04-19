package budget

import (
	"reflect"
	"strings"
	"testing"
)

func TestApplyFields(t *testing.T) {
	t.Run("empty fields uses whitelist", func(t *testing.T) {
		src := map[string]any{
			"job_id":         "job-1",
			"status":         "running",
			"content":        "hidden",
			"truncated":      false,
			"content_length": 10,
			"extra":          "omit",
		}

		got, omitted, err := ApplyFields(src, nil, FieldWhitelist["status"])
		if err != nil {
			t.Fatalf("ApplyFields() error = %v", err)
		}

		// "status" whitelist now includes "content" (synced with ContentBearingFields).
		// truncated + content_length pass as policy metadata.
		// Expected keys: job_id, status, content, truncated, content_length = 5.
		if len(got) != 5 {
			t.Fatalf("len(got) = %d, want 5", len(got))
		}

		expected := map[string]struct{}{"extra": {}}
		if len(omitted) != len(expected) {
			t.Fatalf("len(omitted) = %d, want %d; omitted=%v", len(omitted), len(expected), omitted)
		}
		for _, field := range omitted {
			if _, ok := expected[field]; !ok {
				t.Fatalf("omitted includes unexpected field %q", field)
			}
		}
	})

	t.Run("explicit subset", func(t *testing.T) {
		src := map[string]any{
			"job_id":    "job-1",
			"status":    "running",
			"content":   "hidden",
			"truncated": true,
			"extra":     "omit",
		}

		got, _, err := ApplyFields(src, []string{"status"}, FieldWhitelist["status"])
		if err != nil {
			t.Fatalf("ApplyFields() error = %v", err)
		}

		if got["status"] != "running" {
			t.Fatalf("status = %q, want running", got["status"])
		}
		if got["truncated"] != true {
			t.Fatalf("truncated = %v, want true", got["truncated"])
		}
		if _, ok := got["job_id"]; ok {
			t.Fatalf("job_id should be omitted")
		}
		if _, ok := got["content"]; ok {
			t.Fatalf("content should be omitted")
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		_, _, err := ApplyFields(map[string]any{"job_id": "job-1"}, []string{"bogus"}, FieldWhitelist["status"])
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("policy metadata keys pass through", func(t *testing.T) {
		src := map[string]any{
			"content":             "x",
			"truncated":           true,
			"hint":                "trim later",
			"sessions_pagination": map[string]any{"has_more": true},
		}

		got, _, err := ApplyFields(src, []string{"status"}, FieldWhitelist["status"])
		if err != nil {
			t.Fatalf("ApplyFields() error = %v", err)
		}

		if got["truncated"] != true {
			t.Fatalf("truncated = %v", got["truncated"])
		}
		if got["hint"] != "trim later" {
			t.Fatalf("hint = %q", got["hint"])
		}
		if _, ok := got["sessions_pagination"]; !ok {
			t.Fatalf("sessions_pagination should pass through")
		}
	})

	t.Run("empty whitelist empty fields", func(t *testing.T) {
		src := map[string]any{"a": 1, "b": 2}

		got, omitted, err := ApplyFields(src, nil, []string{})
		if err != nil {
			t.Fatalf("ApplyFields() error = %v", err)
		}

		if !reflect.DeepEqual(got, src) {
			t.Fatalf("got = %#v", got)
		}
		if !reflect.DeepEqual(omitted, []string{}) {
			t.Fatalf("omitted = %#v", omitted)
		}
	})
}

// TestFieldWhitelist_StatusProgressTailFields verifies T004 AC:
// progress_tail and progress_lines are in FieldWhitelist["status"].
func TestFieldWhitelist_StatusProgressTailFields(t *testing.T) {
	whitelist := FieldWhitelist["status"]

	hasProgressTail := false
	hasProgressLines := false
	for _, f := range whitelist {
		switch f {
		case "progress_tail":
			hasProgressTail = true
		case "progress_lines":
			hasProgressLines = true
		}
	}

	if !hasProgressTail {
		t.Error("progress_tail not found in FieldWhitelist[\"status\"]")
	}
	if !hasProgressLines {
		t.Error("progress_lines not found in FieldWhitelist[\"status\"]")
	}
}

// TestApplyFields_ProgressTailFilter verifies that fields=progress_tail
// successfully passes ApplyFields without an unknown-field error (T004 AC).
func TestApplyFields_ProgressTailFilter(t *testing.T) {
	src := map[string]any{
		"job_id":         "job-1",
		"status":         "running",
		"progress_tail":  "Processing file 42/100",
		"progress_lines": 42,
	}

	got, _, err := ApplyFields(src, []string{"progress_tail"}, FieldWhitelist["status"])
	if err != nil {
		t.Fatalf("ApplyFields fields=progress_tail: unexpected error: %v", err)
	}
	if got["progress_tail"] != "Processing file 42/100" {
		t.Errorf("progress_tail = %v, want %q", got["progress_tail"], "Processing file 42/100")
	}
}

// TestApplyFields_ProgressLinesFilter verifies that fields=progress_lines
// successfully passes ApplyFields without an unknown-field error (T004 AC).
func TestApplyFields_ProgressLinesFilter(t *testing.T) {
	src := map[string]any{
		"job_id":         "job-1",
		"status":         "running",
		"progress_lines": 17,
	}

	got, _, err := ApplyFields(src, []string{"progress_lines"}, FieldWhitelist["status"])
	if err != nil {
		t.Fatalf("ApplyFields fields=progress_lines: unexpected error: %v", err)
	}
	if got["progress_lines"] != 17 {
		t.Errorf("progress_lines = %v, want 17", got["progress_lines"])
	}
}

// Swap-body guard for T004: if FieldWhitelist["status"] were empty/nil,
// both TestFieldWhitelist_StatusProgressTailFields checks above would fail.

func TestValidateContentBearingFields(t *testing.T) {
	t.Run("content field without include", func(t *testing.T) {
		err := ValidateContentBearingFields([]string{"content"}, ContentBearingFields["status"], false)
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "field \"content\" requires include_content=true; fields cannot bypass the content opt-in" {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("content field with include", func(t *testing.T) {
		if err := ValidateContentBearingFields([]string{"content"}, ContentBearingFields["status"], true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("no content fields", func(t *testing.T) {
		if err := ValidateContentBearingFields([]string{"status"}, ContentBearingFields["status"], false); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
