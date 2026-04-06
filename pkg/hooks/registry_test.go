package hooks

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func baseCtx() BeforeHookContext {
	return BeforeHookContext{
		CLI:           "testcli",
		PromptPreview: "hello world",
		CWD:           "/tmp",
		Model:         "gpt-4",
		Role:          "coder",
		SessionID:     "sess-1",
		Metadata:      map[string]string{"key": "val"},
	}
}

func TestRunBefore_Proceed(t *testing.T) {
	r := NewRegistry()
	r.RegisterBefore("pass", func(ctx BeforeHookContext) BeforeHookResult {
		return BeforeHookResult{Action: "proceed"}
	})
	result := r.RunBefore(baseCtx())
	if result.Action != "proceed" {
		t.Fatalf("expected proceed, got %s", result.Action)
	}
}

func TestRunBefore_Block_ShortCircuits(t *testing.T) {
	r := NewRegistry()
	called := false
	r.RegisterBefore("blocker", func(ctx BeforeHookContext) BeforeHookResult {
		return BeforeHookResult{Action: "block", Reason: "denied"}
	})
	r.RegisterBefore("after-block", func(ctx BeforeHookContext) BeforeHookResult {
		called = true
		return BeforeHookResult{Action: "proceed"}
	})
	result := r.RunBefore(baseCtx())
	if result.Action != "block" {
		t.Fatalf("expected block, got %s", result.Action)
	}
	if result.Reason != "denied" {
		t.Fatalf("expected reason 'denied', got %s", result.Reason)
	}
	if called {
		t.Fatal("hook after block should not have been called")
	}
}

func TestRunBefore_Skip_WithSyntheticContent(t *testing.T) {
	r := NewRegistry()
	r.RegisterBefore("skipper", func(ctx BeforeHookContext) BeforeHookResult {
		return BeforeHookResult{Action: "skip", SyntheticContent: "cached response"}
	})
	result := r.RunBefore(baseCtx())
	if result.Action != "skip" {
		t.Fatalf("expected skip, got %s", result.Action)
	}
	if result.SyntheticContent != "cached response" {
		t.Fatalf("expected synthetic content, got %s", result.SyntheticContent)
	}
}

func TestRunBefore_AccumulatesModifiedPromptAndMetadata(t *testing.T) {
	r := NewRegistry()
	r.RegisterBefore("first", func(ctx BeforeHookContext) BeforeHookResult {
		return BeforeHookResult{
			Action:         "proceed",
			ModifiedPrompt: "first prompt",
			Metadata:       map[string]string{"a": "1"},
		}
	})
	r.RegisterBefore("second", func(ctx BeforeHookContext) BeforeHookResult {
		return BeforeHookResult{
			Action:         "proceed",
			ModifiedPrompt: "second prompt",
			Metadata:       map[string]string{"b": "2"},
		}
	})
	result := r.RunBefore(baseCtx())
	if result.ModifiedPrompt != "second prompt" {
		t.Fatalf("expected last prompt wins, got %s", result.ModifiedPrompt)
	}
	if result.Metadata["a"] != "1" || result.Metadata["b"] != "2" {
		t.Fatalf("expected merged metadata, got %v", result.Metadata)
	}
}

func TestRunAfter_Accept(t *testing.T) {
	r := NewRegistry()
	r.RegisterAfter("ok", func(ctx AfterHookContext) AfterHookResult {
		return AfterHookResult{Action: "accept"}
	})
	result := r.RunAfter(AfterHookContext{BeforeHookContext: baseCtx(), Content: "output", ExitCode: 0, DurationMs: 100})
	if result.Action != "accept" {
		t.Fatalf("expected accept, got %s", result.Action)
	}
	if result.Annotations != nil && len(result.Annotations) > 0 {
		t.Fatalf("expected no annotations, got %v", result.Annotations)
	}
}

