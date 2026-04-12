package server

import (
	"strings"
	"testing"
)

func TestStatefulToolDescriptions_IncludeRequiredTools(t *testing.T) {
	required := []string{"investigate", "think", "consensus", "debate", "dialog", "workflow"}
	if len(statefulToolDescriptions) != len(required) {
		t.Fatalf("statefulToolDescriptions length = %d, want %d", len(statefulToolDescriptions), len(required))
	}

	for _, tool := range required {
		desc, ok := statefulToolDescriptions[tool]
		if !ok {
			t.Fatalf("missing description for tool %q", tool)
		}

		if strings.TrimSpace(desc.What) == "" {
			t.Fatalf("tool %q missing WHAT section", tool)
		}
		if strings.TrimSpace(desc.When) == "" {
			t.Fatalf("tool %q missing WHEN section", tool)
		}
		if strings.TrimSpace(desc.Why) == "" {
			t.Fatalf("tool %q missing WHY section", tool)
		}
		if strings.TrimSpace(desc.How) == "" {
			t.Fatalf("tool %q missing HOW section", tool)
		}
		if strings.TrimSpace(desc.NotDo) == "" {
			t.Fatalf("tool %q missing NOT-DO section (explicit statement of what the tool does not do)", tool)
		}
		if strings.TrimSpace(desc.Choose) == "" {
			t.Fatalf("tool %q missing CHOOSE section", tool)
		}

		rendered := mustStatefulToolDescription(tool)
		for _, heading := range []string{"WHAT:", "WHEN:", "WHY:", "HOW:", "NOT:", "CHOOSE:"} {
			if !strings.Contains(rendered, heading) {
				t.Fatalf("rendered description for %q missing heading %q", tool, heading)
			}
		}

		// The NOT section must contain a meaningful negative statement.
		// This test fails if NotDo is zeroed out (swap body→return null guard).
		notIdx := strings.Index(rendered, "NOT:")
		chooseIdx := strings.Index(rendered, "CHOOSE:")
		if notIdx < 0 || chooseIdx < 0 || chooseIdx <= notIdx {
			t.Fatalf("rendered description for %q has NOT: after or without CHOOSE:", tool)
		}
		notSection := rendered[notIdx:chooseIdx]
		if !strings.Contains(strings.ToLower(notSection), "not") && !strings.Contains(strings.ToLower(notSection), "does not") {
			t.Fatalf("NOT section for %q does not contain a negative statement", tool)
		}
	}
}

func TestMustStatefulToolDescription_PanicsWhenMissing(t *testing.T) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unknown stateful tool")
		}
	}()

	_ = mustStatefulToolDescription("unknown-tool")
}
