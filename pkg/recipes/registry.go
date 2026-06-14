package recipes

import "strings"

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
			"read_only",
			"structured_review_output",
			"target_required",
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
			"read_only",
			"structured_review_output",
			"target_required",
		},
		OutputResources: []string{
			"task_snapshot",
			"task_events",
			"task_progress",
		},
		RequiredArgs: []string{"target"},
	},
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
