package patterns

import (
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

var validReviewCategories = map[string]bool{
	"novelty": true, "empirical_rigor": true, "reproducibility": true,
	"baselines": true, "clarity": true, "significance": true,
}

type peerReviewPattern struct{}

// NewPeerReviewPattern returns the "peer_review" pattern handler.
func NewPeerReviewPattern() think.PatternHandler { return &peerReviewPattern{} }

func (p *peerReviewPattern) Name() string { return "peer_review" }

func (p *peerReviewPattern) Description() string {
	return "Simulate peer review with objections, severity ratings, and revision plan"
}

func (p *peerReviewPattern) Validate(input map[string]any) (map[string]any, error) {
	artifactRaw, ok := input["artifact"]
	if !ok {
		return nil, fmt.Errorf("missing required field: artifact")
	}
	artifact, ok := artifactRaw.(string)
	if !ok || artifact == "" {
		return nil, fmt.Errorf("field 'artifact' must be a non-empty string")
	}

	out := map[string]any{"artifact": artifact}

	if v, ok := input["claims"].([]any); ok {
		out["claims"] = v
	}
	if v, ok := input["methodology"].(string); ok {
		out["methodology"] = v
	}
	if v, ok := input["novelty"].(string); ok {
		out["novelty"] = v
	}

	return out, nil
}

func (p *peerReviewPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	artifact := validInput["artifact"].(string)
	claims, _ := validInput["claims"].([]any)
	methodology, _ := validInput["methodology"].(string)
	novelty, _ := validInput["novelty"].(string)

	objections := make([]map[string]any, 0)
	strengths := make([]string, 0)
	revisionPlan := make([]string, 0)

	// Evaluate methodology.
	if methodology == "" {
		objections = append(objections, map[string]any{
			"objection": "Methodology not described — reproducibility cannot be assessed",
			"severity":  "P1",
			"category":  "empirical_rigor",
		})
		revisionPlan = append(revisionPlan, "Provide detailed methodology section")
	} else {
		strengths = append(strengths, fmt.Sprintf("Methodology described: %s", methodology))
	}

	// Evaluate novelty.
	if novelty == "" {
		objections = append(objections, map[string]any{
			"objection": "Novelty claim not stated — significance is unclear",
			"severity":  "P2",
			"category":  "novelty",
		})
		revisionPlan = append(revisionPlan, "Clarify novel contribution relative to prior work")
	} else {
		strengths = append(strengths, fmt.Sprintf("Novelty stated: %s", novelty))
	}

	// Evaluate claims.
	if len(claims) == 0 {
		objections = append(objections, map[string]any{
			"objection": "No explicit claims provided — evaluation is based on artifact description only",
			"severity":  "P2",
			"category":  "clarity",
		})
		revisionPlan = append(revisionPlan, "Enumerate specific claims with supporting evidence")
	} else {
		strengths = append(strengths, fmt.Sprintf("%d claim(s) submitted for evaluation", len(claims)))
		for i, c := range claims {
			claimStr, ok := c.(string)
			if !ok {
				if cm, ok := c.(map[string]any); ok {
					claimStr, _ = cm["text"].(string)
				}
			}
			if claimStr == "" {
				objections = append(objections, map[string]any{
					"objection": fmt.Sprintf("Claim %d is empty or unreadable", i+1),
					"severity":  "P2",
					"category":  "clarity",
				})
				revisionPlan = append(revisionPlan, fmt.Sprintf("Rewrite claim %d with precise language", i+1))
			}
		}
	}

	// Check baselines.
	objections = append(objections, map[string]any{
		"objection": "Baseline comparisons not provided — relative performance cannot be judged",
		"severity":  "P2",
		"category":  "baselines",
	})
	revisionPlan = append(revisionPlan, "Add comparison against established baselines")

	// Determine verdict from severity distribution.
	verdict := computeVerdict(objections)

	_ = artifact // used in return data

	data := map[string]any{
		"artifact":      artifact,
		"reviewVerdict": verdict,
		"objections":    objections,
		"revisionPlan":  revisionPlan,
		"strengths":     strengths,
		"guidance":      BuildGuidance("peer_review", "full", []string{"claims", "methodology", "novelty"}),
	}

	// Tier 2A: text analysis
	primaryText := validInput["artifact"].(string)
	if analysis := AnalyzeText(primaryText); analysis != nil {
		domain := MatchDomainTemplate(primaryText)
		if domain != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domain)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("peer_review", data, sessionID, nil, "", nil), nil
}

// computeVerdict derives the overall verdict from the objection severity set.
func computeVerdict(objections []map[string]any) string {
	p0, p1 := 0, 0
	for _, o := range objections {
		switch o["severity"] {
		case "P0":
			p0++
		case "P1":
			p1++
		}
	}
	switch {
	case p0 > 0:
		return "reject"
	case p1 > 1:
		return "major_revision"
	case p1 == 1 || len(objections) > 2:
		return "minor_revision"
	default:
		return "accept"
	}
}
