package patterns

import "strings"

// DomainTemplate provides pre-built decomposition for known problem domains.
type DomainTemplate struct {
	Name             string
	Keywords         []string            // trigger keywords (lowercase)
	SubProblems      []string            // for problem_decomposition
	Entities         []string            // for domain_modeling
	Components       []string            // for architecture_analysis
	Criteria         []string            // for decision_framework
	Dependencies     []map[string]string // [{from, to}] for DAG analysis
	ExpectedEntities []string            // for gap detection in text analysis
}

// Guidance provides progressive enrichment instructions in every pattern response.
type Guidance struct {
	CurrentDepth string   `json:"current_depth"` // "basic", "enriched", "full"
	NextLevel    string   `json:"next_level"`    // what providing more data unlocks
	Example      string   `json:"example"`       // copy-pasteable enriched call
	Enrichments  []string `json:"enrichments"`   // available optional fields
}

// domainTemplates is the registry of pre-built domain templates.
var domainTemplates = []DomainTemplate{
	{
		Name:        "auth",
		Keywords:    []string{"auth", "login", "authentication", "oauth", "jwt", "session"},
		SubProblems: []string{"user-registration", "login-flow", "token-management", "session-handling", "role-based-access-control"},
		Dependencies: []map[string]string{
			{"from": "login-flow", "to": "user-registration"},
			{"from": "token-management", "to": "login-flow"},
			{"from": "session-handling", "to": "token-management"},
			{"from": "role-based-access-control", "to": "token-management"},
		},
		ExpectedEntities: []string{"registration", "login", "tokens", "sessions", "logout", "roles", "password-reset", "mfa"},
	},
	{
		Name:             "api",
		Keywords:         []string{"api", "endpoint", "rest", "graphql", "route"},
		SubProblems:      []string{"schema-design", "routing", "request-validation", "error-handling", "authentication-middleware", "rate-limiting", "documentation"},
		ExpectedEntities: []string{"routing", "validation", "authentication", "rate-limiting", "error-handling", "documentation", "versioning"},
	},
	{
		Name:             "database",
		Keywords:         []string{"database", "schema", "migration", "sql", "postgres", "mongo", "table"},
		SubProblems:      []string{"entity-modeling", "relationships", "indexes", "migrations", "seeding", "backup-strategy"},
		Entities:         []string{"Table", "Column", "Index", "Migration", "Constraint", "Relationship"},
		ExpectedEntities: []string{"entities", "relationships", "indexes", "migrations", "constraints", "normalization", "backup"},
	},
	{
		Name:             "deploy",
		Keywords:         []string{"deploy", "ci", "cd", "docker", "kubernetes", "infrastructure"},
		SubProblems:      []string{"build-pipeline", "containerization", "orchestration", "monitoring", "rollback-strategy", "secret-management"},
		ExpectedEntities: []string{"pipeline", "container", "orchestration", "monitoring", "rollback", "secrets", "ssl"},
	},
	{
		Name:             "test",
		Keywords:         []string{"test", "coverage", "quality", "qa", "unit", "integration", "e2e"},
		SubProblems:      []string{"unit-test-strategy", "integration-tests", "e2e-tests", "coverage-gates", "test-fixtures", "ci-integration"},
		ExpectedEntities: []string{"unit", "integration", "e2e", "coverage", "fixtures", "mocking", "ci"},
	},
	{
		Name:             "frontend",
		Keywords:         []string{"frontend", "ui", "component", "react", "vue", "css", "layout"},
		SubProblems:      []string{"component-architecture", "state-management", "routing", "styling-system", "accessibility", "responsive-design"},
		Components:       []string{"App", "Router", "StateStore", "UIComponents", "ThemeProvider"},
		ExpectedEntities: []string{"components", "state", "routing", "styling", "accessibility", "responsive", "performance"},
	},
	{
		Name:             "backend",
		Keywords:         []string{"backend", "server", "service", "handler", "middleware"},
		SubProblems:      []string{"service-layer", "data-access", "middleware-chain", "error-handling", "logging", "configuration"},
		Components:       []string{"Server", "Router", "Middleware", "ServiceLayer", "DataAccessLayer", "Logger"},
		ExpectedEntities: []string{"services", "handlers", "middleware", "logging", "configuration", "validation", "caching"},
	},
	{
		Name:             "security",
		Keywords:         []string{"security", "vulnerability", "owasp", "audit", "secrets", "encryption"},
		SubProblems:      []string{"input-validation", "output-encoding", "authentication", "authorization", "secrets-management", "dependency-audit", "logging-audit"},
		Criteria:         []string{"severity", "exploitability", "data-exposure-risk", "remediation-effort"},
		ExpectedEntities: []string{"input-validation", "authentication", "authorization", "encryption", "secrets", "audit", "dependencies"},
	},
	{
		Name:             "monitoring",
		Keywords:         []string{"monitoring", "observability", "metrics", "logging", "alerting", "tracing"},
		SubProblems:      []string{"metrics-collection", "log-aggregation", "distributed-tracing", "alerting-rules", "dashboard-design", "SLO-definition"},
		Components:       []string{"MetricsCollector", "LogAggregator", "Tracer", "AlertManager", "Dashboard"},
		ExpectedEntities: []string{"metrics", "logs", "traces", "alerts", "dashboards", "slo", "uptime"},
	},
	{
		Name:             "data-pipeline",
		Keywords:         []string{"pipeline", "etl", "data", "stream", "batch", "transform"},
		SubProblems:      []string{"ingestion", "transformation", "validation", "loading", "scheduling", "error-recovery"},
		Components:       []string{"Ingester", "Transformer", "Validator", "Loader", "Scheduler", "DeadLetterQueue"},
		ExpectedEntities: []string{"ingestion", "transformation", "validation", "loading", "scheduling", "retry", "dead-letter"},
	},
}

// MatchDomainTemplate returns the DomainTemplate whose keywords best match the
// provided text. It lowercases the text, splits into words, counts keyword hits
// per template, and returns the template with the highest count (minimum 1).
// Returns nil if no template has at least one keyword match.
func MatchDomainTemplate(text string) *DomainTemplate {
	lower := strings.ToLower(text)
	words := strings.Fields(lower)

	wordSet := make(map[string]struct{}, len(words))
	for _, w := range words {
		// Strip common punctuation attached to words.
		w = strings.Trim(w, ".,;:!?\"'()")
		wordSet[w] = struct{}{}
	}

	bestIdx := -1
	bestCount := 0

	for i, tmpl := range domainTemplates {
		count := 0
		for _, kw := range tmpl.Keywords {
			if _, ok := wordSet[kw]; ok {
				count++
			}
		}
		if count > bestCount {
			bestCount = count
			bestIdx = i
		}
	}

	if bestCount == 0 {
		return nil
	}
	return &domainTemplates[bestIdx]
}
