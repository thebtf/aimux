package server

// SkillChain represents a recommended next tool after the current one completes.
// Enables composable skill chains where tool output includes navigation hints.
type SkillChain struct {
	CurrentTool string   `json:"current_tool"`
	NextTools   []string `json:"recommended_next"`
	Condition   string   `json:"condition,omitempty"` // when to recommend
}

// DefaultChains returns the built-in skill chain recommendations.
func DefaultChains() []SkillChain {
	return []SkillChain{
		{
			CurrentTool: "sessions",
			NextTools:   []string{"status"},
			Condition:   "when inspecting a specific async job from session output",
		},
		{
			CurrentTool: "think",
			NextTools:   []string{"deepresearch"},
			Condition:   "when reasoning identifies an external knowledge gap",
		},
		{
			CurrentTool: "deepresearch",
			NextTools:   []string{"think"},
			Condition:   "when the report needs synthesis or decision framing",
		},
	}
}

// GetRecommendedNext returns recommended next tools for a given tool.
func GetRecommendedNext(tool string) []string {
	for _, chain := range DefaultChains() {
		if chain.CurrentTool == tool {
			return chain.NextTools
		}
	}
	return nil
}