func TestRunAfter_Reject_ShortCircuits(t *testing.T) {
	r := NewRegistry()
	called := false
	r.RegisterAfter("rejector", func(ctx AfterHookContext) AfterHookResult {
		return AfterHookResult{Action: "reject", Reason: "bad output"}
	})
	r.RegisterAfter("after-reject", func(ctx AfterHookContext) AfterHookResult {
		called = true
		return AfterHookResult{Action: "accept"}
	})
	result := r.RunAfter(AfterHookContext{BeforeHookContext: baseCtx(), Content: "x"})
	if result.Action != "reject" {
		t.Fatalf("expected reject, got %s", result.Action)
	}
	if called {
		t.Fatal("hook after reject should not have been called")
	}
}

func TestRunAfter_Annotate_Accumulates(t *testing.T) {
	r := NewRegistry()
	r.RegisterAfter("ann1", func(ctx AfterHookContext) AfterHookResult {
		return AfterHookResult{Action: "annotate", Annotations: map[string]string{"quality": "high"}}
	})
	r.RegisterAfter("ann2", func(ctx AfterHookContext) AfterHookResult {
		return AfterHookResult{Action: "annotate", Annotations: map[string]string{"lang": "en"}}
	})
	result := r.RunAfter(AfterHookContext{BeforeHookContext: baseCtx(), Content: "x"})
	if result.Action != "accept" {
		t.Fatalf("expected accept (default), got %s", result.Action)
	}
	if result.Annotations["quality"] != "high" || result.Annotations["lang"] != "en" {
		t.Fatalf("expected accumulated annotations, got %v", result.Annotations)
	}
}

func TestRunBefore_Timeout(t *testing.T) {
	r := NewRegistry()
	secondCalled := false
	r.RegisterBefore("slow", func(ctx BeforeHookContext) BeforeHookResult {
		time.Sleep(10 * time.Second)
		return BeforeHookResult{Action: "block"}
	}, 100*time.Millisecond)
	r.RegisterBefore("fast", func(ctx BeforeHookContext) BeforeHookResult {
		secondCalled = true
		return BeforeHookResult{Action: "proceed"}
	})
	result := r.RunBefore(baseCtx())
	if result.Action != "proceed" {
		t.Fatalf("expected proceed after timeout skip, got %s", result.Action)
	}
	if !secondCalled {
		t.Fatal("second hook should have been called after first timed out")
	}
}

func TestRunBefore_PanicRecovery(t *testing.T) {
	r := NewRegistry()
	secondCalled := false
	r.RegisterBefore("panicker", func(ctx BeforeHookContext) BeforeHookResult {
		panic("oh no")
	})
	r.RegisterBefore("survivor", func(ctx BeforeHookContext) BeforeHookResult {
		secondCalled = true
		return BeforeHookResult{Action: "proceed"}
	})
	result := r.RunBefore(baseCtx())
	if result.Action != "proceed" {
		t.Fatalf("expected proceed after panic skip, got %s", result.Action)
	}
	if !secondCalled {
		t.Fatal("second hook should have been called after first panicked")
	}
}

func TestRemove(t *testing.T) {
	r := NewRegistry()
	called := false
	r.RegisterBefore("removeme", func(ctx BeforeHookContext) BeforeHookResult {
		called = true
		return BeforeHookResult{Action: "proceed"}
	})
	r.Remove("removeme")
	r.RunBefore(baseCtx())
	if called {
		t.Fatal("removed hook should not have been called")
	}
}

func TestTelemetryHook(t *testing.T) {
	// Capture stderr
	old := os.Stderr
	rr, w, _ := os.Pipe()
	os.Stderr = w

	hook := NewTelemetryHook()
	result := hook(AfterHookContext{
		BeforeHookContext: BeforeHookContext{CLI: "testcli"},
		Content:           "some output",
		ExitCode:          1,
		DurationMs:        42,
	})

	w.Close()
	os.Stderr = old

	var buf bytes.Buffer
	buf.ReadFrom(rr)
	output := buf.String()

	if result.Action != "accept" {
		t.Fatalf("expected accept, got %s", result.Action)
	}
	expected := fmt.Sprintf("[aimux:telemetry] cli=testcli exit=1 duration=42ms anomaly=non_zero_exit")
	if !strings.Contains(output, expected) {
		t.Fatalf("expected stderr to contain %q, got %q", expected, output)
	}
}
