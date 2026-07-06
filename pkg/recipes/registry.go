package recipes

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/workflow"
)

const (
	TaskClassReview = "review"
)

type Recipe struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	TaskClass       string   `json:"task_class"`
	ReadOnly        bool     `json:"read_only"`
	Phases          []string `json:"phases"`
	PolicyNeeds     []string `json:"policy_needs"`
	OutputResources []string `json:"output_resources"`
	RequiredArgs    []string `json:"required_args,omitempty"`
	GateDefault     bool     `json:"gate_default,omitempty"`
	WorkflowID      string   `json:"recipe_workflow_id,omitempty"`
	WorkflowSource  string   `json:"recipe_workflow_source,omitempty"`
	WorkflowSteps   []string `json:"recipe_workflow_steps,omitempty"`
}

var registry = []Recipe{
	{
		ID:          "code-review",
		Title:       "Code Review",
		Description: "Run the existing multi-pass review worker as a named read-only recipe with gate semantics.",
		TaskClass:   TaskClassReview,
		ReadOnly:    true,
		Phases: []string{
			"structural",
			"behavioural",
			"adversarial",
		},
		PolicyNeeds: []string{
			PolicyReadOnly,
			PolicyStructuredReviewOutput,
			PolicyTargetRequired,
		},
		OutputResources: []string{
			"task_snapshot",
			"task_events",
			"task_progress",
		},
		RequiredArgs: []string{"target"},
		GateDefault:  true,
	},
	{
		ID:          "second-opinion",
		Title:       "Second Opinion",
		Description: "Run the existing multi-pass review worker in aggregate mode for a read-only independent assessment.",
		TaskClass:   TaskClassReview,
		ReadOnly:    true,
		Phases: []string{
			"structural",
			"behavioural",
			"adversarial",
		},
		PolicyNeeds: []string{
			PolicyReadOnly,
			PolicyStructuredReviewOutput,
			PolicyTargetRequired,
		},
		OutputResources: []string{
			"task_snapshot",
			"task_events",
			"task_progress",
		},
		RequiredArgs: []string{"target"},
	},
	workflowRecipe(Recipe{
		ID:              "security-audit",
		Title:           "Security Audit",
		Description:     "Run the compiled security audit workflow as a read-only curated recipe behind the task entry point.",
		TaskClass:       TaskClassReview,
		ReadOnly:        true,
		PolicyNeeds:     readOnlyWorkflowPolicyNeeds(),
		OutputResources: readOnlyWorkflowOutputResources(),
		RequiredArgs:    []string{"target"},
	}, "secaudit", "pkg/workflow/secaudit.go"),
	workflowRecipe(Recipe{
		ID:              "debug-investigation",
		Title:           "Debug Investigation",
		Description:     "Run the compiled debugging workflow as a read-only curated recipe behind the task entry point.",
		TaskClass:       TaskClassReview,
		ReadOnly:        true,
		PolicyNeeds:     readOnlyWorkflowPolicyNeeds(),
		OutputResources: readOnlyWorkflowOutputResources(),
		RequiredArgs:    []string{"target"},
	}, "debug", "pkg/workflow/debug.go"),
}

func List() []Recipe {
	out := make([]Recipe, 0, len(registry))
	for _, recipe := range registry {
		out = append(out, cloneRecipe(recipe))
	}
	return out
}

func Resolve(id string) (Recipe, bool) {
	normalized := strings.TrimSpace(id)
	if normalized == "" {
		return Recipe{}, false
	}
	for _, recipe := range registry {
		if recipe.ID == normalized {
			return cloneRecipe(recipe), true
		}
	}
	return Recipe{}, false
}

func AvailableIDs() []string {
	ids := make([]string, 0, len(registry))
	for _, recipe := range registry {
		ids = append(ids, recipe.ID)
	}
	return ids
}

func cloneRecipe(recipe Recipe) Recipe {
	recipe.Phases = cloneStrings(recipe.Phases)
	recipe.PolicyNeeds = cloneStrings(recipe.PolicyNeeds)
	recipe.OutputResources = cloneStrings(recipe.OutputResources)
	recipe.RequiredArgs = cloneStrings(recipe.RequiredArgs)
	recipe.WorkflowSteps = cloneStrings(recipe.WorkflowSteps)
	return recipe
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func workflowRecipe(recipe Recipe, workflowID string, source string) Recipe {
	recipe.WorkflowID = workflowID
	recipe.WorkflowSource = source
	recipe.WorkflowSteps = compiledWorkflowStepNames(workflowID)
	recipe.Phases = cloneStrings(recipe.WorkflowSteps)
	return recipe
}

func compiledWorkflowStepNames(workflowID string) []string {
	stepsFn, ok := workflow.Registry[workflowID]
	if !ok {
		panic(fmt.Sprintf("recipe workflow %q is not registered", workflowID))
	}
	steps := stepsFn()
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		out = append(out, step.Name)
	}
	return out
}

func readOnlyWorkflowPolicyNeeds() []string {
	return []string{PolicyReadOnly, PolicyStructuredReviewOutput, PolicyTargetRequired}
}

func readOnlyWorkflowOutputResources() []string {
	return []string{"task_snapshot", "task_events", "task_progress"}
}
