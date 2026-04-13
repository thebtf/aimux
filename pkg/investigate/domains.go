package investigate

import "strings"

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

// SecurityDomain focuses on security vulnerabilities and threat modeling.
var SecurityDomain = DomainAlgorithm{
	Name:        "security",
	Description: "Security investigation — vulnerabilities, threat modeling, attack surface, defense-in-depth",
	CoverageAreas: []string{
		"authentication", "authorization", "input_validation", "output_encoding",
		"secrets_management", "dependency_vulnerabilities", "transport_security", "error_disclosure",
	},
	Methods: map[string]string{
		"authentication":           "Audit session management, token storage, and expiry. Test with invalid/expired tokens. Check MFA enforcement.",
		"authorization":            "Test every protected endpoint with no credentials, wrong role, and a valid but unprivileged token. Check for IDOR.",
		"input_validation":         "Fuzz inputs with special characters, oversized payloads, null bytes, and injection payloads. Check every system boundary.",
		"output_encoding":          "Trace user-controlled data to render points. Verify HTML, JS, URL, and SQL escaping is context-aware.",
		"secrets_management":       "Grep source for hardcoded secrets. Audit env var access patterns. Check for secrets in logs or error messages.",
		"dependency_vulnerabilities": "Run `govulncheck ./...` or equivalent. Check transitive deps. Cross-reference CVE databases.",
		"transport_security":       "Verify TLS version and cipher suites. Check certificate pinning for mobile clients. Test for HSTS.",
		"error_disclosure":         "Trigger errors deliberately. Check that stack traces and internal details are not exposed to clients.",
	},
	Patterns: []PatternEntry{
		{Indicator: "Hardcoded secrets, API keys, or passwords in source code", Severity: SeverityP0, FixApproach: "Move to environment variables or a secrets manager. Rotate the exposed credential immediately."},
		{Indicator: "SQL or command injection via unsanitized user input", Severity: SeverityP0, FixApproach: "Use parameterized queries or prepared statements. Never concatenate user input into queries."},
		{Indicator: "Cross-site scripting (XSS) via unescaped output", Severity: SeverityP1, FixApproach: "Escape all user-controlled data before rendering. Use context-aware encoding (HTML, JS, URL)."},
		{Indicator: "Path traversal via user-supplied file paths", Severity: SeverityP1, FixApproach: "Canonicalize paths and verify they remain within the allowed root before access."},
		{Indicator: "CSRF missing or bypassable token", Severity: SeverityP1, FixApproach: "Enforce CSRF tokens on all state-changing requests. Validate origin and referrer headers."},
		{Indicator: "Insecure deserialization of untrusted data", Severity: SeverityP1, FixApproach: "Validate type and schema before deserializing. Prefer safe formats (JSON with schema) over binary."},
		{Indicator: "Broken authentication — weak session management or missing MFA", Severity: SeverityP0, FixApproach: "Use proven auth libraries. Enforce session expiry, secure cookie flags, and MFA for sensitive actions."},
		{Indicator: "Excessive permissions granted to service or user role", Severity: SeverityP2, FixApproach: "Apply least-privilege principle. Audit IAM roles and DB grants against actual usage."},
	},
	Angles: []DomainAngle{
		{Label: "attacker_perspective", Description: "Think like an attacker: what is the highest-value target? How would you chain vulnerabilities?", ThinkPattern: "mental_model", ThinkParams: map[string]string{"modelName": "inversion", "problem": "{topic}"}},
		{Label: "compliance", Description: "Which regulatory requirements apply (OWASP, SOC2, GDPR, PCI)? Where are the gaps?", ThinkPattern: "structured_argumentation", ThinkParams: map[string]string{"claim": "{topic}", "context": "compliance requirements"}},
		{Label: "defense_in_depth", Description: "If one control fails, what's the next layer? Where is there only one layer?", ThinkPattern: "problem_decomposition", ThinkParams: map[string]string{"problem": "{topic}"}},
	},
	AntiPatterns: []string{
		"Security through obscurity is not security",
		"Don't roll your own crypto or auth",
		"A security review that finds nothing is more suspicious than one that finds issues",
	},
}

