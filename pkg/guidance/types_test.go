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
