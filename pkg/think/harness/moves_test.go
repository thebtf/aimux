package harness

import "testing"

func TestMoveCatalogGroupsByReasoningPurpose(t *testing.T) {
	catalog := NewDefaultMoveCatalog()

	for _, group := range []MoveGroup{
		MoveGroupFrame,
		MoveGroupExplore,
		MoveGroupTest,
		MoveGroupEvaluate,
		MoveGroupCalibrate,
		MoveGroupFinalize,
	} {
		moves := catalog.MovesForGroup(group)
		if len(moves) == 0 {
			t.Fatalf("group %q has no cognitive moves", group)
		}
	}
}

func TestMoveCatalogDoesNotModelDomainWorkflows(t *testing.T) {
	catalog := NewDefaultMoveCatalog()

	for _, move := range catalog.AllMoves() {
		switch move.Group {
		case "architecture", "debug", "research":
			t.Fatalf("move %q uses domain workflow group %q", move.Name, move.Group)
		}
		if move.DomainWorkflow != "" {
			t.Fatalf("move %q should not name a domain workflow: %+v", move.Name, move)
		}
	}
}

func TestMoveCatalogRepresentsShippedLowLevelPatterns(t *testing.T) {
	catalog := NewDefaultMoveCatalog()

	for _, name := range []string{
		"architecture_analysis",
		"critical_thinking",
		"debugging_approach",
		"decision_framework",
		"metacognitive_monitoring",
		"research_synthesis",
	} {
		move, ok := catalog.Find(name)
		if !ok {
			t.Fatalf("move %q missing from catalog", name)
		}
		if move.Pattern != name {
			t.Fatalf("move %q pattern = %q, want %q", name, move.Pattern, name)
		}
	}
}
