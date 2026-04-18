package budget

import (
	"reflect"
	"testing"

	"github.com/thebtf/aimux/pkg/guidance"
)

func TestAttachTruncation_InitializesNilResult(t *testing.T) {
	env := &guidance.ResponseEnvelope{Result: nil}
	meta := TruncationMeta{
		Truncated:     true,
		Hint:          "use include_content",
		ContentLength: 24,
	}

	AttachTruncation(env, meta)
	if env.Result == nil {
		t.Fatal("Result should be initialized")
	}

	got, ok := env.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result = %T, want map[string]any", env.Result)
	}
	if got["truncated"] != true {
		t.Fatalf("truncated = %v, want true", got["truncated"])
	}
	if got["hint"] != "use include_content" {
		t.Fatalf("hint = %v, want use include_content", got["hint"])
	}
	if got["content_length"] != 24 {
		t.Fatalf("content_length = %v, want 24", got["content_length"])
	}
}

func TestAttachTruncation_MergesMapResult(t *testing.T) {
	env := &guidance.ResponseEnvelope{
		Result: map[string]any{
			"status": "running",
		},
	}
	meta := TruncationMeta{
		Truncated:     true,
		Hint:          "add include_content",
		ContentLength: 10,
	}

	AttachTruncation(env, meta)
	result, ok := env.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result = %T, want map[string]any", env.Result)
	}
	if result["status"] != "running" {
		t.Fatalf("status = %v, want running", result["status"])
	}
	if result["truncated"] != true {
		t.Fatalf("truncated = %v, want true", result["truncated"])
	}
	if result["hint"] != "add include_content" {
		t.Fatalf("hint = %v, want add include_content", result["hint"])
	}
}

func TestAttachTruncation_NonMapResultUnchanged(t *testing.T) {
	original := "payload"
	env := &guidance.ResponseEnvelope{Result: original}
	meta := TruncationMeta{Truncated: true, Hint: "x", ContentLength: 3}

	AttachTruncation(env, meta)
	if env.Result != original {
		t.Fatalf("Result mutated: %#v", env.Result)
	}
}

func TestAttachTruncation_NoUpdateWhenNotTruncated(t *testing.T) {
	env := &guidance.ResponseEnvelope{
		Result: map[string]any{"status": "running"},
	}
	meta := TruncationMeta{Truncated: false}
	AttachTruncation(env, meta)
	if _, ok := env.Result.(map[string]any); !ok {
		t.Fatal("result should stay map")
	}
	if !reflect.DeepEqual(env.Result, map[string]any{"status": "running"}) {
		t.Fatalf("Result changed unexpectedly: %#v", env.Result)
	}
}

func TestAttachTruncation_NilEnvelope(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AttachTruncation panicked: %v", r)
		}
	}()
	AttachTruncation(nil, TruncationMeta{Truncated: true})
}
