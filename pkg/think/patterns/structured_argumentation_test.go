package patterns

import (
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
