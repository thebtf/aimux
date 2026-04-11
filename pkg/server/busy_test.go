package server

import (
	"testing"
	"time"
)

func TestBuildBusyPayload_DefaultDuration(t *testing.T) {
	p := buildBusyPayload("job-1", "exec:codex", 0)

	if p["id"] != "job-1" {
		t.Errorf("id = %v, want job-1", p["id"])
	}
	if p["task"] != "exec:codex" {
		t.Errorf("task = %v, want exec:codex", p["task"])
	}
	if p["estimatedDurationMs"] != DefaultBusyEstimateMs {
		t.Errorf("estimatedDurationMs = %v, want %d", p["estimatedDurationMs"], DefaultBusyEstimateMs)
	}

	startedAtStr, ok := p["startedAt"].(string)
	if !ok {
		t.Fatalf("startedAt is not a string: %v", p["startedAt"])
	}
	if _, err := time.Parse(time.RFC3339, startedAtStr); err != nil {
		t.Errorf("startedAt = %q is not RFC3339: %v", startedAtStr, err)
	}
}

func TestBuildBusyPayload_NegativeDuration_FallsBackToDefault(t *testing.T) {
	p := buildBusyPayload("job-2", "agent:researcher", -5)
	if p["estimatedDurationMs"] != DefaultBusyEstimateMs {
		t.Errorf("negative duration should fall back to default; got %v", p["estimatedDurationMs"])
	}
}

func TestBuildBusyPayload_ExplicitDuration(t *testing.T) {
	p := buildBusyPayload("job-3", "audit", 90000)
	if p["estimatedDurationMs"] != 90000 {
		t.Errorf("estimatedDurationMs = %v, want 90000", p["estimatedDurationMs"])
	}
}

func TestBuildIdlePayload(t *testing.T) {
	p := buildIdlePayload("job-1")
	if len(p) != 1 {
		t.Errorf("idle payload should have exactly 1 key, got %d", len(p))
	}
	if p["id"] != "job-1" {
		t.Errorf("id = %v, want job-1", p["id"])
	}
}

func TestBuildBusyPayload_StartedAtIsUTC(t *testing.T) {
	p := buildBusyPayload("job-4", "task", 1000)
	startedAtStr := p["startedAt"].(string)
	parsed, err := time.Parse(time.RFC3339, startedAtStr)
	if err != nil {
		t.Fatalf("parse startedAt: %v", err)
	}
	if parsed.Location() != time.UTC {
		t.Errorf("startedAt should be UTC, got %v", parsed.Location())
	}
}
