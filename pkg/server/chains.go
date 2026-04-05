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
			CurrentTool: "exec",
			NextTools:   []string{"status"},
			Condition:   "async=true",
		},
		{
			CurrentTool: "audit",
			NextTools:   []string{"investigate"},
			Condition:   "mode=deep AND high_findings>0",
		},
		{
			CurrentTool: "investigate",
			NextTools:   []string{"exec"},
			Condition:   "convergence>=1.0",
		},
		{
			CurrentTool: "think",
			NextTools:   []string{"consensus", "exec"},
			Condition:   "complexity>=60",
		},
		{
			CurrentTool: "consensus",
			NextTools:   []string{"exec"},
			Condition:   "synthesize=true",
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
