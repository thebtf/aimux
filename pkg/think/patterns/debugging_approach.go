package patterns

import (
	"fmt"
	"strings"
	"time"

	think "github.com/thebtf/aimux/pkg/think"
)

const pivotSuggestionThreshold = 3 // Suggest different approach after this many refuted hypotheses

// knownMethods maps debugging approach names to their descriptions.
var knownMethods = map[string]string{
	"binary_search":   "Narrow the problem space by testing the midpoint",
	"bisect":          "Find the commit that introduced the bug",
	"trace":           "Follow execution path step by step",
	"delta_debugging": "Minimize the failure-inducing input",
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

// confidenceEnumToFloat maps flat confidence enum strings to float values.
func confidenceEnumToFloat(s string) float64 {
	switch s {
	case "exploring":
		return 0.1
	case "low":
		return 0.2
	case "medium":
		return 0.5
	case "high":
		return 0.7
	case "very_high":
		return 0.85
	case "certain":
		return 0.95
	default:
		return 0.5
	}
}

// generateHypothesisID produces a time-based unique ID for flat-format hypotheses.
func generateHypothesisID() string {
	return fmt.Sprintf("h_%d", time.Now().UnixNano())
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

	// --- Flat format detection ---
	_, hasHypothesisText := input["hypothesis_text"]
	_, hasHypothesisAction := input["hypothesis_action"]
	_, hasStepNumber := input["step_number"]

	if hasHypothesisText || hasHypothesisAction || hasStepNumber {
		// New flat format path.

		// step_number: MCP sends float64 for numbers.
		if v, ok := input["step_number"]; ok {
			switch n := v.(type) {
			case float64:
				validated["step_number"] = int(n)
			case int:
				validated["step_number"] = n
			default:
				return nil, fmt.Errorf("field 'step_number' must be a number")
			}
		}

		// next_step_needed
		if v, ok := input["next_step_needed"]; ok {
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("field 'next_step_needed' must be a bool")
			}
			validated["next_step_needed"] = b
		}

		// findings_text
		if v, ok := input["findings_text"]; ok {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("field 'findings_text' must be a string")
			}
			validated["findings_text"] = s
		}

		// resolution_text
		if v, ok := input["resolution_text"]; ok {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("field 'resolution_text' must be a string")
			}
			validated["resolution_text"] = s
		}

		// hypothesis_text → create hypothesis map
		if v, ok := input["hypothesis_text"]; ok {
			text, ok := v.(string)
			if !ok || text == "" {
				return nil, fmt.Errorf("field 'hypothesis_text' must be a non-empty string")
			}
			conf := 0.5 // default
			if cv, ok := input["confidence"]; ok {
				cs, ok := cv.(string)
				if !ok {
					return nil, fmt.Errorf("field 'confidence' must be a string enum")
				}
				switch cs {
				case "exploring", "low", "medium", "high", "very_high", "certain":
					conf = confidenceEnumToFloat(cs)
				default:
					return nil, fmt.Errorf("field 'confidence' must be one of: exploring, low, medium, high, very_high, certain")
				}
			}
			validated["hypothesis"] = map[string]any{
				"id":         generateHypothesisID(),
				"text":       text,
				"confidence": conf,
			}
		}

		// hypothesis_action: confirm/refute → hypothesisUpdate targeting last hypothesis in session
		if v, ok := input["hypothesis_action"]; ok {
			action, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("field 'hypothesis_action' must be a string")
			}
			validated["hypothesis_action"] = action
			var status string
			switch action {
			case "confirm":
				status = "confirmed"
			case "refute":
				status = "refuted"
			case "propose":
				// propose just adds a hypothesis; if hypothesis_text also present it's already handled above
				// nothing to do for hypothesisUpdate
			default:
				return nil, fmt.Errorf("field 'hypothesis_action' must be one of: propose/confirm/refute")
			}
			if status != "" {
				// We need the last hypothesis ID from session; we pass a sentinel and resolve in Handle.
				validated["hypothesisUpdate"] = map[string]any{
					"id":     "__last__",
					"status": status,
				}
			}
		}

		return validated, nil
	}

	// --- Old nested format path (backward compat) ---

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

func (p *debuggingApproachPattern) SchemaFields() map[string]think.FieldSchema {
	return map[string]think.FieldSchema{
		"issue":             {Type: "string", Required: true, Description: "The issue or bug being debugged"},
		"approachName":      {Type: "string", Required: false, Description: "Debugging method name (e.g. binary_search, trace, rubber_duck)"},
		"hypothesis_text":   {Type: "string", Required: false, Description: "Flat format: text of a new hypothesis to add"},
		"confidence":        {Type: "enum", Required: false, Description: "Confidence level for the hypothesis", EnumValues: []string{"exploring", "low", "medium", "high", "very_high", "certain"}},
		"hypothesis_action": {Type: "enum", Required: false, Description: "Flat format: action on last hypothesis", EnumValues: []string{"propose", "confirm", "refute"}},
		"step_number":       {Type: "number", Required: false, Description: "External step tracking number"},
		"next_step_needed":  {Type: "boolean", Required: false, Description: "Whether another step is needed"},
		"findings_text":     {Type: "string", Required: false, Description: "Findings from this debugging step"},
		"resolution_text":   {Type: "string", Required: false, Description: "Resolution text when bug is resolved"},
	}
}

func (p *debuggingApproachPattern) Category() string { return "solo" }

