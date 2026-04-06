package investigate

// DefaultAngles are the investigation angles rotated per iteration (from v2).
var DefaultAngles = []DomainAngle{
	{Label: "file-level", Description: "Read code, understand structure, find basic issues", ThinkPattern: "problem_decomposition", ThinkParams: map[string]string{"problem": "{topic}"}},
	{Label: "architecture", Description: "How do components interact? What are the boundaries?", ThinkPattern: "architecture_analysis", ThinkParams: map[string]string{"target": "{topic}"}},
	{Label: "inversion", Description: "What would GUARANTEE failure? What's the worst input?", ThinkPattern: "mental_model", ThinkParams: map[string]string{"modelName": "inversion", "problem": "{topic}"}},
	{Label: "adversarial", Description: "Try to break it. Edge cases, race conditions, resource limits.", ThinkPattern: "critical_thinking", ThinkParams: map[string]string{"issue": "{topic}"}},
	{Label: "information-theory", Description: "What information is LOST? What's discarded that shouldn't be?", ThinkPattern: "scientific_method", ThinkParams: map[string]string{"observation": "{topic}"}},
	{Label: "peer-review", Description: "If a senior engineer reviewed this, what would they flag?", ThinkPattern: "metacognitive_monitoring", ThinkParams: map[string]string{"task": "{topic}", "confidence": "medium"}},
	{Label: "domain", Description: "Does this solve the RIGHT problem? Is the abstraction correct?", ThinkPattern: "domain_modeling", ThinkParams: map[string]string{"domainName": "{topic}"}},
	{Label: "economic", Description: "What's the cost of bugs here? Where do errors hurt most?", ThinkPattern: "decision_framework", ThinkParams: map[string]string{"decision": "{topic}", "options": "current approach, alternative"}},
}

// GenericDomain is the default investigation domain.
var GenericDomain = DomainAlgorithm{
	Name:        "generic",
	Description: "General-purpose investigation — structure, behavior, data flow, error paths, edge cases",
	CoverageAreas: []string{
		"source_code", "original_intent", "production_usage", "test_coverage",
		"error_paths", "caller_experience", "performance", "state_management",
		"competing_alternatives", "live_validation",
	},
	Methods: map[string]string{
		"source_code":            "Read actual implementations with Read/Grep. For large codebases, use subagent(code-reviewer) for parallel scan.",
		"original_intent":        "Read design docs, ADRs, original source repo. Use subagent to analyze if repo is large.",
		"production_usage":       "Query DB or logs. Check session counts, error rates, usage patterns.",
		"test_coverage":          "Read test files — tests encode what developers KNOW can break. Grep for skip/todo/fixme.",
		"error_paths":            "Make live calls with bad/missing input. Observe actual error messages.",
		"caller_experience":      "Call the tool end-to-end with real inputs. Observe the full response. Is it useful?",
		"performance":            "Benchmark with timing. Measure, don't assume. Check for O(n^2) patterns.",
		"state_management":       "Call multiple times with session_id. Check memory growth. Look for stale state.",
		"competing_alternatives": "Search for alternative approaches. Compare tradeoffs.",
		"live_validation":        "Call with correct input, verify output matches expectations. The ultimate test.",
	},
	Patterns: []PatternEntry{
		{Indicator: "TODO/FIXME/HACK comments in implementation code", Severity: SeverityP2, FixApproach: "Resolve the TODO or document why it's deferred in TECHNICAL_DEBT.md"},
		{Indicator: "Empty catch blocks or swallowed errors", Severity: SeverityP1, FixApproach: "Log error context at minimum; re-throw or return error result for callers"},
		{Indicator: "Hardcoded values that should be configurable", Severity: SeverityP2, FixApproach: "Extract to config/constants with clear naming"},
		{Indicator: "Functions with >5 parameters or >50 lines", Severity: SeverityP3, FixApproach: "Decompose into smaller functions or use parameter objects"},
		{Indicator: "Missing input validation at system boundaries", Severity: SeverityP1, FixApproach: "Add schema validation for all external inputs"},
		{Indicator: "State mutation instead of immutable updates", Severity: SeverityP2, FixApproach: "Use immutable patterns — create new objects instead of mutating"},
		{Indicator: "Missing error handling in async operations", Severity: SeverityP0, FixApproach: "Handle errors explicitly, prevent unhandled failures"},
		{Indicator: "Dead code (unused exports, unreachable branches)", Severity: SeverityP3, FixApproach: "Remove dead code; verify with reference search before deletion"},
	},
	AntiPatterns: []string{
		"Don't assume code works based on reading alone — call it with real inputs to verify behavior.",
		"Don't trust comments over implementation — comments lie, code doesn't.",
		"Don't stop at first impression — the audit that built this tool declared 'converged' at iteration 4, then iteration 5 found a critical correction.",
		"Don't investigate only happy paths — error paths and edge cases contain the most dangerous issues.",
		"Don't rely on training memory for API behavior — verify with tool calls (Read, Grep, Context7).",
	},
}

