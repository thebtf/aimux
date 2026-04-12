package server

import "fmt"

// StatefulToolDescription is structured guidance for tool descriptions.
// The fields are intentionally explicit so guidance stays data-first.
type StatefulToolDescription struct {
	What   string
	When   string
	Why    string
	How    string
	NotDo  string // explicit statement of what the tool does NOT do
	Choose string
}

var statefulToolDescriptions = map[string]StatefulToolDescription{
	"investigate": {
		What:   "Structured investigation lifecycle with tracked findings, corrections, and convergence/coverage checks.",
		When:   "Use for debugging or research work where root cause is uncertain and assumptions must be validated.",
		Why:    "Prevents premature conclusions by enforcing evidence quality and coverage before final reporting.",
		How:    "Call start (topic/domain), then iterate finding + assess until convergence and coverage targets are met, then call report.",
		NotDo:  "Does NOT execute fixes, write code, or produce immediate answers — it produces a structured evidence report only.",
		Choose: "Choose investigate over ad-hoc debugging when the problem is ambiguous, cross-cutting, or high impact.",
	},
	"think": {
		What:   "Structured reasoning engine with 23 patterns for analysis, planning, debugging, and synthesis.",
		When:   "Use when you need explicit reasoning structure, repeatable thought process, or session-based iterative analysis.",
		Why:    "Improves reasoning quality by matching the problem shape to a pattern instead of free-form analysis.",
		How:    "Provide pattern plus pattern-specific fields; pass session_id to continue stateful patterns across turns.",
		NotDo:  "Does NOT invoke external CLIs, run code, or consult other models — reasoning happens locally within the pattern engine.",
		Choose: "Choose think for deep single-thread reasoning; choose consensus/debate when you need multiple model perspectives.",
	},
	"consensus": {
		What:   "Blinded multi-model consensus run that compares independent positions and can synthesize a final summary.",
		When:   "Use when decisions benefit from independent model agreement checks rather than one model's opinion.",
		Why:    "Reduces single-model bias and reveals where viewpoints align or diverge.",
		How:    "Provide topic; optionally set blinded/max_turns and synthesize to request a merged recommendation.",
		NotDo:  "Does NOT perform adversarial rebuttals or stress-test arguments — use debate when challenge quality matters more than agreement scoring.",
		Choose: "Choose consensus to measure agreement quality; choose debate when you need adversarial argument stress-testing.",
	},
	"debate": {
		What:   "Adversarial multi-model debate that develops opposing arguments and optional synthesized verdict.",
		When:   "Use for contested claims, risky architecture choices, or tradeoffs with meaningful downsides.",
		Why:    "Forces explicit rebuttals that expose weak assumptions before implementation.",
		How:    "Provide topic; optionally tune max_turns and synthesize to produce a final verdict summary.",
		NotDo:  "Does NOT reach binding consensus or guarantee agreement — it surfaces strongest arguments on each side, not a definitive answer.",
		Choose: "Choose debate when challenge and rebuttal quality matters more than agreement scoring.",
	},
	"dialog": {
		What:   "Sequential multi-turn conversation between CLIs to iterate ideas and refine direction.",
		When:   "Use when a single prompt is insufficient and you want progressive refinement through turns.",
		Why:    "Surfaces incremental insights and clarifications that one-shot prompts can miss.",
		How:    "Provide prompt and optional max_turns; review the turn-by-turn exchange to decide next action.",
		NotDo:  "Does NOT follow a deterministic step sequence or enforce convergence — it is open-ended exploration, not a structured pipeline.",
		Choose: "Choose dialog for exploratory iteration; choose think for pattern-driven structure or workflow for deterministic steps.",
	},
	"workflow": {
		What:   "Declarative pipeline runner for multi-step tasks where steps can call exec, think, or investigate.",
		When:   "Use when work must follow an explicit sequence with reusable templates and cross-step references.",
		Why:    "Makes orchestration deterministic, inspectable, and repeatable for complex multi-phase tasks.",
		How:    "Provide JSON steps (id/tool/params with optional condition/on_error) and optional input templated into later steps.",
		NotDo:  "Does NOT adapt step order dynamically or handle open-ended exploration — it executes exactly the steps you define.",
		Choose: "Choose workflow for repeatable chains; choose exec/agent tools for one-shot tasks.",
	},
}

func mustStatefulToolDescription(tool string) string {
	desc, ok := statefulToolDescriptions[tool]
	if !ok {
		panic(fmt.Sprintf("missing structured description for stateful tool %q", tool))
	}
	return renderStatefulToolDescription(desc)
}

func renderStatefulToolDescription(desc StatefulToolDescription) string {
	return fmt.Sprintf(
		"WHAT: %s\n\nWHEN: %s\n\nWHY: %s\n\nHOW: %s\n\nNOT: %s\n\nCHOOSE: %s",
		desc.What,
		desc.When,
		desc.Why,
		desc.How,
		desc.NotDo,
		desc.Choose,
	)
}