// PerformanceDomain covers latency, throughput, and resource efficiency.
var PerformanceDomain = DomainAlgorithm{
	Name:        "performance",
	Description: "Performance investigation — profiling, bottlenecks, allocation, concurrency, algorithmic complexity",
	CoverageAreas: []string{
		"cpu_hotspots", "memory_allocation", "io_bottlenecks", "database_queries",
		"network_calls", "concurrency", "caching", "algorithm_complexity",
	},
	Methods: map[string]string{
		"cpu_hotspots":       "Profile with pprof CPU sampling. Identify top 5 call sites by CPU time. Look for hot loops with avoidable work.",
		"memory_allocation":  "Profile with pprof heap sampling. Check allocation rate vs. live objects. Look for sync.Pool candidates.",
		"io_bottlenecks":     "Measure with pprof block or trace. Identify synchronous IO in hot paths. Check for missing bufio usage.",
		"database_queries":   "Enable query logging. Look for N+1 patterns. Run EXPLAIN on slow queries. Check index usage.",
		"network_calls":      "Trace outbound calls with timing. Check for sequential calls that could be parallelised. Verify connection reuse.",
		"concurrency":        "Run with -race. Check goroutine count under load. Look for unbounded goroutine launch without lifecycle control.",
		"caching":            "Measure cache hit rate. Check TTL vs. data freshness requirements. Look for negative caching opportunities.",
		"algorithm_complexity": "Identify loops that grow with input size. Check sort/search usage for large inputs. Look for O(n^2) patterns.",
	},
	Patterns: []PatternEntry{
		{Indicator: "N+1 query pattern — query inside loop", Severity: SeverityP1, FixApproach: "Batch queries or use eager loading. Fetch all needed data in one query outside the loop."},
		{Indicator: "Unbounded collection growth — append without eviction", Severity: SeverityP1, FixApproach: "Add size limits, TTL eviction, or pagination. Profile memory growth under load."},
		{Indicator: "Synchronous IO or blocking call in hot path", Severity: SeverityP2, FixApproach: "Move to async IO or off-thread processing. Use connection pooling and non-blocking APIs."},
		{Indicator: "Missing database index on high-cardinality filter column", Severity: SeverityP2, FixApproach: "Add index on frequently filtered/sorted columns. Use EXPLAIN to verify query plan."},
		{Indicator: "Goroutine or thread leak — launched without lifecycle management", Severity: SeverityP1, FixApproach: "Use context cancellation, WaitGroups, or worker pools. Track goroutine count under load."},
		{Indicator: "Excessive per-request allocation causing GC pressure", Severity: SeverityP2, FixApproach: "Use sync.Pool for hot objects. Pre-allocate slices with known capacity. Profile with pprof."},
	},
	Angles: []DomainAngle{
		{Label: "profiler_driven", Description: "Measure first — what does the profiler show? Never optimize a guess.", ThinkPattern: "debugging_approach", ThinkParams: map[string]string{"problem": "{topic}"}},
		{Label: "big_o_analysis", Description: "What is the algorithmic complexity? Does it degrade with data size or concurrency?", ThinkPattern: "recursive_thinking", ThinkParams: map[string]string{"problem": "{topic}"}},
		{Label: "user_experience", Description: "Where does latency hurt users most? P50 vs P99 — which matters here?", ThinkPattern: "mental_model", ThinkParams: map[string]string{"modelName": "second_order_thinking", "problem": "{topic}"}},
	},
	AntiPatterns: []string{
		"Don't optimize without profiling first",
		"Premature optimization is the root of all evil, but so is premature pessimization",
	},
}

