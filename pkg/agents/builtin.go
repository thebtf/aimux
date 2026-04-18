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
		// MaxTurns: 1 — research tasks produce a single structured report; additional turns
		// add noise without improving accuracy. Callers re-invoke if they need follow-up depth.
		MaxTurns: 1,
		When:     "Use when you need to gather and synthesize information from multiple sources into a structured, cited report.",
		Source:   "builtin",
	},
	{
		Name:        "reviewer",
		Description: "Review code for quality, security, and maintainability",
		Role:        "codereview",
		Domain:      "review",
		Content:     "You are a code review expert. Analyze code for bugs, security issues, performance problems, and maintainability concerns. Provide specific file:line references for every finding.",
		// MaxTurns: 1 — code review produces a single findings report. Multi-turn review
		// risks scope creep; re-invoke with a narrower scope if follow-up is needed.
		MaxTurns: 1,
		When:     "Use when you need a code review covering bugs, security issues, performance, and maintainability — with file:line references for every finding.",
		Source:   "builtin",
	},
	{
		Name:        "debugger",
		Description: "Debug and investigate issues using systematic root cause analysis",
		Role:        "debug",
		Domain:      "debugging",
		Content:     "You are a debugging and investigation specialist. Investigate issues systematically: reproduce the problem, form hypotheses, test each one, identify root cause. Never guess — verify with evidence.",
		// MaxTurns: 1 — debugging sessions terminate when root cause is identified and a
		// fix is proposed. Iterative probing belongs in investigate(action="finding") instead.
		MaxTurns: 1,
		When:     "Use when you have a specific bug, error, or unexpected behavior to investigate. Provides hypothesis-driven root cause analysis with a concrete fix recommendation.",
		Source:   "builtin",
	},
	{
		Name:        "implementer",
		Description: "Implement features and fix bugs with tests",
		Role:        "coding",
		Domain:      "implementation",
		Content:     "You are a senior developer. Implement the requested changes with clean, tested code. Follow existing patterns. Write tests for new functionality. Commit message format: type: description.",
		// MaxTurns: 1 — implementation tasks are bounded by the prompt scope. Multi-turn
		// sessions risk scope drift; break large tasks into phases and invoke once per phase.
		MaxTurns: 1,
		When:     "Use when you need code written, a bug fixed, or a feature implemented. Produces working, tested code that follows existing project conventions.",
		Source:   "builtin",
	},
	{
		Name:        "generic",
		Description: "General-purpose assistant — follows instructions literally, no assumptions",
		// Role: "default" routes to a general-purpose CLI (codex, medium effort) — capable
		// of both analysis and file modification, which is required for a last-resort agent
		// that follows instructions literally (including "implement only what was described").
		// "coding" was considered but is overkill for Q&A/analysis tasks. "analyze" routes
		// to gemini (read-only advisory), which blocks implementation requests.
		Role:   "default",
		Domain: "general",
		Content: "Follow the user's instructions exactly as written. Do not add, expand, or interpret beyond what was explicitly asked. If asked to respond with a specific phrase — respond with that phrase only. If asked to read a file — read it and report what was asked. If asked to implement — implement only what was described. No preamble, no summary, no extra steps.",
		// MaxTurns: 1 — generic agent is designed for precise, bounded tasks. If a task
		// requires iteration, use a specialist agent (debugger, implementer) instead.
		MaxTurns: 1,
		When:     "Use as a last resort when no specialist agent fits. Follows instructions literally with no interpretation. Prefer a named specialist (researcher, reviewer, debugger, implementer) when the task type is clear.",
		Source:   "builtin",
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
