package deepresearch_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/tools/deepresearch"
)

func TestClient_Close_DoesNotPanic(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-key-for-close")

	client, err := deepresearch.NewClient("gemini-2.0-flash", 30)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// Close should not panic even before any Research call.
	client.Close()
}

func TestClient_Close_Idempotent(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-key-idempotent")

	client, err := deepresearch.NewClient("gemini-2.0-flash", 30)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// Multiple Close calls must not panic.
	client.Close()
	client.Close()
}

func TestClient_SearchCache_EmptyReturnsNil(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-key-search")

	client, err := deepresearch.NewClient("", 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	results := client.SearchCache("anything", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty cache, got %d", len(results))
	}
}

func TestNewClient_DefaultsApplied(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-key-defaults")
	t.Setenv("GEMINI_API_KEY", "")

	// Passing empty model and 0 timeout should succeed (defaults applied inside).
	client, err := deepresearch.NewClient("", 0)
	if err != nil {
		t.Fatalf("NewClient with defaults: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClient_GeminiAPIKeyFallback(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "gemini-fallback-key")

	client, err := deepresearch.NewClient("", 0)
	if err != nil {
		t.Fatalf("NewClient with GEMINI_API_KEY fallback: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}
