package patterns

import (
	"strings"
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// TestArg_FlatArgument: argument_type + argument_text + supports_claim_id → argument tracked in session.
func TestArg_FlatArgument(t *testing.T) {
	think.ClearSessions()
	p := NewStructuredArgumentationPattern()
	sid := "arg-flat-1"

	// First add a claim so we can reference it via supports_claim_id.
	claimInp, err := p.Validate(map[string]any{
		"topic":         "renewable energy policy",
		"argument_type": "claim",
		"argument_text": "solar subsidies reduce emissions",
	})
	if err != nil {
		t.Fatalf("validate claim: %v", err)
	}
	claimResult, err := p.Handle(claimInp, sid)
	if err != nil {
		t.Fatalf("handle claim: %v", err)
	}

	// Extract the auto-generated claim ID (A-1).
	args, _ := claimResult.Data["arguments"].([]any)
	if len(args) != 1 {
		t.Fatalf("expected 1 argument after claim, got %d", len(args))
	}
	claimID, _ := args[0].(map[string]any)["id"].(string)

	// Now submit evidence via flat params referencing the claim.
	evInp, err := p.Validate(map[string]any{
		"topic":             "renewable energy policy",
		"argument_type":     "evidence",
		"argument_text":     "IEA 2023 data shows 30% emissions cut in countries with subsidies",
		"supports_claim_id": claimID,
	})
	if err != nil {
		t.Fatalf("validate evidence: %v", err)
	}

	arg, ok := evInp["argument"].(map[string]any)
	if !ok {
		t.Fatal("expected argument map in validated input")
	}
	if arg["type"] != "evidence" {
		t.Fatalf("expected type=evidence, got %v", arg["type"])
	}
	if arg["supportsClaimId"] != claimID {
		t.Fatalf("expected supportsClaimId=%s, got %v", claimID, arg["supportsClaimId"])
	}

	evResult, err := p.Handle(evInp, sid)
	if err != nil {
		t.Fatalf("handle evidence: %v", err)
	}
	if evResult.Data["evidenceCount"] != 1 {
		t.Fatalf("expected evidenceCount=1, got %v", evResult.Data["evidenceCount"])
	}
	if evResult.Data["claimCount"] != 1 {
		t.Fatalf("expected claimCount=1, got %v", evResult.Data["claimCount"])
	}
}

// TestArg_FlatBackwardCompat: old nested argument map still works.
func TestArg_FlatBackwardCompat(t *testing.T) {
	think.ClearSessions()
	p := NewStructuredArgumentationPattern()
	sid := "arg-compat-1"

	inp, err := p.Validate(map[string]any{
		"topic": "open source licensing",
		"argument": map[string]any{
			"type": "claim",
			"text": "GPL ensures software freedom",
		},
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	arg, ok := inp["argument"].(map[string]any)
	if !ok {
		t.Fatal("expected argument map in validated input")
	}
	if arg["type"] != "claim" {
		t.Fatalf("expected type=claim, got %v", arg["type"])
	}
	if arg["text"] != "GPL ensures software freedom" {
		t.Fatalf("expected text='GPL ensures software freedom', got %v", arg["text"])
	}

	r, err := p.Handle(inp, sid)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.Data["claimCount"] != 1 {
		t.Fatalf("expected claimCount=1, got %v", r.Data["claimCount"])
	}
}

func TestStructuredArgumentation_SessionIDInData(t *testing.T) {
	think.ClearSessions()
	p := NewStructuredArgumentationPattern()
	input := map[string]any{
		"topic":         "test topic",
		"argument_type": "claim",
		"argument_text": "Test claim text",
	}
	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	result, err := p.Handle(validated, "test-session-123")
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	sid, ok := result.Data["session_id"]
	if !ok {
		t.Fatal("session_id not found in result data")
	}
	if sid != "test-session-123" {
		t.Errorf("session_id = %v, want test-session-123", sid)
	}
}

// TestStructuredArgumentation_MultiTurnAccumulation verifies that using session_id from
// the first result in a subsequent call correctly accumulates arguments (T002 AC).
func TestStructuredArgumentation_MultiTurnAccumulation(t *testing.T) {
	think.ClearSessions()
	p := NewStructuredArgumentationPattern()
	sid := "multiturn-session-t002"

	// Turn 1: add a claim, capture session_id.
	inp1, err := p.Validate(map[string]any{
		"topic":         "AI governance",
		"argument_type": "claim",
		"argument_text": "AI systems require mandatory audits",
	})
	if err != nil {
		t.Fatalf("validate turn 1: %v", err)
	}
	r1, err := p.Handle(inp1, sid)
	if err != nil {
		t.Fatalf("handle turn 1: %v", err)
	}
	returnedSID, ok := r1.Data["session_id"].(string)
	if !ok || returnedSID == "" {
		t.Fatal("session_id not returned in turn 1 result")
	}
	if returnedSID != sid {
		t.Errorf("session_id = %q, want %q", returnedSID, sid)
	}

	// Turn 2: use returned session_id, add evidence.
	claimID := r1.Data["arguments"].([]any)[0].(map[string]any)["id"].(string)
	inp2, err := p.Validate(map[string]any{
		"topic":             "AI governance",
		"argument_type":     "evidence",
		"argument_text":     "EU AI Act requires conformity assessments",
		"supports_claim_id": claimID,
	})
	if err != nil {
		t.Fatalf("validate turn 2: %v", err)
	}
	r2, err := p.Handle(inp2, returnedSID)
	if err != nil {
		t.Fatalf("handle turn 2: %v", err)
	}
	if r2.Data["claimCount"] != 1 {
		t.Errorf("expected claimCount=1 in turn 2, got %v", r2.Data["claimCount"])
	}
	if r2.Data["evidenceCount"] != 1 {
		t.Errorf("expected evidenceCount=1 in turn 2, got %v", r2.Data["evidenceCount"])
	}
	args2, _ := r2.Data["arguments"].([]any)
	if len(args2) != 2 {
		t.Errorf("expected 2 accumulated arguments, got %d", len(args2))
	}
}

func TestStructuredArgumentation_SingleClaimWarning_R5_3(t *testing.T) {
	think.ClearSessions()
	p := NewStructuredArgumentationPattern()
	in := map[string]any{
		"topic":         "Single claim test R5-3",
		"argument_type": "claim",
		"argument_text": "X is true",
	}
	valid, err := p.Validate(in)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	res, err := p.Handle(valid, "fresh-session-r5-3-single")
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	// Warning is set on ThinkResult.Summary (not res.Data["summary"]).
	summary := res.Summary
	if !strings.Contains(summary, "warning: single unsupported claim") {
		t.Errorf("R5-3: expected single-claim warning in summary, got: %q", summary)
	}
}
