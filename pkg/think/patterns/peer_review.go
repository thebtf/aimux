package patterns

import (
	"context"
	"encoding/json"
	"fmt"

	think "github.com/thebtf/aimux/pkg/think"
)

type peerReviewPattern struct {
	sampling think.SamplingProvider
}

// NewPeerReviewPattern returns the "peer_review" pattern handler.
func NewPeerReviewPattern() think.PatternHandler { return &peerReviewPattern{} }

// SetSampling injects the sampling provider. Implements think.SamplingAwareHandler.
func (p *peerReviewPattern) SetSampling(provider think.SamplingProvider) {
	p.sampling = provider
}

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

func (p *peerReviewPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"artifact": {Type: "string", Required: true, Description: "The artifact to peer review"},
		"claims": {
			Type:        "array",
			Required:    false,
			Description: "List of claims to evaluate as strings or objects with text",
			Items: map[string]any{
				"oneOf": []map[string]any{
					{"type": "string"},
					{
						"type": "object",
						"properties": map[string]any{
							"text": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
		"methodology": {Type: "string", Required: false, Description: "Methodology description"},
		"novelty":     {Type: "string", Required: false, Description: "Novelty claim description"},
	}
}

func (p *peerReviewPattern) Category() string { return "solo" }

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

	// Tier 1.5: sampling-enhanced review — merge LLM-detected objections when artifact is substantial.
	samplingSource := ""
	if p.sampling != nil && len(artifact) > 100 {
		if samplingObjections, samplingStrengths, err := p.requestSamplingReview(artifact); err == nil {
			for _, obj := range samplingObjections {
				objections = append(objections, obj)
			}
			strengths = append(strengths, samplingStrengths...)
			samplingSource = "sampling"
		}
		// On error: fall through — existing keyword-based review stands.
	}

	// Determine verdict from severity distribution.
	verdict := computeVerdict(objections)

	reviewSource := "keyword-analysis"
	if samplingSource != "" {
		reviewSource = samplingSource
	}

	data := map[string]any{
		"artifact":      artifact,
		"reviewVerdict": verdict,
		"objections":    objections,
		"revisionPlan":  revisionPlan,
		"strengths":     strengths,
		"reviewSource":  reviewSource,
		"guidance":      BuildGuidance("peer_review", "full", []string{"claims", "methodology", "novelty"}),
	}

	// Tier 2A: text analysis
	if analysis := AnalyzeText(artifact); analysis != nil {
		domain := MatchDomainTemplate(artifact)
		if domain != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domain)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("peer_review", data, sessionID, nil, "", nil), nil
}

// samplingReviewResponse is the JSON shape we ask the LLM to return for peer review.
type samplingReviewResponse struct {
	Objections []struct {
		Severity    string `json:"severity"`
		Category    string `json:"category"`
		Description string `json:"description"`
		Suggestion  string `json:"suggestion"`
	} `json:"objections"`
	Strengths []string `json:"strengths"`
}

// requestSamplingReview calls the sampling provider to get LLM-enhanced peer review.
// Returns (objections, strengths, error). On any failure the caller falls back gracefully.
func (p *peerReviewPattern) requestSamplingReview(artifact string) ([]map[string]any, []string, error) {
	tmpl := GetSamplingPrompt("peer_review")
	var messages []think.SamplingMessage
	maxTokens := 2000
	if tmpl != nil {
		systemRole, userPrompt := FormatSamplingPrompt(tmpl, artifact)
		messages = []think.SamplingMessage{
			{Role: "user", Content: systemRole + "\n\n" + userPrompt},
		}
		maxTokens = tmpl.MaxTokens
	} else {
		messages = []think.SamplingMessage{
			{Role: "user", Content: fmt.Sprintf(
				`Review this artifact for issues. Artifact: %s. `+
					`Return JSON: {"objections": [{"severity": "P0|P1|P2|P3", "category": "...", "description": "...", "suggestion": "..."}], "strengths": ["..."]}`,
				artifact,
			)},
		}
	}

	raw, err := p.sampling.RequestSampling(context.Background(), messages, maxTokens)
	if err != nil {
		return nil, nil, err
	}

	var resp samplingReviewResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, nil, fmt.Errorf("sampling JSON parse failed: %w", err)
	}

	objections := make([]map[string]any, 0, len(resp.Objections))
	for _, o := range resp.Objections {
		objections = append(objections, map[string]any{
			"objection":  o.Description,
			"severity":   o.Severity,
			"category":   o.Category,
			"suggestion": o.Suggestion,
			"source":     "sampling",
		})
	}
	return objections, resp.Strengths, nil
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
