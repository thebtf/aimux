package deepresearch_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/tools/deepresearch"
)

func TestNew_Defaults(t *testing.T) {
	dr := deepresearch.New("", 0)
	if dr.Model() != "deep-research-pro-preview-12-2025" {
		t.Errorf("Model = %q, want default", dr.Model())
	}
	if dr.TimeoutSeconds() != 1800 {
		t.Errorf("Timeout = %d, want 1800", dr.TimeoutSeconds())
	}
}

func TestNew_Custom(t *testing.T) {
	dr := deepresearch.New("custom-model", 600)
	if dr.Model() != "custom-model" {
		t.Errorf("Model = %q, want custom-model", dr.Model())
	}
	if dr.TimeoutSeconds() != 600 {
		t.Errorf("Timeout = %d, want 600", dr.TimeoutSeconds())
	}
}

func TestExecute_Placeholder(t *testing.T) {
	dr := deepresearch.New("", 0)
	result, err := dr.Execute("test topic", "summary", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}
