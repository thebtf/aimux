// Package investigate implements iterative convergent investigation with
// domain specialization. Ported from mcp-aimux v2 TypeScript with enhancements:
// 5-level confidence classification, gap tracking, adversarial validation.
package investigate

import "time"

// Severity classifies finding impact.
type Severity string

const (
	SeverityP0 Severity = "P0"
	SeverityP1 Severity = "P1"
	SeverityP2 Severity = "P2"
	SeverityP3 Severity = "P3"
)

// Confidence classifies finding evidence level (NEW in v3).
type Confidence string

const (
	ConfidenceVerified Confidence = "VERIFIED"
	ConfidenceInferred Confidence = "INFERRED"
	ConfidenceStale    Confidence = "STALE"
	ConfidenceBlocked  Confidence = "BLOCKED"
	ConfidenceUnknown  Confidence = "UNKNOWN"
)

// Finding represents a single discovery during investigation.
type Finding struct {
	ID           string     `json:"id"`
	Severity     Severity   `json:"severity"`
	Confidence   Confidence `json:"confidence"`
	Description  string     `json:"description"`
	Source       string     `json:"source"`
	Iteration    int        `json:"iteration"`
	CoverageArea string     `json:"coverage_area,omitempty"`
	CorrectedBy  string     `json:"corrected_by,omitempty"`
}

// Correction records when a later finding supersedes an earlier one.
type Correction struct {
	OriginalID     string `json:"original_id"`
	OriginalClaim  string `json:"original_claim"`
	CorrectedClaim string `json:"corrected_claim"`
	Evidence       string `json:"evidence"`
	Iteration      int    `json:"iteration"`
}

// FindingInput is the input for adding a new finding.
type FindingInput struct {
	Description  string     `json:"description"`
	Severity     Severity   `json:"severity"`
	Source       string     `json:"source"`
	Confidence   Confidence `json:"confidence,omitempty"`
	CoverageArea string     `json:"coverage_area,omitempty"`
	Corrects     string     `json:"corrects,omitempty"`
}

// InvestigationState holds all state for an active investigation.
type InvestigationState struct {
	Topic              string          `json:"topic"`
	Domain             string          `json:"domain,omitempty"`
	Iteration          int             `json:"iteration"`
	Findings           []Finding       `json:"findings"`
	Corrections        []Correction    `json:"corrections"`
	CoverageAreas      []string        `json:"coverage_areas"`
	CoverageChecked    map[string]bool `json:"coverage_checked"`
	ConvergenceHistory []float64       `json:"convergence_history"`
	CreatedAt          time.Time       `json:"created_at"`
	LastActivityAt     time.Time       `json:"last_activity_at"`
}

// InvestigationSummary is a lightweight view for listing investigations.
type InvestigationSummary struct {
	SessionID     string `json:"session_id"`
	Topic         string `json:"topic"`
	Iteration     int    `json:"iteration"`
	FindingsCount int    `json:"findings_count"`
}

// AssessResult contains convergence assessment output.
type AssessResult struct {
	Iteration           int            `json:"iteration"`
	ConvergenceScore    float64        `json:"convergence_score"`
	CoverageScore       float64        `json:"coverage_score"`
	FindingsCount       int            `json:"findings_count"`
	CorrectionsCount    int            `json:"corrections_count"`
	Recommendation      string         `json:"recommendation"`
	UncheckedAreas      []string       `json:"unchecked_areas"`
	WeakAreas           []string       `json:"weak_areas"`
	ConflictingAreas    []string       `json:"conflicting_areas"`
	PriorityNext        string         `json:"priority_next,omitempty"`
	SuggestedAngle      string         `json:"suggested_angle,omitempty"`
	SuggestedThinkCall  string         `json:"suggested_think_call,omitempty"`
	AntiPatternWarnings []string       `json:"anti_pattern_warnings,omitempty"`
	PatternHints        []PatternEntry `json:"pattern_hints,omitempty"`
	AdversarialPrompt   string         `json:"adversarial_prompt,omitempty"`
	Message             string         `json:"message"`
}

// PatternEntry describes what to look for during investigation.
type PatternEntry struct {
	Indicator   string   `json:"indicator"`
	Severity    Severity `json:"severity"`
	FixApproach string   `json:"fix_approach"`
}

// DomainAngle is a perspective for analyzing the topic with a matching think pattern.
type DomainAngle struct {
	Label        string            `json:"label"`
	Description  string            `json:"description"`
	ThinkPattern string            `json:"think_pattern"`
	ThinkParams  map[string]string `json:"think_params"`
}

// DomainAlgorithm defines a complete investigation strategy for a domain.
type DomainAlgorithm struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	CoverageAreas []string          `json:"coverage_areas"`
	Methods       map[string]string `json:"methods"`
	Patterns      []PatternEntry    `json:"patterns"`
	AntiPatterns  []string          `json:"anti_patterns"`
	Angles        []DomainAngle     `json:"angles,omitempty"`
}
