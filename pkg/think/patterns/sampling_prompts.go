package patterns

import "strings"

// SamplingPrompt holds a structured prompt template for requesting LLM analysis.
type SamplingPrompt struct {
	SystemRole string // e.g., "You are a software architecture expert"
	UserPrompt string // template with {input} placeholder
	MaxTokens  int
}

// samplingPrompts maps pattern names to their sampling prompt templates.
var samplingPrompts = map[string]SamplingPrompt{
	"problem_decomposition": {
		SystemRole: "You are a systems architect. Decompose problems into concrete sub-problems.",
		UserPrompt: "Decompose this problem into 3-7 sub-problems with dependencies between them.\n\nProblem: {input}\n\nReturn a JSON object:\n{\"subProblems\": [{\"id\": \"sp1\", \"description\": \"...\"}], \"dependencies\": [{\"from\": \"sp1\", \"to\": \"sp2\", \"reason\": \"...\"}]}\n\nBe specific and concrete. Each sub-problem should be independently implementable.",
		MaxTokens:  2000,
	},
	"peer_review": {
		SystemRole: "You are a senior code reviewer. Find real issues, not nitpicks.",
		UserPrompt: "Review this artifact for correctness, security, maintainability, and performance issues.\n\nArtifact:\n{input}\n\nReturn JSON:\n{\"objections\": [{\"severity\": \"P0|P1|P2|P3\", \"category\": \"...\", \"description\": \"...\", \"suggestion\": \"...\"}], \"strengths\": [\"...\"]}\n\nFocus on issues that would cause production problems.",
		MaxTokens:  2000,
	},
	"decision_framework": {
		SystemRole: "You are a technical decision advisor. Help evaluate options objectively.",
		UserPrompt: "Suggest evaluation criteria for this decision.\n\nDecision: {input}\n\nReturn JSON:\n{\"suggestedCriteria\": [{\"name\": \"...\", \"weight\": 0.0-1.0, \"rationale\": \"...\"}], \"suggestedOptions\": [\"...\"]}\n\nCriteria should be measurable. Weights should sum to 1.0.",
		MaxTokens:  1500,
	},
	"critical_thinking": {
		SystemRole: "You are a cognitive bias expert. Detect reasoning flaws.",
		UserPrompt: "Analyze this statement for cognitive biases, logical fallacies, and unsupported assumptions.\n\nStatement: {input}\n\nReturn JSON:\n{\"biases\": [{\"type\": \"...\", \"evidence\": \"...\", \"severity\": \"low|medium|high\"}], \"fallacies\": [\"...\"], \"unsupportedAssumptions\": [\"...\"]}",
		MaxTokens:  1500,
	},
	"architecture_analysis": {
		SystemRole: "You are a software architect specializing in system design evaluation.",
		UserPrompt: "Analyze this system architecture for coupling, cohesion, scalability, and failure modes.\n\nSystem: {input}\n\nReturn JSON:\n{\"components\": [{\"name\": \"...\", \"dependencies\": [\"...\"]}], \"concerns\": [{\"type\": \"coupling|scalability|reliability|security\", \"description\": \"...\"}]}",
		MaxTokens:  2000,
	},
}

// GetSamplingPrompt returns the sampling prompt for a pattern, or nil if none defined.
func GetSamplingPrompt(patternName string) *SamplingPrompt {
	p, ok := samplingPrompts[patternName]
	if !ok {
		return nil
	}
	return &p
}

// FormatSamplingPrompt replaces {input} in the template with actual text.
// Returns (systemRole, formattedUserPrompt).
func FormatSamplingPrompt(prompt *SamplingPrompt, input string) (string, string) {
	return prompt.SystemRole, strings.Replace(prompt.UserPrompt, "{input}", input, 1)
}
