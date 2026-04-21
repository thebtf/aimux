package think

import (
	"context"
	"testing"
)

func TestSamplingMessage(t *testing.T) {
	msg := SamplingMessage{
		Role:    "user",
		Content: "hello",
	}
	if msg.Role != "user" {
		t.Errorf("expected Role %q, got %q", "user", msg.Role)
	}
	if msg.Content != "hello" {
		t.Errorf("expected Content %q, got %q", "hello", msg.Content)
	}

	assistant := SamplingMessage{Role: "assistant", Content: "world"}
	if assistant.Role != "assistant" {
		t.Errorf("expected Role %q, got %q", "assistant", assistant.Role)
	}
}

// mockSamplingHandler is a compile-time proof that a type can satisfy both
// PatternHandler and SamplingAwareHandler simultaneously.
type mockSamplingHandler struct {
	provider SamplingProvider
}

func (m *mockSamplingHandler) Name() string        { return "mock" }
func (m *mockSamplingHandler) Description() string { return "mock sampling handler" }
func (m *mockSamplingHandler) Validate(input map[string]any) (map[string]any, error) {
	return input, nil
}
func (m *mockSamplingHandler) Handle(validInput map[string]any, sessionID string) (*ThinkResult, error) {
	return MakeThinkResult("mock", map[string]any{}, sessionID, nil, "", nil), nil
}
func (m *mockSamplingHandler) SetSampling(provider SamplingProvider) {
	m.provider = provider
}
func (m *mockSamplingHandler) SchemaFields() map[string]FieldSchema { return nil }
func (m *mockSamplingHandler) Category() string                     { return "solo" }

// mockProvider is a minimal SamplingProvider for test wiring.
type mockProvider struct{}

func (p *mockProvider) RequestSampling(_ context.Context, _ []SamplingMessage, _ int) (string, error) {
	return "mocked response", nil
}

func TestSamplingAwareHandler(t *testing.T) {
	// Verify interface satisfaction at compile time via explicit assignments.
	var _ PatternHandler = (*mockSamplingHandler)(nil)
	var _ SamplingAwareHandler = (*mockSamplingHandler)(nil)

	h := &mockSamplingHandler{}
	p := &mockProvider{}
	h.SetSampling(p)

	if h.provider == nil {
		t.Error("expected provider to be set after SetSampling")
	}

	result, err := h.Handle(map[string]any{}, "sess-1")
	if err != nil {
		t.Fatalf("Handle returned unexpected error: %v", err)
	}
	if result.Pattern != "mock" {
		t.Errorf("expected pattern %q, got %q", "mock", result.Pattern)
	}
}