// DebuggingDomain provides debugging-specific investigation strategy.
var DebuggingDomain = DomainAlgorithm{
	Name:        "debugging",
	Description: "Debugging investigation — reproduce, isolate, hypothesis-test, verify, regression prevention",
	CoverageAreas: []string{
		"reproduction", "isolation", "hypothesis_formation", "root_cause_analysis",
		"fix_verification", "regression_prevention", "environmental_factors", "error_trail",
	},
	Methods: map[string]string{
		"reproduction":          "Create minimal reproduction: strip away unrelated code until the bug persists. Document exact steps.",
		"isolation":             "Binary search the codebase: comment out halves until the bug disappears. Use git bisect.",
		"hypothesis_formation":  "Form 2-3 hypotheses about the root cause. For each: what evidence would confirm? Refute?",
		"root_cause_analysis":   "Trace execution path from trigger to failure point. Read stack trace bottom-up.",
		"fix_verification":      "After fixing: does the original reproduction still pass? Are there similar patterns elsewhere?",
		"regression_prevention": "Add test that catches this exact bug. Check if a lint rule could prevent the category.",
		"environmental_factors": "Check OS-specific behavior, version differences, env vars, race conditions, timezone/locale.",
		"error_trail":           "Read the FULL error message. Check stderr. Look for earlier warnings that predict the failure.",
	},
	Patterns: []PatternEntry{
		{Indicator: "Error message points to wrong location (symptom vs cause mismatch)", Severity: SeverityP1, FixApproach: "Trace back from symptom to actual cause using stack trace + data flow analysis."},
		{Indicator: "Bug only reproduces intermittently (race condition)", Severity: SeverityP0, FixApproach: "Add timing/ordering constraints. Use mutexes or sequential queuing. Reproduce by adding artificial delays."},
		{Indicator: "Bug only on one platform (Windows/Linux/macOS)", Severity: SeverityP2, FixApproach: "Check path separators, line endings, case sensitivity, process signals, file locking."},
		{Indicator: "Test passes in isolation but fails in suite (shared state)", Severity: SeverityP1, FixApproach: "Isolate test state: use setup/teardown cleanup. Check for global mutations, singleton state."},
		{Indicator: "Fix that only masks the symptom", Severity: SeverityP1, FixApproach: "Verify the fix addresses root cause, not symptom. Ask: if I fix at root cause instead, does it work?"},
		{Indicator: "Silent failure — no error thrown but wrong result", Severity: SeverityP1, FixApproach: "Add assertions at intermediate steps. Check for type coercion surprises."},
		{Indicator: "Timeout without clear cause", Severity: SeverityP2, FixApproach: "Add progress logging. Check for deadlocks, awaiting something that never resolves."},
	},
	AntiPatterns: []string{
		"Don't guess-and-check — form a hypothesis, predict what you'll see, then test.",
		"Don't change multiple things at once — change one variable, test, revert if it didn't help.",
		"Don't say 'it works on my machine' — investigate the environment difference.",
		"Don't keep retrying the same fix — after 2 failures, reconsider your mental model.",
		"Don't trust the error message literally — it tells you WHAT failed, not WHY.",
	},
	Angles: []DomainAngle{
		{Label: "scientific-method", Description: "Observe → hypothesize → predict → test → conclude", ThinkPattern: "scientific_method", ThinkParams: map[string]string{"observation": "{topic}"}},
		{Label: "systematic-elimination", Description: "Binary search: eliminate half the possibilities each step", ThinkPattern: "debugging_approach", ThinkParams: map[string]string{"problem": "{topic}"}},
		{Label: "timeline-reconstruction", Description: "What happened in what order? Reconstruct the sequence", ThinkPattern: "sequential_thinking", ThinkParams: map[string]string{"problem": "{topic}"}},
		{Label: "inversion", Description: "What conditions would guarantee this bug? Work backward from failure", ThinkPattern: "mental_model", ThinkParams: map[string]string{"modelName": "inversion", "problem": "{topic}"}},
		{Label: "assumptions-audit", Description: "List every assumption about how the system works. Which one is wrong?", ThinkPattern: "metacognitive_monitoring", ThinkParams: map[string]string{"task": "{topic}", "confidence": "low"}},
	},
}

// domainRegistry maps domain names to algorithms.
var domainRegistry = map[string]*DomainAlgorithm{
	"generic":   &GenericDomain,
	"debugging":  &DebuggingDomain,
}

// GetDomain returns the domain algorithm for the given name.
// Returns GenericDomain for empty name or "generic". Returns nil for unknown domains.
func GetDomain(name string) *DomainAlgorithm {
	if name == "" {
		return &GenericDomain
	}
	return domainRegistry[name]
}

// DomainNames returns all registered domain names.
func DomainNames() []string {
	names := make([]string, 0, len(domainRegistry))
	for k := range domainRegistry {
		names = append(names, k)
	}
	return names
}
