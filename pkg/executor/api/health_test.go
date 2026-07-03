package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// ---------------------------------------------------------------------------
// isAliveWithCooldown
// ---------------------------------------------------------------------------

func TestIsAliveWithCooldown_AliveNoCooldown(t *testing.T) {
	exec, err := NewOpenAI("test-key", "gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if got := exec.IsAlive(); got != types.HealthAlive {
		t.Errorf("got %v, want HealthAlive", got)
	}
}

func TestIsAliveWithCooldown_DegradedWhenRateLimited(t *testing.T) {
	tracker := &mockCooldownTracker{available: false}
	exec, err := NewOpenAI("test-key", "gpt-4o", WithCooldownTracker(tracker))
	if err != nil {
		t.Fatal(err)
	}
	if got := exec.IsAlive(); got != types.HealthDegraded {
		t.Errorf("got %v, want HealthDegraded", got)
	}
}

func TestIsAliveWithCooldown_DeadAfterClose(t *testing.T) {
	tracker := &mockCooldownTracker{available: false}
	exec, err := NewOpenAI("test-key", "gpt-4o", WithCooldownTracker(tracker))
	if err != nil {
		t.Fatal(err)
	}
	exec.Close()
	if got := exec.IsAlive(); got != types.HealthDead {
		t.Errorf("got %v, want HealthDead (close takes precedence over degraded)", got)
	}
}

// ---------------------------------------------------------------------------
// HealthDetail
// ---------------------------------------------------------------------------

func TestHealthDetail_Alive(t *testing.T) {
	exec, err := NewOpenAI("test-key", "gpt-4o-mini")
	if err != nil {
		t.Fatal(err)
	}
	hd := exec.HealthDetail()
	if hd.Status != types.HealthAlive {
		t.Errorf("status: got %v, want HealthAlive", hd.Status)
	}
	if hd.Model != "gpt-4o-mini" {
		t.Errorf("model: got %q", hd.Model)
	}
	if hd.Provider != "openai" {
		t.Errorf("provider: got %q", hd.Provider)
	}
	if hd.RateLimited {
		t.Error("expected RateLimited=false")
	}
}

func TestHealthDetail_Degraded(t *testing.T) {
	tracker := &mockCooldownTracker{available: false}
	exec, err := NewAnthropic("test-key", "claude-3-haiku", WithCooldownTracker(tracker))
	if err != nil {
		t.Fatal(err)
	}
	hd := exec.HealthDetail()
	if hd.Status != types.HealthDegraded {
		t.Errorf("status: got %v, want HealthDegraded", hd.Status)
	}
	if !hd.RateLimited {
		t.Error("expected RateLimited=true when model is on cooldown")
	}
	if hd.Provider != "anthropic" {
		t.Errorf("provider: got %q", hd.Provider)
	}
}

func TestHealthDetail_WithBaseURL(t *testing.T) {
	exec, err := NewOpenAI("test-key", "gpt-4o", WithBaseURL("https://proxy.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	hd := exec.HealthDetail()
	if hd.BaseURL != "https://proxy.example.com" {
		t.Errorf("baseURL: got %q", hd.BaseURL)
	}
}

func TestHealthDetail_MaxRetries(t *testing.T) {
	exec, err := NewOpenAI("test-key", "gpt-4o", WithMaxRetries(3))
	if err != nil {
		t.Fatal(err)
	}
	hd := exec.HealthDetail()
	if hd.MaxRetries != 3 {
		t.Errorf("maxRetries: got %d", hd.MaxRetries)
	}
}

// ---------------------------------------------------------------------------
// HealthChecker interface
// ---------------------------------------------------------------------------

func TestHealthCheckerInterface_OpenAI(t *testing.T) {
	exec, _ := NewOpenAI("test-key", "gpt-4o")
	var hc HealthChecker = exec // compile-time check
	_ = hc.HealthDetail()
}

func TestHealthCheckerInterface_Anthropic(t *testing.T) {
	exec, _ := NewAnthropic("test-key", "")
	var hc HealthChecker = exec
	_ = hc.HealthDetail()
}

// ---------------------------------------------------------------------------
// WithAPIKeyInjector
// ---------------------------------------------------------------------------

func TestEffectiveAPIKey_NoInjector(t *testing.T) {
	b := &baseExecutor{apiKey: "original-key"}
	if got := b.effectiveAPIKey(); got != "original-key" {
		t.Errorf("got %q, want %q", got, "original-key")
	}
}

func TestEffectiveAPIKey_WithInjector(t *testing.T) {
	b := &baseExecutor{apiKey: "original-key"}
	WithAPIKeyInjector(func() string { return "injected-key" })(b)
	if got := b.effectiveAPIKey(); got != "injected-key" {
		t.Errorf("got %q, want %q", got, "injected-key")
	}
}

func TestEffectiveAPIKey_InjectorReturnsEmpty(t *testing.T) {
	b := &baseExecutor{apiKey: "original-key"}
	WithAPIKeyInjector(func() string { return "" })(b)
	if got := b.effectiveAPIKey(); got != "original-key" {
		t.Errorf("got %q, want fallback %q", got, "original-key")
	}
}

// ---------------------------------------------------------------------------
// lastError
// ---------------------------------------------------------------------------

func TestLastError_SetAndGet(t *testing.T) {
	var le lastError
	if le.get() != nil {
		t.Error("initial error should be nil")
	}
	le.set(nil) // should not panic
	if le.get() != nil {
		t.Error("setting nil should keep nil")
	}
	le.set(fmt.Errorf("test error")) // use a concrete error
	if le.get() == nil {
		t.Error("expected non-nil after set")
	}
}

// ---------------------------------------------------------------------------
// Model and Provider accessors
// ---------------------------------------------------------------------------

func TestModelAccessor(t *testing.T) {
	b := &baseExecutor{model: "gpt-4o"}
	if got := b.Model(); got != "gpt-4o" {
		t.Errorf("got %q", got)
	}
}

func TestProviderAccessor(t *testing.T) {
	b := &baseExecutor{provider: "anthropic"}
	if got := b.Provider(); got != "anthropic" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Provider is set correctly by each constructor
// ---------------------------------------------------------------------------

func TestProviderSetByOpenAI(t *testing.T) {
	exec, _ := NewOpenAI("key", "gpt-4o")
	if exec.HealthDetail().Provider != "openai" {
		t.Errorf("provider: got %q", exec.HealthDetail().Provider)
	}
}

func TestProviderSetByAnthropic(t *testing.T) {
	exec, _ := NewAnthropic("key", "")
	if exec.HealthDetail().Provider != "anthropic" {
		t.Errorf("provider: got %q", exec.HealthDetail().Provider)
	}
}

// Composite option check
func TestCompositeOptions(t *testing.T) {
	tracker := &mockCooldownTracker{available: true}
	exec, err := NewOpenAI("key", "gpt-4o",
		WithCooldownTracker(tracker),
		WithTimeout(2*time.Minute),
		WithBaseURL("https://proxy.test"),
		WithMaxRetries(5),
		WithAPIKeyInjector(func() string { return "injected" }),
	)
	if err != nil {
		t.Fatal(err)
	}
	hd := exec.HealthDetail()
	if hd.Status != types.HealthAlive {
		t.Errorf("status: got %v", hd.Status)
	}
	if hd.BaseURL != "https://proxy.test" {
		t.Errorf("baseURL: got %q", hd.BaseURL)
	}
	if hd.MaxRetries != 5 {
		t.Errorf("maxRetries: got %d", hd.MaxRetries)
	}
}
