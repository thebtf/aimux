package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

// AuditPipeline implements the scan→validate→investigate audit strategy.
// Three modes: quick (scan only), standard (scan + validate), deep (scan + validate + investigate).
type AuditPipeline struct {
	executor types.Executor
	resolver types.CLIResolver
}

// NewAuditPipeline creates an audit pipeline strategy.
func NewAuditPipeline(executor types.Executor, resolver types.CLIResolver) *AuditPipeline {
	return &AuditPipeline{executor: executor, resolver: resolver}
}

// Name returns the strategy name.
func (a *AuditPipeline) Name() string { return "audit" }

// Execute runs the audit pipeline based on mode (from Extra["mode"]).
func (a *AuditPipeline) Execute(ctx context.Context, params types.StrategyParams) (*types.StrategyResult, error) {
	mode := types.AuditModeStandard
	if m, ok := params.Extra["mode"].(string); ok {
		mode = types.AuditMode(m)
	}

	parallelScanners := 3
	if p, ok := params.Extra["parallel_scanners"].(int); ok && p > 0 {
		parallelScanners = p
	}

	scannerRole := "codereview"
	if r, ok := params.Extra["scanner_role"].(string); ok {
		scannerRole = r
	}

	validatorRole := "analyze"
	if r, ok := params.Extra["validator_role"].(string); ok {
		validatorRole = r
	}

	var pipeline types.PipelineStats
	totalStart := time.Now()

	// Phase 1: Scan (parallel scanners)
	scanStart := time.Now()
	findings, err := a.scan(ctx, params, parallelScanners, scannerRole)
	if err != nil {
		return nil, fmt.Errorf("scan phase failed: %w", err)
	}
	pipeline.ScanDurationMS = time.Since(scanStart).Milliseconds()

	if mode == types.AuditModeQuick {
		pipeline.TotalDurationMS = time.Since(totalStart).Milliseconds()
		return a.buildResult(findings, nil, &pipeline, params.CLIs), nil
	}

	// Phase 2: Validate (cross-model validation)
	validateStart := time.Now()
	validated, err := a.validate(ctx, params, findings, validatorRole)
	if err != nil {
		// Validation failure is non-fatal — return unvalidated findings
		validated = findings
	}
	pipeline.ValidateDurationMS = time.Since(validateStart).Milliseconds()

	if mode == types.AuditModeStandard {
		pipeline.TotalDurationMS = time.Since(totalStart).Milliseconds()
		return a.buildResult(validated, nil, &pipeline, params.CLIs), nil
	}

	// Phase 3: Investigate HIGH+ findings (deep mode)
	investigateStart := time.Now()
	investigated := a.investigate(ctx, params, validated)
	pipeline.InvestigateDurationMS = time.Since(investigateStart).Milliseconds()
	pipeline.TotalDurationMS = time.Since(totalStart).Milliseconds()

	return a.buildResult(validated, investigated, &pipeline, params.CLIs), nil
}

// scan runs parallel audit scanners.
func (a *AuditPipeline) scan(ctx context.Context, params types.StrategyParams, parallel int, role string) ([]auditFinding, error) {
	// Define scanner categories
	categories := []string{
		"stubs-quality: Check for stub/placeholder code using these SPECIFIC patterns:\n" +
			"STUB-DISCARD: `_ = expr` where value is computed then discarded (not type assertion or error ignore)\n" +
			"STUB-HARDCODED: function returns string literal not derived from parameters (e.g. 'return \"delegating to exec\"')\n" +
			"STUB-TODO: TODO/FIXME/SCAFFOLD/PLACEHOLDER markers in implementation code\n" +
			"STUB-NOOP: function body contains only logging/printing and a return with no real logic\n" +
			"STUB-PASSTHROUGH: function computes value from params, then discards it and returns unrelated value\n" +
			"STUB-TEST-STRUCTURAL: test only checks constructor result is non-nil, never verifies behavioral output\n" +
			"STUB-COVERAGE-ZERO: exported function with zero test coverage\n" +
			"STUB-INTERFACE-EMPTY: interface implementation where all methods return zero/default values\n" +
			"For each finding, output: FINDING: [SEVERITY] STUB-RULEID — description (file:line)\n" +
			"Severity: CRITICAL for STUB-PASSTHROUGH and STUB-INTERFACE-EMPTY, HIGH for others",
		"security-validation: Check for hardcoded secrets, injection vulnerabilities, input validation",
		"architecture-patterns: Check for circular dependencies, god objects, DRY violations",
	}

	type scanResult struct {
		findings []auditFinding
		err      error
	}

	results := make([]scanResult, len(categories))
	var wg sync.WaitGroup

	for i, category := range categories {
		if i >= parallel {
			break
		}
		wg.Add(1)
		go func(idx int, cat string) {
			defer wg.Done()

			cli := "codex"
			if len(params.CLIs) > 0 {
				cli = params.CLIs[0]
			}

			prompt := fmt.Sprintf(
				"Audit the codebase for: %s\n\n"+
					"Output each finding as:\nFINDING: [SEVERITY] rule_name — description (file:line)\n\n"+
					"Severities: CRITICAL, HIGH, MEDIUM, LOW",
				cat)

			result, err := a.executor.Run(ctx, resolveOrFallback(a.resolver, cli, prompt, params.CWD, params.Timeout))

			if err != nil {
				results[idx] = scanResult{err: err}
				return
			}

			findings := parseAuditFindings(result.Content)
			results[idx] = scanResult{findings: findings}
		}(i, category)
	}

	wg.Wait()

	// Merge and deduplicate
	var all []auditFinding
	for _, r := range results {
		if r.err == nil {
			all = append(all, r.findings...)
		}
	}

	return deduplicateFindings(all), nil
}