// ArchitectureDomain analyzes module boundaries, coupling, and structural decisions.
var ArchitectureDomain = DomainAlgorithm{
	Name:        "architecture",
	Description: "Architecture investigation — module boundaries, coupling, dependency direction, abstraction quality",
	CoverageAreas: []string{
		"module_boundaries", "coupling_analysis", "dependency_direction", "abstraction_levels",
		"data_flow", "error_propagation", "configuration", "extensibility",
	},
	Patterns: []PatternEntry{
		{Indicator: "Circular dependencies between packages or modules", Severity: SeverityP1, FixApproach: "Extract shared interface or common package. Apply dependency inversion to break the cycle."},
		{Indicator: "God object — single type/package doing too many things", Severity: SeverityP2, FixApproach: "Split by responsibility. Each package should have one clear reason to change."},
		{Indicator: "Leaky abstraction — internal details exposed through public API", Severity: SeverityP2, FixApproach: "Define stable interfaces. Hide implementation details behind the interface boundary."},
		{Indicator: "Layer violation — UI calling DB directly, or domain importing infrastructure", Severity: SeverityP1, FixApproach: "Enforce dependency direction: outer layers depend on inner layers, not vice versa."},
	},
	Angles: []DomainAngle{
		{Label: "dependency_graph", Description: "Draw the actual dependency graph. Which nodes have the most in-edges? Which have cycles?", ThinkPattern: "architecture_analysis", ThinkParams: map[string]string{"target": "{topic}"}},
		{Label: "clean_architecture", Description: "Do domain types appear in infrastructure code? Do use cases depend on frameworks?", ThinkPattern: "domain_modeling", ThinkParams: map[string]string{"domainName": "{topic}"}},
		{Label: "evolution", Description: "How hard is it to add a new feature? Which change touches the most files?", ThinkPattern: "temporal_thinking", ThinkParams: map[string]string{"problem": "{topic}"}},
	},
	AntiPatterns: []string{
		"Architecture is not documentation — it's the decisions that constrain the system",
		"Every abstraction leaks — the question is whether the leak matters",
	},
}

// ResearchDomain investigates papers, benchmarks, and prior art with scientific rigor.
var ResearchDomain = DomainAlgorithm{
	Name:        "research",
	Description: "Research investigation — prior art, methodology, reproducibility, claims validation, practical applicability",
	CoverageAreas: []string{
		"prior_art", "methodology", "reproducibility", "limitations",
		"comparisons", "novelty_claim", "implementation_gaps", "real_world_applicability",
	},
	Patterns: []PatternEntry{
		{Indicator: "Cherry-picked benchmarks — favorable subset presented as representative", Severity: SeverityP2, FixApproach: "Reproduce full benchmark suite. Check what baselines were omitted and why."},
		{Indicator: "Missing baselines — no comparison to obvious alternatives", Severity: SeverityP2, FixApproach: "Identify the standard baselines for this problem class. Demand or run the comparison."},
		{Indicator: "Unreproducible results — missing code, data, or seeds", Severity: SeverityP1, FixApproach: "Request artifacts. If unavailable, treat claims as INFERRED not VERIFIED."},
		{Indicator: "Overclaiming — conclusions exceed what the evidence supports", Severity: SeverityP2, FixApproach: "Separate what the experiment proved from what the authors concluded. Check scope of evaluation."},
	},
	Angles: []DomainAngle{
		{Label: "replication", Description: "Can you reproduce the key result? What would it take? What's missing?", ThinkPattern: "scientific_method", ThinkParams: map[string]string{"observation": "{topic}"}},
		{Label: "systematic_review", Description: "What does the broader literature say? Is this an outlier or consensus?", ThinkPattern: "structured_argumentation", ThinkParams: map[string]string{"claim": "{topic}", "context": "related literature"}},
		{Label: "practical_impact", Description: "Does this work in production? At scale? With real users? Or only in the lab?", ThinkPattern: "decision_framework", ThinkParams: map[string]string{"decision": "{topic}", "options": "adopt, wait, reject"}},
	},
	AntiPatterns: []string{
		"Absence of evidence is not evidence of absence",
		"If you can't reproduce it, you don't understand it",
	},
}

