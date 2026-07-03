package api

import (
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Option functions
// ---------------------------------------------------------------------------

func TestWithTimeout_Positive(t *testing.T) {
	b := &baseExecutor{timeout: DefaultTimeout}
	WithTimeout(30 * time.Second)(b)
	if b.timeout != 30*time.Second {
		t.Errorf("got %v, want 30s", b.timeout)
	}
}

func TestWithTimeout_Zero(t *testing.T) {
	b := &baseExecutor{timeout: DefaultTimeout}
	WithTimeout(0)(b)
	if b.timeout != DefaultTimeout {
		t.Errorf("zero duration should not override default, got %v", b.timeout)
	}
}

func TestWithBaseURL(t *testing.T) {
	b := &baseExecutor{}
	WithBaseURL("https://proxy.example.com/v1")(b)
	if b.baseURL != "https://proxy.example.com/v1" {
		t.Errorf("got %q", b.baseURL)
	}
}

func TestWithMaxRetries(t *testing.T) {
	b := &baseExecutor{}
	WithMaxRetries(3)(b)
	if b.maxRetries != 3 {
		t.Errorf("got %d, want 3", b.maxRetries)
	}
}

func TestWithMaxRetries_Negative(t *testing.T) {
	b := &baseExecutor{maxRetries: 2}
	WithMaxRetries(-1)(b)
	if b.maxRetries != 2 {
		t.Errorf("negative should not override, got %d", b.maxRetries)
	}
}

// ---------------------------------------------------------------------------
// NewFromConfig
// ---------------------------------------------------------------------------

func TestNewFromConfig_OpenAI(t *testing.T) {
	cfg := Config{
		Provider:   "openai",
		APIKey:     "test-key",
		Model:      "gpt-4o-mini",
		Timeout:    30 * time.Second,
		MaxRetries: 2,
	}
	exec, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info := exec.Info()
	if info.Name != "openai" {
		t.Errorf("name: got %q, want %q", info.Name, "openai")
	}
}

func TestNewFromConfig_Anthropic(t *testing.T) {
	cfg := Config{
		Provider: "anthropic",
		APIKey:   "test-key",
	}
	exec, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.Info().Name != "anthropic" {
		t.Errorf("name: got %q", exec.Info().Name)
	}
}

func TestNewFromConfig_UnknownProvider(t *testing.T) {
	cfg := Config{
		Provider: "mistral",
		APIKey:   "test-key",
	}
	_, err := NewFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !errors.Is(err, err) { // err is non-nil
		t.Logf("error message: %v", err)
	}
}

func TestNewFromConfig_EmptyAPIKey(t *testing.T) {
	cfg := Config{
		Provider: "openai",
		APIKey:   "",
	}
	_, err := NewFromConfig(cfg)
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
}

func TestNewFromConfig_WithCooldown(t *testing.T) {
	tracker := &mockCooldownTracker{available: true}
	cfg := Config{
		Provider: "openai",
		APIKey:   "test-key",
		Model:    "gpt-4o",
		Cooldown: tracker,
	}
	_, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The cooldown is wired internally; no public accessor to check,
	// but verifying construction succeeds without panic is the goal.
}