func (p *debuggingApproachPattern) Handle(validInput map[string]any, sessionID string) (*think.ThinkResult, error) {
	sess := think.GetOrCreateSession(sessionID, "debugging_approach", map[string]any{
		"hypotheses": []any{},
		"steps":      []any{},
		"resolution": "",
	})

	hypotheses, _ := sess.State["hypotheses"].([]any)
	steps, _ := sess.State["steps"].([]any)
	resolution, _ := sess.State["resolution"].(string)

	// Add new hypothesis with duplicate detection (case-insensitive exact match).
	duplicateDetected := false
	if hRaw, ok := validInput["hypothesis"]; ok {
		h := hRaw.(map[string]any)
		newText, _ := h["text"].(string)
		newTextLower := strings.ToLower(newText)
		for _, existing := range hypotheses {
			if em, ok := existing.(map[string]any); ok {
				if existText, ok := em["text"].(string); ok {
					if strings.ToLower(existText) == newTextLower {
						duplicateDetected = true
						break
					}
				}
			}
		}
		if !duplicateDetected {
			entry := map[string]any{
				"id":      h["id"],
				"text":    h["text"],
				"status":  "untested",
				"addedAt": time.Now().UTC().Format(time.RFC3339),
			}
			hypotheses = append(hypotheses, entry)
		}
	}

	// Resolve __last__ sentinel for flat hypothesis_action path.
	if huRaw, ok := validInput["hypothesisUpdate"]; ok {
		hu := huRaw.(map[string]any)
		if hu["id"] == "__last__" {
			if len(hypotheses) == 0 {
				return nil, fmt.Errorf("no hypothesis to update: session has no hypotheses")
			}
			lastH, ok := hypotheses[len(hypotheses)-1].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("malformed hypothesis in session")
			}
			hu = map[string]any{
				"id":     lastH["id"],
				"status": hu["status"],
			}
			validInput["hypothesisUpdate"] = hu
		}
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

	// Track steps: each Handle invocation with findings_text or approachName is a step.
	stepEntry := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	if approachName, ok := validInput["approachName"].(string); ok {
		stepEntry["approachName"] = approachName
	}
	if findingsText, ok := validInput["findings_text"].(string); ok {
		stepEntry["findings"] = findingsText
	}
	if hypAction, ok := validInput["hypothesis_action"].(string); ok {
		stepEntry["hypothesis_action"] = hypAction
	}
	// Only append a step when there's meaningful content to track.
	if _, hasApproach := stepEntry["approachName"]; hasApproach {
		steps = append(steps, stepEntry)
	} else if _, hasFindings := stepEntry["findings"]; hasFindings {
		steps = append(steps, stepEntry)
	}

	// Track resolution: resolution_text input field (TS v1 parity).
	if resText, ok := validInput["resolution_text"].(string); ok && resText != "" {
		resolution = resText
	}

	think.UpdateSessionState(sessionID, map[string]any{
		"hypotheses": hypotheses,
		"steps":      steps,
		"resolution": resolution,
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

	// TS v1 parity: hasFindings — true when any step has findings recorded.
	hasFindings := false
	for _, s := range steps {
		if sm, ok := s.(map[string]any); ok {
			if _, found := sm["findings"]; found {
				hasFindings = true
				break
			}
		}
	}
	// Also consider current findings_text input.
	if ft, ok := validInput["findings_text"].(string); ok && ft != "" {
		hasFindings = true
	}

	hasResolution := resolution != ""

	data := map[string]any{
		"issue":             issue,
		"methodDescription": methodDescription,
		"hypotheses":        hypotheses,
		"hypothesisCount":   len(hypotheses),
		"confirmedCount":    confirmedCount,
		"refutedCount":      refutedCount,
		// TS v1 parity fields.
		"duplicateDetected": duplicateDetected,
		"stepCount":         len(steps),
		"steps":             steps,
		"hasFindings":       hasFindings,
		"hasResolution":     hasResolution,
		"suggestedNext":     "scientific_method",
		"guidance":          BuildGuidance("debugging_approach", guidanceDepth, []string{"approachName", "hypothesis", "hypothesisUpdate"}),
	}
	if hasResolution {
		data["resolution"] = resolution
	}

	// Propagate flat format optional fields to output.
	if v, ok := validInput["step_number"]; ok {
		data["step_number"] = v
	}
	if v, ok := validInput["next_step_needed"]; ok {
		data["next_step_needed"] = v
	}
	if v, ok := validInput["findings_text"]; ok {
		data["findings_text"] = v
	}

	if refutedCount >= pivotSuggestionThreshold {
		data["suggestion"] = "3+ hypotheses refuted — consider trying a fundamentally different approach"
	}

	// Method efficiency: for each unique approachName in steps, compute
	// confirmed / (confirmed + refuted) based on hypothesis_action recorded in those steps.
	efficiencyMap := map[string]float64{}
	type methodCounts struct{ confirmed, refuted int }
	counts := map[string]*methodCounts{}
	for _, sRaw := range steps {
		sm, ok := sRaw.(map[string]any)
		if !ok {
			continue
		}
		approach, hasApproach := sm["approachName"].(string)
		if !hasApproach || approach == "" {
			continue
		}
		action, _ := sm["hypothesis_action"].(string)
		if _, seen := counts[approach]; !seen {
			counts[approach] = &methodCounts{}
		}
		switch action {
		case "confirm":
			counts[approach].confirmed++
		case "refute":
			counts[approach].refuted++
		}
	}
	for approach, mc := range counts {
		total := mc.confirmed + mc.refuted
		if total == 0 {
			continue
		}
		efficiencyMap[approach] = float64(mc.confirmed) / float64(total)
	}
	if len(efficiencyMap) > 0 {
		data["methodEfficiency"] = efficiencyMap
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

	return think.MakeThinkResult("debugging_approach", data, sessionID, nil, "", nil), nil
}
