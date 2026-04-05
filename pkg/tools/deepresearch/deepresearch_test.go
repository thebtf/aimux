package deepresearch_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/tools/deepresearch"
)

func TestNewClient_MissingAPIKey(t *testing.T) {
	// Ensure env vars are not set for this test
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

	_, err := deepresearch.NewClient("", 0)
	if err == nil {
		t.Fatal("expected error when no API key set")
	}
}

func TestNewClient_WithAPIKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-key-123")

	client, err := deepresearch.NewClient("", 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClient_CustomModel(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-key-456")

	client, err := deepresearch.NewClient("custom-model", 600)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestCache_PutGet(t *testing.T) {
	cache := deepresearch.NewCache()

	cache.Put("topic1", "summary", "model1", nil, "result content")

	entry, ok := cache.Get("topic1", "summary", "model1", nil)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if entry.Content != "result content" {
		t.Errorf("Content = %q, want %q", entry.Content, "result content")
	}
}

func TestCache_Miss(t *testing.T) {
	cache := deepresearch.NewCache()

	_, ok := cache.Get("nonexistent", "summary", "model1", nil)
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestCache_DifferentInputsDifferentResults(t *testing.T) {
	cache := deepresearch.NewCache()

	cache.Put("topic1", "summary", "model1", nil, "result A")
	cache.Put("topic2", "summary", "model1", nil, "result B")

	entryA, okA := cache.Get("topic1", "summary", "model1", nil)
	entryB, okB := cache.Get("topic2", "summary", "model1", nil)

	if !okA || !okB {
		t.Fatal("expected both cache hits")
	}
	if entryA.Content == entryB.Content {
		t.Error("different topics should produce different cached results")
	}
}
