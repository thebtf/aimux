package policies_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/guidance/policies"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
)

// TestStatefulPatterns_AllRegistered verifies that every pattern listed in the
// statefulPatterns map is actually registered in the think pattern registry.
// If a pattern is removed or renamed, this test will catch the stale entry.
func TestStatefulPatterns_AllRegistered(t *testing.T) {
	patterns.RegisterAll()

	all := think.GetAllPatterns()
	registered := make(map[string]bool, len(all))
	for _, name := range all {
		registered[name] = true
	}

	// The six patterns that maintain session state must all be registered.
	stateful := []string{
		"sequential_thinking",
		"scientific_method",
		"debugging_approach",
		"experimental_loop",
		"structured_argumentation",
		"collaborative_reasoning",
	}

	for _, name := range stateful {
		if !registered[name] {
			t.Errorf("statefulPatterns references %q but it is not registered in the think registry", name)
		}
		// Also verify the policy's IsStatefulPattern agrees.
		if !policies.IsStatefulPattern(name) {
			t.Errorf("IsStatefulPattern(%q) = false, expected true", name)
		}
	}
}

// TestStatefulPatterns_RegistryMembersConsistency verifies that every registered
// pattern that IsStatefulPattern claims is stateful actually exists in the registry —
// i.e. there are no stale entries in the statefulPatterns map.
func TestStatefulPatterns_RegistryMembersConsistency(t *testing.T) {
	patterns.RegisterAll()

	all := think.GetAllPatterns()
	registered := make(map[string]bool, len(all))
	for _, name := range all {
		registered[name] = true
	}

	for _, name := range all {
		if policies.IsStatefulPattern(name) && !registered[name] {
			t.Errorf("pattern %q is marked stateful but not found in registry", name)
		}
	}
}
