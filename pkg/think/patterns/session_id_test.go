// Package patterns — session_id_test.go
// Regression test for engram issue #180: cold-start callers (session_id="") must not
// share the session store key "". Each call must produce a distinct, non-empty session ID.
package patterns

import (
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
)

// coldStartInput returns pre-validated minimal input for the named stateful pattern.
func coldStartInput(t *testing.T, patternName string) map[string]any {
	t.Helper()
	p := think.GetPattern(patternName)
	if p == nil {
		t.Fatalf("pattern %q not registered", patternName)
	}

	switch patternName {
	case "sequential_thinking":
		out, err := p.Validate(map[string]any{"thought": "cold start test"})
		if err != nil {
			t.Fatalf("validate %s: %v", patternName, err)
		}
		return out
	case "scientific_method":
		out, err := p.Validate(map[string]any{"stage": "observation"})
		if err != nil {
			t.Fatalf("validate %s: %v", patternName, err)
		}
		return out
	case "debugging_approach":
		out, err := p.Validate(map[string]any{"issue": "cold start test"})
		if err != nil {
			t.Fatalf("validate %s: %v", patternName, err)
		}
		return out
	case "structured_argumentation":
		out, err := p.Validate(map[string]any{"topic": "cold start test"})
		if err != nil {
			t.Fatalf("validate %s: %v", patternName, err)
		}
		return out
	case "collaborative_reasoning":
		out, err := p.Validate(map[string]any{"topic": "cold start test"})
		if err != nil {
			t.Fatalf("validate %s: %v", patternName, err)
		}
		return out
	case "experimental_loop":
		out, err := p.Validate(map[string]any{"hypothesis": "cold start test"})
		if err != nil {
			t.Fatalf("validate %s: %v", patternName, err)
		}
		return out
	default:
		t.Fatalf("unknown stateful pattern %q", patternName)
	}
	return nil
}

// TestColdStartSessionID_NoDuplication verifies that two consecutive cold-start calls
// (session_id="") to each stateful pattern each return a non-empty session_id, and
// that the two returned IDs are distinct (engram#180).
func TestColdStartSessionID_NoDuplication(t *testing.T) {
	RegisterAll()

	statefulPatterns := []string{
		"sequential_thinking",
		"scientific_method",
		"debugging_approach",
		"structured_argumentation",
		"collaborative_reasoning",
		"experimental_loop",
	}

	for _, name := range statefulPatterns {
		t.Run(name, func(t *testing.T) {
			think.ClearSessions()
			defer think.ClearSessions()

			p := think.GetPattern(name)
			if p == nil {
				t.Fatalf("pattern %q not registered", name)
			}

			input1 := coldStartInput(t, name)
			r1, err := p.Handle(input1, "") // cold start — empty session_id
			if err != nil {
				t.Fatalf("call 1: %v", err)
			}
			if r1.SessionID == "" {
				t.Errorf("call 1: session_id is empty, want non-empty UUID")
			}

			input2 := coldStartInput(t, name)
			r2, err := p.Handle(input2, "") // second cold start — must get a different ID
			if err != nil {
				t.Fatalf("call 2: %v", err)
			}
			if r2.SessionID == "" {
				t.Errorf("call 2: session_id is empty, want non-empty UUID")
			}

			if r1.SessionID == r2.SessionID {
				t.Errorf("call 1 and call 2 share the same session_id %q — multi-tenant collision", r1.SessionID)
			}
		})
	}
}
