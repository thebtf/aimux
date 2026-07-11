package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const DefaultGateTimeoutSeconds = 300

// DecisionValue is the external ALLOW/BLOCK result emitted by the review gate.
type DecisionValue string

const (
	DecisionAllow DecisionValue = "allow"
	DecisionBlock DecisionValue = "block"
)

// Decision is the synchronous review gate outcome.
type Decision struct {
	Decision        DecisionValue `json:"decision"`
	Reason          string        `json:"reason"`
	Findings        []Finding     `json:"findings"`
	Summary         string        `json:"summary"`
	PassesCompleted []PassName    `json:"passes_completed"`
	Severity        Severity      `json:"severity,omitempty"`
	Blocking        bool          `json:"blocking"`
	ReviewComplete  bool          `json:"review_complete"`
	ConfidenceScore float64       `json:"confidence_score"`
}

// PassRunner is the gate-facing subset of the review pass pipeline.
type PassRunner interface {
	Run(ctx context.Context, target string, criteria Criteria) ([]PassResult, error)
}

// Gate runs the full review pipeline and maps aggregated findings to ALLOW/BLOCK.
type Gate struct {
	runner     PassRunner
	criteria   Criteria
	aggregator Aggregator
}

// NewGate constructs a fail-closed review gate.
func NewGate(runner PassRunner, criteria Criteria) *Gate {
	return &Gate{
		runner:     runner,
		criteria:   criteria,
		aggregator: Aggregator{},
	}
}

// RunGate runs review passes synchronously and only allows after every required
// pass completes successfully.
func (g *Gate) RunGate(ctx context.Context, target string, timeoutSec int) (Decision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := gateTimeout(timeoutSec)
	gateCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if g == nil || g.runner == nil {
		return failClosedDecision("review gate runner is required", Aggregator{}.Aggregate(nil)), nil
	}

	criteria := g.criteria
	if criteria.TaskTimeout <= 0 {
		criteria.TaskTimeout = timeout
	}
	results, err := g.runner.Run(gateCtx, target, criteria)
	aggregated := g.aggregator.Aggregate(results)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(gateCtx.Err(), context.DeadlineExceeded) {
			return failClosedDecision("timeout", aggregated), nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(gateCtx.Err(), context.Canceled) {
			return Decision{}, context.Canceled
		}
		return failClosedDecision(err.Error(), aggregated), nil
	}

	if !completeReviewPassSet(aggregated.PassesCompleted) {
		return failClosedDecision(incompleteReviewPassReason(aggregated.PassesCompleted), aggregated), nil
	}
	if aggregated.Blocking {
		return blockDecision(aggregated), nil
	}
	return allowDecision(aggregated), nil
}

func gateTimeout(timeoutSec int) time.Duration {
	if timeoutSec <= 0 {
		timeoutSec = DefaultGateTimeoutSeconds
	}
	return time.Duration(timeoutSec) * time.Second
}

func allowDecision(aggregated AggregatedFindings) Decision {
	reason := strings.TrimSpace(aggregated.Summary)
	if reason == "" {
		reason = "no blocking findings"
	}
	reason = SanitizePublicReason(reason)
	return Decision{
		Decision:        DecisionAllow,
		Reason:          reason,
		Findings:        aggregated.Findings,
		Summary:         aggregated.Summary,
		PassesCompleted: aggregated.PassesCompleted,
		Severity:        aggregated.Severity,
		Blocking:        false,
		ReviewComplete:  true,
		ConfidenceScore: 1,
	}
}

func blockDecision(aggregated AggregatedFindings) Decision {
	return Decision{
		Decision:        DecisionBlock,
		Reason:          SanitizePublicReason(topErrorSummary(aggregated.Findings)),
		Findings:        aggregated.Findings,
		Summary:         aggregated.Summary,
		PassesCompleted: aggregated.PassesCompleted,
		Severity:        aggregated.Severity,
		Blocking:        true,
		ReviewComplete:  true,
		ConfidenceScore: 1,
	}
}

func failClosedDecision(reason string, aggregated AggregatedFindings) Decision {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "review gate incomplete"
	}
	reason = SanitizePublicReason(reason)
	summary := strings.TrimSpace(aggregated.Summary)
	if len(aggregated.PassesCompleted) == 0 || summary == "" || summary == "No review findings." {
		summary = reason
	}
	return Decision{
		Decision:        DecisionBlock,
		Reason:          reason,
		Findings:        aggregated.Findings,
		Summary:         summary,
		PassesCompleted: aggregated.PassesCompleted,
		Severity:        aggregated.Severity,
		Blocking:        true,
		ReviewComplete:  false,
		ConfidenceScore: 0,
	}
}

func completeReviewPassSet(passes []PassName) bool {
	expected := orderedPasses()
	if len(passes) != len(expected) {
		return false
	}
	completed := make(map[PassName]struct{}, len(passes))
	for _, pass := range passes {
		completed[pass] = struct{}{}
	}
	for _, pass := range expected {
		if _, ok := completed[pass]; !ok {
			return false
		}
	}
	return true
}

func incompleteReviewPassReason(passes []PassName) string {
	if len(passes) == 0 {
		return "review gate incomplete: zero passes completed"
	}
	completed := make(map[PassName]struct{}, len(passes))
	for _, pass := range passes {
		completed[pass] = struct{}{}
	}
	missing := make([]string, 0, len(orderedPasses()))
	for _, pass := range orderedPasses() {
		if _, ok := completed[pass]; !ok {
			missing = append(missing, string(pass))
		}
	}
	if len(missing) == 0 {
		return "review gate incomplete: unexpected pass set"
	}
	return "review gate incomplete: missing passes: " + strings.Join(missing, ", ")
}

func topErrorSummary(findings []Finding) string {
	summaries := make([]string, 0, 3)
	for _, finding := range findings {
		if finding.Severity != SeverityError {
			continue
		}
		summaries = append(summaries, formatFindingSummary(finding))
		if len(summaries) == 3 {
			break
		}
	}
	if len(summaries) == 0 {
		return "blocking review finding"
	}
	return strings.Join(summaries, "; ")
}

func formatFindingSummary(finding Finding) string {
	body := strings.TrimSpace(finding.Body)
	location := strings.TrimSpace(finding.File)
	if finding.Line != nil {
		if location != "" {
			location = fmt.Sprintf("%s:%d", location, *finding.Line)
		} else {
			location = fmt.Sprintf("line %d", *finding.Line)
		}
	}
	if location == "" {
		return body
	}
	if body == "" {
		return location
	}
	return fmt.Sprintf("%s: %s", location, body)
}
