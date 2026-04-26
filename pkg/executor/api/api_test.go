package api

import (
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks (redundant with the per-file var _ checks,
// kept here for documentation clarity).
// ---------------------------------------------------------------------------

var (
	_ types.ExecutorV2 = (*OpenAIExecutor)(nil)
	_ types.ExecutorV2 = (*AnthropicExecutor)(nil)
	_ types.ExecutorV2 = (*GoogleAIExecutor)(nil)
)

// ---------------------------------------------------------------------------
// Info() tests
// ---------------------------------------------------------------------------

func TestOpenAIExecutorInfo(t *testing.T) {
	e := &OpenAIExecutor{base: mustBase(t, "key", "gpt-4o")}
	info := e.Info()

	if info.Name != "openai" {
		t.Errorf("Name: got %q, want %q", info.Name, "openai")
	}
	if info.Type != types.ExecutorTypeAPI {
		t.Errorf("Type: got %v, want ExecutorTypeAPI", info.Type)
	}
	if !info.Capabilities.Streaming {
		t.Error("Capabilities.Streaming should be true")
	}
	if !info.Capabilities.Tools {
		t.Error("Capabilities.Tools should be true")
	}
}

func TestAnthropicExecutorInfo(t *testing.T) {
	e := &AnthropicExecutor{base: mustBase(t, "key", "claude-3-5-sonnet")}
	info := e.Info()

	if info.Name != "anthropic" {
		t.Errorf("Name: got %q, want %q", info.Name, "anthropic")
	}
	if info.Type != types.ExecutorTypeAPI {
		t.Errorf("Type: got %v, want ExecutorTypeAPI", info.Type)
	}
	if !info.Capabilities.Streaming {
		t.Error("Capabilities.Streaming should be true")
	}
	if !info.Capabilities.Tools {
		t.Error("Capabilities.Tools should be true")
	}
}

func TestGoogleAIExecutorInfo(t *testing.T) {
	e := &GoogleAIExecutor{base: mustBase(t, "key", "gemini-2.0-flash")}
	info := e.Info()

	if info.Name != "google" {
		t.Errorf("Name: got %q, want %q", info.Name, "google")
	}
	if info.Type != types.ExecutorTypeAPI {
		t.Errorf("Type: got %v, want ExecutorTypeAPI", info.Type)
	}
	if !info.Capabilities.Streaming {
		t.Error("Capabilities.Streaming should be true")
	}
	if !info.Capabilities.Tools {
		t.Error("Capabilities.Tools should be true")
	}
	if !info.Capabilities.Images {
		t.Error("Capabilities.Images should be true")
	}
}

// ---------------------------------------------------------------------------
// Empty / nil API key error tests
// ---------------------------------------------------------------------------

func TestNewOpenAI_EmptyAPIKey(t *testing.T) {
	_, err := NewOpenAI("", "gpt-4o")
	if err == nil {
		t.Fatal("NewOpenAI with empty API key: expected error, got nil")
	}
}

func TestNewOpenAI_DefaultModel(t *testing.T) {
	e, err := NewOpenAI("test-key", "")
	if err != nil {
		t.Fatalf("NewOpenAI with empty model: unexpected error: %v", err)
	}
	if e.base.model != DefaultOpenAIModel {
		t.Errorf("model: got %q, want %q", e.base.model, DefaultOpenAIModel)
	}
}

func TestNewAnthropic_EmptyAPIKey(t *testing.T) {
	_, err := NewAnthropic("", "claude-3-5-sonnet")
	if err == nil {
		t.Fatal("NewAnthropic with empty API key: expected error, got nil")
	}
}

func TestNewAnthropic_DefaultModel(t *testing.T) {
	e, err := NewAnthropic("test-key", "")
	if err != nil {
		t.Fatalf("NewAnthropic with empty model: unexpected error: %v", err)
	}
	if e.base.model != DefaultAnthropicModel {
		t.Errorf("model: got %q, want %q", e.base.model, DefaultAnthropicModel)
	}
}

// NewGoogleAI requires a real network call to create the client, so the
// empty-key test is kept simple (just verify the error path in newBase).
func TestNewGoogleAI_EmptyAPIKey(t *testing.T) {
	_, err := NewGoogleAI("", "gemini-2.0-flash")
	if err == nil {
		t.Fatal("NewGoogleAI with empty API key: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// IsAlive / Close lifecycle tests
// ---------------------------------------------------------------------------

func TestOpenAIExecutor_IsAliveAndClose(t *testing.T) {
	e := &OpenAIExecutor{base: mustBase(t, "key", "gpt-4o")}

	if got := e.IsAlive(); got != types.HealthAlive {
		t.Errorf("IsAlive before Close: got %v, want HealthAlive", got)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: unexpected error: %v", err)
	}
	if got := e.IsAlive(); got != types.HealthDead {
		t.Errorf("IsAlive after Close: got %v, want HealthDead", got)
	}
}

func TestAnthropicExecutor_IsAliveAndClose(t *testing.T) {
	e := &AnthropicExecutor{base: mustBase(t, "key", "claude-3-5-sonnet")}

	if got := e.IsAlive(); got != types.HealthAlive {
		t.Errorf("IsAlive before Close: got %v, want HealthAlive", got)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: unexpected error: %v", err)
	}
	if got := e.IsAlive(); got != types.HealthDead {
		t.Errorf("IsAlive after Close: got %v, want HealthDead", got)
	}
}

func TestGoogleAIExecutor_IsAliveAndClose(t *testing.T) {
	e := &GoogleAIExecutor{base: mustBase(t, "key", "gemini-2.0-flash")}

	if got := e.IsAlive(); got != types.HealthAlive {
		t.Errorf("IsAlive before Close: got %v, want HealthAlive", got)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: unexpected error: %v", err)
	}
	if got := e.IsAlive(); got != types.HealthDead {
		t.Errorf("IsAlive after Close: got %v, want HealthDead", got)
	}
}

// ---------------------------------------------------------------------------
// Send/SendStream after Close (closed executor must return error)
// ---------------------------------------------------------------------------

func TestOpenAIExecutor_SendAfterClose(t *testing.T) {
	e := &OpenAIExecutor{base: mustBase(t, "key", "gpt-4o")}
	_ = e.Close()

	_, err := e.Send(t.Context(), types.Message{Content: "hello"})
	if err == nil {
		t.Fatal("Send after Close: expected error, got nil")
	}
}

func TestAnthropicExecutor_SendAfterClose(t *testing.T) {
	e := &AnthropicExecutor{base: mustBase(t, "key", "claude-3-5-sonnet")}
	_ = e.Close()

	_, err := e.Send(t.Context(), types.Message{Content: "hello"})
	if err == nil {
		t.Fatal("Send after Close: expected error, got nil")
	}
}

func TestGoogleAIExecutor_SendAfterClose(t *testing.T) {
	e := &GoogleAIExecutor{base: mustBase(t, "key", "gemini-2.0-flash")}
	_ = e.Close()

	_, err := e.Send(t.Context(), types.Message{Content: "hello"})
	if err == nil {
		t.Fatal("Send after Close: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// buildHistory (pure function, no network)
// ---------------------------------------------------------------------------

func TestBuildHistory_Empty(t *testing.T) {
	if result := buildHistory(nil); result != nil {
		t.Errorf("buildHistory(nil): got %v, want nil", result)
	}
}

func TestBuildHistory_Copy(t *testing.T) {
	original := []types.Turn{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	result := buildHistory(original)

	if len(result) != len(original) {
		t.Fatalf("len: got %d, want %d", len(result), len(original))
	}
	// Verify it's a defensive copy.
	result[0].Content = "mutated"
	if original[0].Content == "mutated" {
		t.Error("buildHistory returned a reference, not a copy")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mustBase constructs a baseExecutor directly for unit tests that need a live
// executor struct without making network calls.
func mustBase(t *testing.T, apiKey, model string) *baseExecutor {
	t.Helper()
	b, err := newBase(apiKey, model)
	if err != nil {
		t.Fatalf("newBase(%q, %q): %v", apiKey, model, err)
	}
	return b
}