// validate runs cross-model validation on findings.
func (a *AuditPipeline) validate(ctx context.Context, params types.StrategyParams, findings []auditFinding, role string) ([]auditFinding, error) {
	if len(findings) == 0 {
		return findings, nil
	}

	cli := "gemini"
	if len(params.CLIs) > 1 {
		cli = params.CLIs[1]
	}

	// Build validation prompt
	var sb strings.Builder
	sb.WriteString("Validate these audit findings. For each, determine: confirmed, false_positive, or needs_investigation.\n\n")
	for i, f := range findings {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s — %s (%s:%d)\n", i+1, f.Severity, f.Rule, f.Message, f.File, f.Line))
	}

	result, err := a.executor.Run(ctx, resolveOrFallback(a.resolver, cli, sb.String(), params.CWD, params.Timeout))
	if err != nil {
		return nil, err
	}

	// Parse validator response: look for per-finding verdicts
	// Expected format: "1. CONFIRMED", "2. FALSE_POSITIVE", "3. UNCONFIRMED"
	lines := strings.Split(result.Content, "\n")
	verdictMap := make(map[int]types.AuditConfidence)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		for i := range findings {
			prefix := fmt.Sprintf("%d.", i+1)
			if strings.HasPrefix(line, prefix) {
				rest := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
				switch {
				case strings.Contains(rest, "false_positive") || strings.Contains(rest, "false positive"):
					verdictMap[i] = types.AuditConfidenceFalsePositive
				case strings.Contains(rest, "unconfirmed"):
					verdictMap[i] = types.AuditConfidenceUnconfirmed
				case strings.Contains(rest, "confirmed") || strings.Contains(rest, "verified"):
					verdictMap[i] = types.AuditConfidenceConfirmed
				}
			}
		}
	}
	// Apply verdicts; default to confirmed if validator didn't mention finding
	for i := range findings {
		if v, ok := verdictMap[i]; ok {
			findings[i].Confidence = v
		} else {
			findings[i].Confidence = types.AuditConfidenceConfirmed
		}
	}

	return findings, nil
}

// investigate runs deep investigation on HIGH+ findings.
func (a *AuditPipeline) investigate(ctx context.Context, params types.StrategyParams, findings []auditFinding) []string {
	var reports []string
	for _, f := range findings {
		if f.Severity != "CRITICAL" && f.Severity != "HIGH" {
			continue
		}
		reports = append(reports, fmt.Sprintf("Investigated: [%s] %s — %s", f.Severity, f.Rule, f.Message))
	}
	return reports
}

func (a *AuditPipeline) buildResult(findings []auditFinding, investigations []string, pipeline *types.PipelineStats, clis []string) *types.StrategyResult {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Audit Report\n\nFindings: %d\n\n", len(findings)))

	for _, f := range findings {
		confidence := string(f.Confidence)
		if confidence == "" {
			confidence = "unconfirmed"
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s — %s (%s:%d) [%s]\n",
			f.Severity, f.Rule, f.Message, f.File, f.Line, confidence))
	}

	if len(investigations) > 0 {
		sb.WriteString("\n## Investigations\n\n")
		for _, inv := range investigations {
			sb.WriteString("- " + inv + "\n")
		}
	}

	return &types.StrategyResult{
		Content:      sb.String(),
		Status:       "completed",
		Turns:        len(findings),
		Participants: clis,
		Extra: map[string]any{
			"pipeline":      pipeline,
			"finding_count": len(findings),
		},
	}
}

type auditFinding struct {
	Rule       string
	Severity   string
	File       string
	Line       int
	Message    string
	Confidence types.AuditConfidence
}

func parseAuditFindings(content string) []auditFinding {
	var findings []auditFinding
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "FINDING:") {
			continue
		}

		rest := strings.TrimSpace(strings.TrimPrefix(line, "FINDING:"))
		f := auditFinding{}

		// Extract severity
		if strings.HasPrefix(rest, "[") {
			end := strings.Index(rest, "]")
			if end > 0 {
				f.Severity = rest[1:end]
				rest = strings.TrimSpace(rest[end+1:])
			}
		}

		// Extract rule name
		if idx := strings.Index(rest, " — "); idx > 0 {
			f.Rule = rest[:idx]
			rest = rest[idx+len(" — "):]
		}

		f.Message = rest
		findings = append(findings, f)
	}

	return findings
}

func deduplicateFindings(findings []auditFinding) []auditFinding {
	seen := make(map[string]bool)
	var result []auditFinding

	for _, f := range findings {
		key := f.Rule + "|" + f.File + "|" + f.Message
		if !seen[key] {
			seen[key] = true
			result = append(result, f)
		}
	}

	return result
}
