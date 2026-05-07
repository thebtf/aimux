package fallback

import (
	"testing"

	"github.com/thebtf/aimux/pkg/executor/picker"
)

func TestPassThroughTranslator_Identity(t *testing.T) {
	tr := NewPassThroughTranslator()
	spec := picker.TaskSpec{
		TaskClass: "code",
		Prompt:    "implement a binary search tree",
	}
	got := tr.Adapt(spec, "codex", "claude")
	if got != spec {
		t.Errorf("Adapt() modified spec: got %+v, want %+v", got, spec)
	}
}

func TestPassThroughTranslator_AllDirections(t *testing.T) {
	tr := NewPassThroughTranslator()
	spec := picker.TaskSpec{TaskClass: "review", Prompt: "review this PR diff"}

	pairs := [][2]string{
		{"codex", "claude"},
		{"claude", "gemini"},
		{"gemini", "codex"},
	}
	for _, pair := range pairs {
		got := tr.Adapt(spec, pair[0], pair[1])
		if got != spec {
			t.Errorf("Adapt(%s→%s) mutated spec", pair[0], pair[1])
		}
	}
}

func TestPassThroughTranslator_EmptySpec(t *testing.T) {
	tr := NewPassThroughTranslator()
	empty := picker.TaskSpec{}
	got := tr.Adapt(empty, "from", "to")
	if got != empty {
		t.Errorf("Adapt(empty) returned non-empty: %+v", got)
	}
}
