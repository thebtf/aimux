package workflow

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/tenant"
)

// --- C1 test helpers ---

// collectingAuditLog captures all emitted audit events for assertions.
type collectingAuditLog struct {
	mu     sync.Mutex
	events []audit.AuditEvent
}

func (c *collectingAuditLog) Emit(e audit.AuditEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *collectingAuditLog) Close() error { return nil }

func (c *collectingAuditLog) Events() []audit.AuditEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.AuditEvent, len(c.events))
	copy(out, c.events)
	return out
}

// --- C1 tests ---

// TestEngine_AuditEvents_StepStartComplete verifies that the engine emits
// workflow_step_start and workflow_step_complete events when an AuditLog is
// injected, and that tenant ID propagates from context.
func TestEngine_AuditEvents_StepStartComplete(t *testing.T) {
	al := &collectingAuditLog{}

	sender := &mockSender{responses: map[string]string{
		"analyzer": "looks good",
	}}

	eng := New(sender, nil, nil, nil, WithAuditLog(al))

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "tenant-abc",
	})

	steps := []WorkflowStep{
		{
			Name:   "analyze",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "analyzer"},
		},
	}

	result, err := eng.Execute(ctx, steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s", result.Status)
	}

	events := al.Events()
	if len(events) < 2 {
		t.Fatalf("expected at least 2 audit events, got %d", len(events))
	}

	// First event: step_start
	if events[0].EventType != audit.EventWorkflowStepStart {
		t.Errorf("event[0] type = %s, want workflow_step_start", events[0].EventType)
	}
	if events[0].TenantID != "tenant-abc" {
		t.Errorf("event[0] tenant = %q, want tenant-abc", events[0].TenantID)
	}
	if events[0].ResourceID != "analyze" {
		t.Errorf("event[0] resource = %q, want analyze", events[0].ResourceID)
	}

	// Second event: step_complete
	if events[1].EventType != audit.EventWorkflowStepComplete {
		t.Errorf("event[1] type = %s, want workflow_step_complete", events[1].EventType)
	}
	if events[1].ExtraFields["status"] != "completed" {
		t.Errorf("event[1] status = %q, want completed", events[1].ExtraFields["status"])
	}
}

// TestEngine_NoAuditLog_NoPanic verifies backward compat — no AuditLog injected,
// no panic, no audit events.
func TestEngine_NoAuditLog_NoPanic(t *testing.T) {
	sender := &mockSender{responses: map[string]string{
		"cli": "ok",
	}}

	eng := New(sender, nil, nil, nil) // no WithAuditLog

	steps := []WorkflowStep{
		{
			Name:   "step1",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "cli"},
		},
	}

	result, err := eng.Execute(context.Background(), steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s", result.Status)
	}
}

// TestEngine_AdvisoryGate_Continues verifies that a gate with mode=advisory
// logs the failure but continues execution to the next step.
func TestEngine_AdvisoryGate_Continues(t *testing.T) {
	al := &collectingAuditLog{}

	sender := &mockSender{responses: map[string]string{
		"a": "CRITICAL issue found",
		"b": "final step output",
	}}

	eng := New(sender, nil, nil, nil, WithAuditLog(al))

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{
		TenantID: "tenant-xyz",
	})

	steps := []WorkflowStep{
		{
			Name:   "scan",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "a"},
		},
		{
			Name:   "quality-gate",
			Action: ActionGate,
			Config: map[string]any{
				"require": "no_critical_issues",
				"mode":    "advisory",
			},
		},
		{
			Name:   "finalize",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "b"},
		},
	}

	result, err := eng.Execute(ctx, steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Advisory gate should NOT stop execution — workflow should complete.
	if result.Status != "completed" {
		t.Fatalf("expected completed (advisory gate should not block), got %s", result.Status)
	}

	// Should have 3 step results.
	if len(result.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(result.Steps))
	}

	// Gate step should be advisory_gated.
	gateStep := result.Steps[1]
	if gateStep.Status != "advisory_gated" {
		t.Errorf("gate step status = %q, want advisory_gated", gateStep.Status)
	}

	// Finalize step should have completed.
	finalStep := result.Steps[2]
	if finalStep.Status != "completed" {
		t.Errorf("final step status = %q, want completed", finalStep.Status)
	}
	if finalStep.Content != "final step output" {
		t.Errorf("final step content = %q, want 'final step output'", finalStep.Content)
	}

	// Check audit: should have gated event.
	events := al.Events()
	var gatedEvents []audit.AuditEvent
	for _, ev := range events {
		if ev.EventType == audit.EventWorkflowGated {
			gatedEvents = append(gatedEvents, ev)
		}
	}
	if len(gatedEvents) != 1 {
		t.Fatalf("expected 1 gated audit event, got %d", len(gatedEvents))
	}
	if gatedEvents[0].ExtraFields["mode"] != "advisory" {
		t.Errorf("gated event mode = %q, want advisory", gatedEvents[0].ExtraFields["mode"])
	}
	if gatedEvents[0].TenantID != "tenant-xyz" {
		t.Errorf("gated event tenant = %q, want tenant-xyz", gatedEvents[0].TenantID)
	}
}

