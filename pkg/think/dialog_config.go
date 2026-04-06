package think

import "strings"

// dialogConfigs holds per-pattern dialog configuration. 12 patterns have configs;
// 5 are solo-only (think, sequential_thinking, recursive_thinking, visual_reasoning, stochastic_algorithm).
var dialogConfigs = map[string]*DialogConfig{
	"mental_model": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Model Advocate"},
			{CLI: "codex", Role: "Skeptic"},
		},
		TopicTemplate:  "Apply mental model '{modelName}' to: {problem}",
		PromptTemplate: "You are a {role}. Analyze this problem using the '{modelName}' mental model:\n\nProblem: {problem}\n\nProvide your perspective as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: 0,
	},
	"debugging_approach": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Debugger"},
			{CLI: "codex", Role: "Systems Engineer"},
		},
		TopicTemplate:  "Debug: {issue} using {approachName}",
		PromptTemplate: "You are a {role}. Help debug this issue using the '{approachName}' approach:\n\nIssue: {issue}\n\nProvide your analysis as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: 0,
	},
	"critical_thinking": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Analyst"},
			{CLI: "codex", Role: "Devil's Advocate"},
		},
		TopicTemplate:  "Critical analysis: {issue}",
		PromptTemplate: "You are a {role}. Critically analyze this issue:\n\nIssue: {issue}\n\nProvide your analysis as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: 10,
	},
	"decision_framework": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Decision Analyst"},
			{CLI: "codex", Role: "Risk Assessor"},
		},
		TopicTemplate:  "Decision: {decision}",
		PromptTemplate: "You are a {role}. Evaluate this decision:\n\nDecision: {decision}\n\nProvide your evaluation as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: 30,
	},
	"problem_decomposition": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Architect"},
			{CLI: "codex", Role: "Integration Specialist"},
		},
		TopicTemplate:  "Decompose: {problem}",
		PromptTemplate: "You are a {role}. Decompose this problem:\n\nProblem: {problem}\n\nProvide your decomposition as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: 10,
	},
	"structured_argumentation": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Proponent"},
			{CLI: "codex", Role: "Opponent"},
		},
		TopicTemplate:  "Argue: {topic}",
		PromptTemplate: "You are a {role}. Build arguments about:\n\nTopic: {topic}\n\nProvide your arguments as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: 20,
	},
	"metacognitive_monitoring": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Cognitive Scientist"},
			{CLI: "codex", Role: "Domain Expert"},
		},
		TopicTemplate:  "Monitor cognition: {task}",
		PromptTemplate: "You are a {role}. Monitor cognitive processes for:\n\nTask: {task}\n\nProvide your assessment as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: -10,
	},
	"domain_modeling": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Domain Expert"},
			{CLI: "codex", Role: "Software Architect"},
		},
		TopicTemplate:  "Model domain: {domainName}",
		PromptTemplate: "You are a {role}. Model this domain:\n\nDomain: {domainName}\n{description}\n\nProvide your model as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: 10,
	},
	"architecture_analysis": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Solutions Architect"},
			{CLI: "codex", Role: "Risk Analyst"},
		},
		TopicTemplate:  "Architecture analysis",
		PromptTemplate: "You are a {role}. Analyze this architecture:\n\nProvide your analysis as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: 10,
	},
	"scientific_method": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Researcher"},
			{CLI: "codex", Role: "Peer Reviewer"},
		},
		TopicTemplate:  "Scientific inquiry: {observation}",
		PromptTemplate: "You are a {role}. Review this scientific inquiry:\n\nProvide your review as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: 10,
	},
	"temporal_thinking": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Systems Analyst"},
			{CLI: "codex", Role: "Historian"},
		},
		TopicTemplate:  "Temporal analysis: {timeFrame}",
		PromptTemplate: "You are a {role}. Analyze temporal aspects:\n\nTimeframe: {timeFrame}\n\nProvide your analysis as {role}.",
		MaxTurns:       4,
		Mode:           "sequential",
		Synthesize:     true,
		ComplexityBias: 0,
	},
	"collaborative_reasoning": {
		Participants: []DialogParticipant{
			{CLI: "gemini", Role: "Lead Analyst"},
			{CLI: "codex", Role: "Critical Reviewer"},
		},
		TopicTemplate:  "Collaborative reasoning: {topic}",
		PromptTemplate: "You are a {role}. Engage in collaborative reasoning:\n\nTopic: {topic}\n\nProvide your contribution as {role}.",
		MaxTurns:       6,
		Mode:           "consensus",
		Synthesize:     true,
		ComplexityBias: 20,
	},
}

// GetDialogConfig returns the dialog configuration for a pattern, or nil if solo-only.
func GetDialogConfig(pattern string) *DialogConfig {
	return dialogConfigs[pattern]
}

// BuildDialogTopic interpolates the topic template with input values.
func BuildDialogTopic(config *DialogConfig, input map[string]any) string {
	return interpolateTemplate(config.TopicTemplate, input)
}

// BuildPatternDialogPrompt interpolates the prompt template with input values.
func BuildPatternDialogPrompt(config *DialogConfig, input map[string]any) string {
	return interpolateTemplate(config.PromptTemplate, input)
}

// GetDialogPatterns returns the list of patterns that have dialog configs.
func GetDialogPatterns() []string {
	names := make([]string, 0, len(dialogConfigs))
	for name := range dialogConfigs {
		names = append(names, name)
	}
	return names
}

// interpolateTemplate replaces {key} placeholders with values from the input map.
func interpolateTemplate(template string, input map[string]any) string {
	result := template
	for key, val := range input {
		if s, ok := val.(string); ok {
			result = strings.ReplaceAll(result, "{"+key+"}", s)
		}
	}
	return result
}
