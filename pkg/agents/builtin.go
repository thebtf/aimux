package agents

// builtinAgents are generic agents registered by default when no project-specific agents exist.
// They provide coverage for common task types without requiring agent file setup.
var builtinAgents = []*Agent{
	{
		Name:        "researcher",
		Description: "Research and analyze topics using multiple sources",
		Role:        "analyze",
		Domain:      "research",
		Content:     "You are a research specialist. Gather information from multiple sources, cross-reference findings, and produce structured analysis with citations. Focus on accuracy over speed.",
		MaxTurns:    1,
		Source:      "builtin",
	},
	{
		Name:        "reviewer",
		Description: "Review code for quality, security, and maintainability",
		Role:        "codereview",
		Domain:      "review",
		Content:     "You are a code review expert. Analyze code for bugs, security issues, performance problems, and maintainability concerns. Provide specific file:line references for every finding.",
		MaxTurns:    1,
		Source:      "builtin",
	},
	{
		Name:        "debugger",
		Description: "Debug issues using systematic root cause analysis",
		Role:        "debug",
		Domain:      "debugging",
		Content:     "You are a debugging specialist. Use systematic investigation: reproduce the issue, form hypotheses, test each one, identify root cause. Never guess — verify with evidence.",
		MaxTurns:    1,
		Source:      "builtin",
	},
	{
		Name:        "implementer",
		Description: "Implement features and fix bugs with tests",
		Role:        "coding",
		Domain:      "implementation",
		Content:     "You are a senior developer. Implement the requested changes with clean, tested code. Follow existing patterns. Write tests for new functionality. Commit message format: type: description.",
		MaxTurns:    1,
		Source:      "builtin",
	},
}

// RegisterBuiltins adds the built-in generic agents to the registry.
// Built-ins are registered with lowest priority — project and user agents shadow them
// if they share the same name.
func RegisterBuiltins(r *Registry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, a := range builtinAgents {
		// Only register if no agent with this name was already discovered
		if _, exists := r.agents[a.Name]; !exists {
			// Copy to avoid shared pointer mutation between registries
			copy := *a
			copy.Meta = map[string]string{"source_type": "builtin"}
			r.agents[copy.Name] = &copy
		}
	}
}
