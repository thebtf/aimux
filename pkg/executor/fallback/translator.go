package fallback

import "github.com/thebtf/aimux/pkg/executor/picker"

// Translator adapts a TaskSpec for the next CLI in the fallback chain (spec FR-5, ADR-004).
//
// v1: PassThroughTranslator — returns the input unchanged. codex/claude/gemini share
// plain natural-language prompt syntax for the task classes in scope (code, review,
// task, research), so no per-CLI framing is needed in v1.
//
// v2: per-CLI framing adapters reserved if quality diverges in dogfood.
type Translator interface {
	// Adapt returns a (possibly modified) TaskSpec suitable for the target CLI.
	// fromCLI is the original CLI that failed; toCLI is the next CLI to try.
	// Both are provided so future adapters can perform directional transformations.
	Adapt(spec picker.TaskSpec, fromCLI, toCLI string) picker.TaskSpec
}

// PassThroughTranslator implements Translator by returning the input spec unchanged.
// This is the v1 default (ADR-004).
type PassThroughTranslator struct{}

// NewPassThroughTranslator constructs a PassThroughTranslator.
func NewPassThroughTranslator() *PassThroughTranslator {
	return &PassThroughTranslator{}
}

// Adapt returns spec unchanged. All three active CLIs (codex, claude, gemini) accept
// the same natural-language prompt format for v1 task classes.
func (t *PassThroughTranslator) Adapt(spec picker.TaskSpec, _, _ string) picker.TaskSpec {
	return spec
}
