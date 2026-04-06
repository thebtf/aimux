package orchestrator

import "testing"

func TestQualityGate_ContinueOnValid(t *testing.T) {
	qg := NewQualityGate(2)
	action := qg.Evaluate("gemini", "Here is a detailed analysis...", "", 0)
	if action != QualityContinue {
		t.Errorf("action = %q, want continue", action)
	}
}

func TestQualityGate_RetryOnEmpty(t *testing.T) {
	qg := NewQualityGate(2)
	action := qg.Evaluate("gemini", "", "", 0)
	if action != QualityRetry {
		t.Errorf("action = %q, want retry", action)
	}
}

func TestQualityGate_RetryOnError(t *testing.T) {
	qg := NewQualityGate(2)
	action := qg.Evaluate("gemini", "", "rate limit exceeded", 1)
	if action != QualityRetry {
		t.Errorf("action = %q, want retry", action)
	}
}

func TestQualityGate_EscalateOnRefusal(t *testing.T) {
	qg := NewQualityGate(2)
	action := qg.Evaluate("gemini", "I cannot help with that request", "", 0)
	if action != QualityEscalate {
		t.Errorf("action = %q, want escalate", action)
	}
}

func TestQualityGate_HaltOnMaxRetries(t *testing.T) {
	qg := NewQualityGate(2)

	// First retry
	action := qg.Evaluate("gemini", "", "", 0)
	if action != QualityRetry {
		t.Fatalf("first attempt: %q, want retry", action)
	}

	// Second retry
	action = qg.Evaluate("gemini", "", "", 0)
	if action != QualityRetry {
		t.Fatalf("second attempt: %q, want retry", action)
	}

	// Third → halt
	action = qg.Evaluate("gemini", "", "", 0)
	if action != QualityHalt {
		t.Errorf("third attempt: %q, want halt", action)
	}
}

func TestQualityGate_ContinueOnWarnings(t *testing.T) {
	qg := NewQualityGate(2)
	// Short content produces a warning but is valid
	action := qg.Evaluate("gemini", "short", "", 0)
	if action != QualityContinue {
		t.Errorf("action = %q, want continue (warnings are ok)", action)
	}
}

func TestQualityGate_Reset(t *testing.T) {
	qg := NewQualityGate(1)

	// Use up retry
	qg.Evaluate("gemini", "", "", 0)
	qg.Evaluate("gemini", "", "", 0) // halt

	// Reset
	qg.Reset()

	// Should retry again
	action := qg.Evaluate("gemini", "", "", 0)
	if action != QualityRetry {
		t.Errorf("after reset: %q, want retry", action)
	}
}
