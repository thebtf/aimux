package budget

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func makeRequest(params map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = params
	return req
}

func TestParseBudgetParams(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		if got.Limit != DefaultLimit {
			t.Fatalf("Limit = %d, want %d", got.Limit, DefaultLimit)
		}
		if got.Offset != 0 {
			t.Fatalf("Offset = %d, want 0", got.Offset)
		}
		if got.IncludeContent {
			t.Fatalf("IncludeContent = true, want false")
		}
		if got.Tail != 0 {
			t.Fatalf("Tail = %d, want 0", got.Tail)
		}
		if got.Fields != nil {
			t.Fatalf("Fields = %#v, want nil", got.Fields)
		}
	})

	t.Run("limit=50", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"limit": 50}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		if got.Limit != 50 {
			t.Fatalf("Limit = %d, want 50", got.Limit)
		}
		if got.LimitClamped {
			t.Fatalf("LimitClamped = true, want false")
		}
	})

	t.Run("limit=200", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"limit": 200}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		if got.Limit != MaxLimit {
			t.Fatalf("Limit = %d, want %d", got.Limit, MaxLimit)
		}
		if !got.LimitClamped {
			t.Fatalf("LimitClamped = false, want true")
		}
	})

	t.Run("limit=0", func(t *testing.T) {
		_, err := ParseBudgetParams(makeRequest(map[string]any{"limit": 0}))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "limit must be >= 1") {
			t.Fatalf("error = %q", err.Error())
		}
	})

	t.Run("limit=-1", func(t *testing.T) {
		_, err := ParseBudgetParams(makeRequest(map[string]any{"limit": -1}))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "limit must be >= 1") {
			t.Fatalf("error = %q", err.Error())
		}
	})

	t.Run("offset=-1", func(t *testing.T) {
		_, err := ParseBudgetParams(makeRequest(map[string]any{"offset": -1}))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "offset must be >= 0") {
			t.Fatalf("error = %q", err.Error())
		}
	})

	t.Run("tail=0", func(t *testing.T) {
		_, err := ParseBudgetParams(makeRequest(map[string]any{"tail": 0}))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "tail must be >= 1") {
			t.Fatalf("error = %q", err.Error())
		}
	})

	t.Run("tail=-5", func(t *testing.T) {
		_, err := ParseBudgetParams(makeRequest(map[string]any{"tail": -5}))
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "tail must be >= 1") {
			t.Fatalf("error = %q", err.Error())
		}
	})

	t.Run("tail=10", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"tail": 10}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		if got.Tail != 10 {
			t.Fatalf("Tail = %d, want 10", got.Tail)
		}
	})

	t.Run("fields=id,status", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"fields": "id,status"}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		want := []string{"id", "status"}
		if len(got.Fields) != len(want) {
			t.Fatalf("len(Fields) = %d, want %d", len(got.Fields), len(want))
		}
		for i := range want {
			if got.Fields[i] != want[i] {
				t.Fatalf("Fields[%d] = %q, want %q", i, got.Fields[i], want[i])
			}
		}
	})

	t.Run("fields spaced", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"fields": " id , status "}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		want := []string{"id", "status"}
		if len(got.Fields) != len(want) {
			t.Fatalf("len(Fields) = %d, want %d", len(got.Fields), len(want))
		}
		for i := range want {
			if got.Fields[i] != want[i] {
				t.Fatalf("Fields[%d] = %q, want %q", i, got.Fields[i], want[i])
			}
		}
	})

	t.Run("fields empty", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"fields": ""}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		if got.Fields != nil {
			t.Fatalf("Fields = %#v, want nil", got.Fields)
		}
	})

	t.Run("sessions_limit and loom_limit", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"sessions_limit": 5, "loom_limit": 10}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		if got.SessionsLimit != 5 {
			t.Fatalf("SessionsLimit = %d, want 5", got.SessionsLimit)
		}
		if got.LoomLimit != 10 {
			t.Fatalf("LoomLimit = %d, want 10", got.LoomLimit)
		}
	})

	t.Run("sessions_offset negative", func(t *testing.T) {
		_, err := ParseBudgetParams(makeRequest(map[string]any{"sessions_offset": -1}))
		if err == nil {
			t.Fatal("expected error for negative sessions_offset")
		}
		if !strings.Contains(err.Error(), "sessions_offset must be >= 0") {
			t.Fatalf("error = %q", err.Error())
		}
	})

	t.Run("loom_offset negative", func(t *testing.T) {
		_, err := ParseBudgetParams(makeRequest(map[string]any{"loom_offset": -1}))
		if err == nil {
			t.Fatal("expected error for negative loom_offset")
		}
		if !strings.Contains(err.Error(), "loom_offset must be >= 0") {
			t.Fatalf("error = %q", err.Error())
		}
	})

	t.Run("sessions_limit negative", func(t *testing.T) {
		_, err := ParseBudgetParams(makeRequest(map[string]any{"sessions_limit": -1}))
		if err == nil {
			t.Fatal("expected error for negative sessions_limit")
		}
		if !strings.Contains(err.Error(), "sessions_limit must be >= 0") {
			t.Fatalf("error = %q", err.Error())
		}
	})

	t.Run("loom_limit negative", func(t *testing.T) {
		_, err := ParseBudgetParams(makeRequest(map[string]any{"loom_limit": -1}))
		if err == nil {
			t.Fatal("expected error for negative loom_limit")
		}
		if !strings.Contains(err.Error(), "loom_limit must be >= 0") {
			t.Fatalf("error = %q", err.Error())
		}
	})

	t.Run("sessions_limit>MaxLimit is clamped", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"sessions_limit": 999999}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		if got.SessionsLimit != MaxLimit {
			t.Fatalf("SessionsLimit = %d, want %d (clamped)", got.SessionsLimit, MaxLimit)
		}
		if !got.LimitClamped {
			t.Fatal("LimitClamped = false, want true")
		}
	})

	t.Run("loom_limit>MaxLimit is clamped", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"loom_limit": 999999}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		if got.LoomLimit != MaxLimit {
			t.Fatalf("LoomLimit = %d, want %d (clamped)", got.LoomLimit, MaxLimit)
		}
		if !got.LimitClamped {
			t.Fatal("LimitClamped = false, want true")
		}
	})

	t.Run("sessions_limit=0 is allowed (fallback to global)", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"sessions_limit": 0}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		if got.SessionsLimit != 0 {
			t.Fatalf("SessionsLimit = %d, want 0 (global fallback)", got.SessionsLimit)
		}
	})

	t.Run("loom_limit=0 is allowed (fallback to global)", func(t *testing.T) {
		got, err := ParseBudgetParams(makeRequest(map[string]any{"loom_limit": 0}))
		if err != nil {
			t.Fatalf("ParseBudgetParams() error = %v", err)
		}
		if got.LoomLimit != 0 {
			t.Fatalf("LoomLimit = %d, want 0 (global fallback)", got.LoomLimit)
		}
	})

	t.Run("tail sentinel -999 is valid", func(t *testing.T) {
		// The old sentinel trick would ignore tail=-999; now we use GetArguments() presence check.
		got, err := ParseBudgetParams(makeRequest(map[string]any{"tail": -999}))
		if err == nil {
			t.Fatalf("tail=-999 should be rejected as <= 0, got params: %+v", got)
		}
		if !strings.Contains(err.Error(), "tail must be >= 1") {
			t.Fatalf("error = %q", err.Error())
		}
	})
}