// domainRegistry maps domain names to algorithms.
var domainRegistry = map[string]*DomainAlgorithm{
	"generic":      &GenericDomain,
	"debugging":    &DebuggingDomain,
	"security":     &SecurityDomain,
	"performance":  &PerformanceDomain,
	"architecture": &ArchitectureDomain,
	"research":     &ResearchDomain,
}

// GetDomain returns a deep copy of the domain algorithm for the given name.
// Returns a copy of GenericDomain for empty name or "generic".
// Returns nil for unknown domains.
// Deep copy is required to prevent callers from mutating the package-level vars.
func GetDomain(name string) *DomainAlgorithm {
	if name == "" {
		return copyDomain(&GenericDomain)
	}
	d := domainRegistry[name]
	if d == nil {
		return nil
	}
	return copyDomain(d)
}

// copyDomain returns a shallow-field copy of d with all slice and map fields
// deep-copied so callers cannot mutate the package-level domain variables.
func copyDomain(d *DomainAlgorithm) *DomainAlgorithm {
	out := *d // copy scalar fields

	out.CoverageAreas = append([]string(nil), d.CoverageAreas...)
	out.AntiPatterns = append([]string(nil), d.AntiPatterns...)

	out.Methods = make(map[string]string, len(d.Methods))
	for k, v := range d.Methods {
		out.Methods[k] = v
	}

	out.Patterns = make([]PatternEntry, len(d.Patterns))
	copy(out.Patterns, d.Patterns)

	out.Angles = make([]DomainAngle, len(d.Angles))
	for i, a := range d.Angles {
		ac := a
		ac.ThinkParams = make(map[string]string, len(a.ThinkParams))
		for k, v := range a.ThinkParams {
			ac.ThinkParams[k] = v
		}
		out.Angles[i] = ac
	}

	return &out
}

// DomainNames returns all registered domain names.
func DomainNames() []string {
	names := make([]string, 0, len(domainRegistry))
	for k := range domainRegistry {
		names = append(names, k)
	}
	return names
}

// AutoDetectDomain scans a lowercased topic for keyword patterns and returns the
// best-matching domain name. Priority order: security > performance > architecture >
// debugging > research > generic (default).
func AutoDetectDomain(topic string) string {
	lower := strings.ToLower(topic)

	securityKeywords := []string{"security", "auth", "injection", "xss", "owasp", "cve", "vulnerability", "exploit", "csrf", "secrets"}
	for _, kw := range securityKeywords {
		if strings.Contains(lower, kw) {
			return "security"
		}
	}

	performanceKeywords := []string{"slow", "latency", "memory", "cpu", "bottleneck", "performance", "leak", "allocation", "throughput", "benchmark"}
	for _, kw := range performanceKeywords {
		if strings.Contains(lower, kw) {
			return "performance"
		}
	}

	architectureKeywords := []string{"architecture", "coupling", "module", "dependency", "design", "abstraction", "layer", "boundary", "refactor"}
	for _, kw := range architectureKeywords {
		if strings.Contains(lower, kw) {
			return "architecture"
		}
	}

	debuggingKeywords := []string{"bug", "crash", "error", "fail", "panic", "nil", "race", "deadlock", "timeout", "exception"}
	for _, kw := range debuggingKeywords {
		if strings.Contains(lower, kw) {
			return "debugging"
		}
	}

	researchKeywords := []string{"research", "paper", "literature", "survey", "compare", "benchmark", "alternative", "evaluate"}
	for _, kw := range researchKeywords {
		if strings.Contains(lower, kw) {
			return "research"
		}
	}

	return "generic"
}
