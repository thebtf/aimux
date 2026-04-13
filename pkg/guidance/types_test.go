package guidance_test

import (
	"reflect"
	"testing"

	"github.com/thebtf/aimux/pkg/guidance"
)

func TestUnwrapResult_UnwrapsNestedResult(t *testing.T) {
	nested := map[string]any{"session_id": "abc", "status": "running"}
	response := map[string]any{
		"state":  "in_progress",
		"result": nested,
	}

	got := guidance.UnwrapResult(response)
	if !reflect.DeepEqual(got, nested) {
		t.Fatalf("UnwrapResult(response) = %#v, want %#v", got, nested)
	}
}

func TestUnwrapResult_ReturnsOriginalWhenNoNestedResult(t *testing.T) {
	response := map[string]any{"state": "done", "value": 42}

	got := guidance.UnwrapResult(response)
	if !reflect.DeepEqual(got, response) {
		t.Fatalf("UnwrapResult(response) = %#v, want original %#v", got, response)
	}
}

func TestUnwrapResult_WithTypedSingleResultMap(t *testing.T) {
	response := map[string]string{"result": "wrapped"}

	got := guidance.UnwrapResult(response)
	if got != "wrapped" {
		t.Fatalf("UnwrapResult(typed map) = %#v, want %q", got, "wrapped")
	}
}

func TestUnwrapResult_DoesNotUnwrapArbitraryMapWithSiblingFields(t *testing.T) {
	response := map[string]any{
		"result": map[string]any{"session_id": "abc"},
		"kind":   "domain-payload",
	}

	got := guidance.UnwrapResult(response)
	if !reflect.DeepEqual(got, response) {
		t.Fatalf("UnwrapResult(response) = %#v, want original %#v", got, response)
	}
}

func TestNewMissingPolicyEnvelope_UnwrapsWhenNestedResultExists(t *testing.T) {
	raw := map[string]any{
		"result": map[string]any{"session_id": "abc"},
		"state":  "ignored",
	}

	env := guidance.NewMissingPolicyEnvelope(raw)
	if env.State != guidance.StateGuidanceNotImplemented {
		t.Fatalf("env.State = %q, want %q", env.State, guidance.StateGuidanceNotImplemented)
	}

	resultMap, ok := env.Result.(map[string]any)
	if !ok {
		t.Fatalf("env.Result type = %T, want map[string]any", env.Result)
	}
	if resultMap["session_id"] != "abc" {
		t.Fatalf("env.Result.session_id = %v, want abc", resultMap["session_id"])
	}
}

func TestNewMissingPolicyEnvelope_KeepsOriginalWhenNoNestedResult(t *testing.T) {
	raw := map[string]any{"session_id": "abc"}
	env := guidance.NewMissingPolicyEnvelope(raw)

	if !reflect.DeepEqual(env.Result, raw) {
		t.Fatalf("env.Result = %#v, want original %#v", env.Result, raw)
	}
}

// T031 — UnwrapResult coverage: typed guidance structs and payload preservation.

// TestUnwrapResult_TypedResponseEnvelope verifies the typed ResponseEnvelope path.
// The ResponseEnvelope case is distinct from map[string]any — it must be handled
// via the concrete type switch, not the generic map detection logic.
func TestUnwrapResult_TypedResponseEnvelope(t *testing.T) {
	nested := map[string]any{"session_id": "sess_1", "topic": "crash at startup"}
	envelope := guidance.ResponseEnvelope{
		GuidanceFields: guidance.GuidanceFields{
			State:      "gathering",
			StopConditions: "all coverage areas green",
		},
		Result: nested,
	}

	got := guidance.UnwrapResult(envelope)
	if !reflect.DeepEqual(got, nested) {
		t.Fatalf("UnwrapResult(ResponseEnvelope) = %#v, want %#v", got, nested)
	}
}

// TestUnwrapResult_TypedResponseEnvelopePointer verifies the *ResponseEnvelope path.
func TestUnwrapResult_TypedResponseEnvelopePointer(t *testing.T) {
	nested := map[string]any{"job_id": "job_42"}
	envelope := &guidance.ResponseEnvelope{
		GuidanceFields: guidance.GuidanceFields{State: "complete"},
		Result:         nested,
	}

	got := guidance.UnwrapResult(envelope)
	if !reflect.DeepEqual(got, nested) {
		t.Fatalf("UnwrapResult(*ResponseEnvelope) = %#v, want %#v", got, nested)
	}
}

// TestUnwrapResult_NilResponseEnvelopePointer verifies nil *ResponseEnvelope returns nil.
func TestUnwrapResult_NilResponseEnvelopePointer(t *testing.T) {
	var envelope *guidance.ResponseEnvelope

	got := guidance.UnwrapResult(envelope)
	if got != nil {
		t.Fatalf("UnwrapResult(nil *ResponseEnvelope) = %v, want nil", got)
	}
}

// TestUnwrapResult_TypedHandlerResult verifies the HandlerResult type switch path.
func TestUnwrapResult_TypedHandlerResult(t *testing.T) {
	nested := map[string]any{"findings": []string{"memory leak"}, "coverage": "full"}
	hr := guidance.HandlerResult{
		Tool:   "investigate",
		Action: "report",
		State:  "complete",
		Result: nested,
	}

	got := guidance.UnwrapResult(hr)
	if !reflect.DeepEqual(got, nested) {
		t.Fatalf("UnwrapResult(HandlerResult) = %#v, want %#v", got, nested)
	}
}

// TestUnwrapResult_UnguidedFlatResponse verifies that a flat domain object with no
// guidance envelope structure is returned unchanged.
// This guards against accidentally stripping sibling domain fields from real payloads.
func TestUnwrapResult_UnguidedFlatResponse(t *testing.T) {
	response := map[string]any{
		"status":     "completed",
		"content":    "exec output here",
		"session_id": "sess_99",
		"exit_code":  float64(0),
	}

	got := guidance.UnwrapResult(response)
	if !reflect.DeepEqual(got, response) {
		t.Fatalf("UnwrapResult(flat exec response) = %#v, want original unchanged %#v", got, response)
	}
}

// TestUnwrapResult_NestedPayloadIntact verifies that the full nested payload is
// preserved without field loss when unwrapping a guidance envelope.
// Regression guard: early implementations lost sibling fields inside the result object.
func TestUnwrapResult_NestedPayloadIntact(t *testing.T) {
	nested := map[string]any{
		"session_id":     "sess_100",
		"topic":          "db connection leak",
		"coverage_areas": []string{"reproduction", "isolation"},
		"findings_count": float64(3),
		"severity":       "P0",
	}
	envelope := guidance.ResponseEnvelope{
		GuidanceFields: guidance.GuidanceFields{
			State:      "gathering",
			YouAreHere: "Phase 2 — isolation",
		},
		Result: nested,
	}

	got := guidance.UnwrapResult(envelope)
	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("UnwrapResult payload type = %T, want map[string]any", got)
	}
	for key, want := range nested {
		if !reflect.DeepEqual(gotMap[key], want) {
			t.Errorf("nested field %q = %#v, want %#v", key, gotMap[key], want)
		}
	}
	if len(gotMap) != len(nested) {
		t.Errorf("nested payload has %d fields, want %d — field was added or dropped", len(gotMap), len(nested))
	}
}
