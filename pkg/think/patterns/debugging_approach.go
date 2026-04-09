package patterns

import (
	"fmt"
	"time"

	think "github.com/thebtf/aimux/pkg/think"
)

const pivotSuggestionThreshold = 3 // Suggest different approach after this many refuted hypotheses

// knownMethods maps debugging approach names to their descriptions.
var knownMethods = map[string]string{
	"binary_search":   "Narrow the problem space by testing the midpoint",
	"bisect":          "Find the commit that introduced the bug",
	"trace":           "Follow execution path step by step",
	"delta_debugging":  "Minimize the failure-inducing input",
	"wolf_fence":      "Divide the search space in half repeatedly",
	"rubber_duck":     "Explain the problem aloud to identify assumptions",
	"printf":          "Add output statements to track variable values",
	"reverse":         "Work backwards from the symptom to the cause",
	"hypothesis":      "Form and test specific hypotheses",
	"differential":    "Compare working vs broken state",
	"stack_trace":     "Analyze the call stack at point of failure",
	"git_blame":       "Check who changed what and when",
	"minimal_repro":   "Create the smallest possible reproduction case",
	"divide_conquer":  "Split the system into testable parts",
	"profiler":        "Use performance profiling tools",
	"memory_dump":     "Analyze memory state at failure point",
	"network_trace":   "Capture and analyze network traffic",
	"static_analysis": "Use static analysis tools to find issues",
}

var validHypothesisStatuses = map[string]bool{
	"untested": true, "tested": true, "confirmed": true, "refuted": true,
}

type debuggingApproachPattern struct{}

// NewDebuggingApproachPattern returns the "debugging_approach" pattern handler.
func NewDebuggingApproachPattern() think.PatternHandler { return &debuggingApproachPattern{} }

func (p *debuggingApproachPattern) Name() string { return "debugging_approach" }

func (p *debuggingApproachPattern) Description() string {
	return "Structured debugging with hypothesis tracking and 18 known methods"
}

func (p *debuggingApproachPattern) Validate(input map[string]any) (map[string]any, error) {
	issueRaw, ok := input["issue"]
	if !ok {
		return nil, fmt.Errorf("missing required field: issue")
	}
	issue, ok := issueRaw.(string)
	if !ok || issue == "" {
		return nil, fmt.Errorf("field 'issue' must be a non-empty string")
	}

	validated := map[string]any{"issue": issue}

	if v, ok := input["approachName"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("field 'approachName' must be a string")
		}
		validated["approachName"] = s
	}

	if v, ok := input["hypothesis"]; ok {
		h, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field 'hypothesis' must be a map with 'id' and 'text'")
		}
		id, idOk := h["id"].(string)
		text, textOk := h["text"].(string)
		if !idOk || !textOk || id == "" || text == "" {
			return nil, fmt.Errorf("hypothesis must have non-empty 'id' and 'text' strings")
		}
		validatedHyp := map[string]any{"id": id, "text": text}
		if conf, ok := h["confidence"].(float64); ok {
			validatedHyp["confidence"] = conf
		}
		validated["hypothesis"] = validatedHyp
	}

	if v, ok := input["hypothesisUpdate"]; ok {
		hu, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field 'hypothesisUpdate' must be a map with 'id' and 'status'")
		}
		id, idOk := hu["id"].(string)
		status, statusOk := hu["status"].(string)
		if !idOk || !statusOk || id == "" {
			return nil, fmt.Errorf("hypothesisUpdate must have non-empty 'id' and 'status'")
		}
		if !validHypothesisStatuses[status] {
			return nil, fmt.Errorf("hypothesisUpdate 'status' must be one of: untested, tested, confirmed, refuted")
		}
		validated["hypothesisUpdate"] = map[string]any{"id": id, "status": status}
	}

	return validated, nil
}

func (p *debuggingApproachPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	sess := think.GetOrCreateSession(sessionID, "debugging_approach", map[string]any{
		"hypotheses": []any{},
	})

	hypotheses, _ := sess.State["hypotheses"].([]any)

	// Add new hypothesis
	if hRaw, ok := validInput["hypothesis"]; ok {
		h := hRaw.(map[string]any)
		entry := map[string]any{
			"id":      h["id"],
			"text":    h["text"],
			"status":  "untested",
			"addedAt": time.Now().UTC().Format(time.RFC3339),
		}
		hypotheses = append(hypotheses, entry)
	}

	// Update existing hypothesis
	if huRaw, ok := validInput["hypothesisUpdate"]; ok {
		hu := huRaw.(map[string]any)
		targetID := hu["id"].(string)
		newStatus := hu["status"].(string)
		found := false
		updated := make([]any, len(hypotheses))
		for i, hRaw := range hypotheses {
			h, ok := hRaw.(map[string]any)
			if !ok {
				updated[i] = hRaw
				continue
			}
			if h["id"] == targetID {
				found = true
				newH := make(map[string]any, len(h))
				for k, v := range h {
					newH[k] = v
				}
				newH["status"] = newStatus
				updated[i] = newH
			} else {
				updated[i] = hRaw
			}
		}
		if !found {
			return nil, fmt.Errorf("hypothesis with id '%s' not found", targetID)
		}
		hypotheses = updated
	}

	think.UpdateSessionState(sessionID, map[string]any{
		"hypotheses": hypotheses,
	})

	issue := validInput["issue"].(string)
	methodDescription := "custom approach"
	if approachName, ok := validInput["approachName"].(string); ok {
		if desc, found := knownMethods[approachName]; found {
			methodDescription = desc
		}
	}

	confirmedCount := 0
	refutedCount := 0
	for _, hRaw := range hypotheses {
		if h, ok := hRaw.(map[string]any); ok {
			switch h["status"] {
			case "confirmed":
				confirmedCount++
			case "refuted":
				refutedCount++
			}
		}
	}

	guidanceDepth := "enriched"
	if len(hypotheses) == 0 {
		guidanceDepth = "basic"
	}

	data := map[string]any{
		"issue":             issue,
		"methodDescription": methodDescription,
		"hypotheses":        hypotheses,
		"hypothesisCount":   len(hypotheses),
		"confirmedCount":    confirmedCount,
		"refutedCount":      refutedCount,
		"guidance":          BuildGuidance("debugging_approach", guidanceDepth, []string{"approachName", "hypothesis", "hypothesisUpdate"}),
	}

	if refutedCount >= pivotSuggestionThreshold {
		data["suggestion"] = "3+ hypotheses refuted — consider trying a fundamentally different approach"
	}

	// Forced Reflection Protocol: gate hypothesis submission behind evidence requirements.
	if hRaw, hasHyp := validInput["hypothesis"]; hasHyp {
		// findingsCount = total hypotheses including the one just added
		findingsCount := len(hypotheses)

		if directive := ValidateEvidenceGate(findingsCount, 3); directive != nil {
			data["reflection"] = directive
		} else {
			// Evidence gate passed — check for overconfidence.
			h := hRaw.(map[string]any)
			if conf, ok := h["confidence"].(float64); ok {
				if directive := ValidateConfidence(conf, findingsCount); directive != nil {
					data["reflection"] = directive
				}
			}
		}
	}

	// Tier 2A: text analysis
	primaryText := validInput["issue"].(string)
	if analysis := AnalyzeText(primaryText); analysis != nil {
		domain := MatchDomainTemplate(primaryText)
		if domain != nil {
			analysis.Gaps = DetectGaps(analysis.Entities, domain)
		}
		data["textAnalysis"] = analysis
	}

	return think.MakeThinkResult("debugging_approach", data, sessionID, nil, "", nil), nil
}