// TestEngine_BlockingGate_Default verifies that a gate without explicit mode
// defaults to blocking behavior (backward compat).
func TestEngine_BlockingGate_Default(t *testing.T) {
	al := &collectingAuditLog{}

	sender := &mockSender{responses: map[string]string{
		"a": "CRITICAL error",
		"b": "should not run",
	}}

	eng := New(sender, nil, nil, nil, WithAuditLog(al))

	steps := []WorkflowStep{
		{
			Name:   "scan",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "a"},
		},
		{
			Name:   "gate",
			Action: ActionGate,
			Config: map[string]any{
				"require": "no_critical_issues",
				// no "mode" — should default to blocking
			},
		},
		{
			Name:   "unreachable",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "b"},
		},
	}

	result, err := eng.Execute(context.Background(), steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Should be gated (blocking).
	if result.Status != "gated" {
		t.Fatalf("expected gated, got %s", result.Status)
	}

	// Should have 2 step results (scan + gate), not 3.
	if len(result.Steps) != 2 {
		t.Fatalf("expected 2 steps (scan+gate), got %d", len(result.Steps))
	}

	// Audit should have gated event with mode=blocking.
	events := al.Events()
	var gatedEvents []audit.AuditEvent
	for _, ev := range events {
		if ev.EventType == audit.EventWorkflowGated {
			gatedEvents = append(gatedEvents, ev)
		}
	}
	if len(gatedEvents) != 1 {
		t.Fatalf("expected 1 gated audit event, got %d", len(gatedEvents))
	}
	if gatedEvents[0].ExtraFields["mode"] != "blocking" {
		t.Errorf("gated event mode = %q, want blocking", gatedEvents[0].ExtraFields["mode"])
	}
}

// TestEngine_AllStepsCompleted_Gate verifies the new all_steps_completed gate
// condition (C1 gate enrichment).
func TestEngine_AllStepsCompleted_Gate(t *testing.T) {
	sender := &mockSender{responses: map[string]string{
		"a": "done",
		"b": "also done",
	}}

	eng := New(sender, nil, nil, nil)

	steps := []WorkflowStep{
		{
			Name:   "s1",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "a"},
		},
		{
			Name:   "s2",
			Action: ActionSingleExec,
			Config: map[string]any{"role": "b"},
		},
		{
			Name:   "completion-gate",
			Action: ActionGate,
			Config: map[string]any{
				"require": "all_steps_completed",
			},
		},
	}

	result, err := eng.Execute(context.Background(), steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed (all steps succeeded), got %s", result.Status)
	}
}

// TestEngine_AuditTimestamp verifies that audit events have non-zero timestamps.
func TestEngine_AuditTimestamp(t *testing.T) {
	al := &collectingAuditLog{}
	sender := &mockSender{responses: map[string]string{"x": "ok"}}
	eng := New(sender, nil, nil, nil, WithAuditLog(al))

	steps := []WorkflowStep{
		{Name: "ts-test", Action: ActionSingleExec, Config: map[string]any{"role": "x"}},
	}

	before := time.Now()
	_, err := eng.Execute(context.Background(), steps, WorkflowInput{Topic: "test"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for i, ev := range al.Events() {
		if ev.Timestamp.Before(before) {
			t.Errorf("event[%d] timestamp %v is before test start %v", i, ev.Timestamp, before)
		}
	}
}
