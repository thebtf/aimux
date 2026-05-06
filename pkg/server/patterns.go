package server

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/guidance/policies"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
)

// patternDescriptions maps low-level cognitive move names to public MCP descriptions.
// Format: "[cognitive move — solo/free] {situation}. {function}."
var patternDescriptions = map[string]string{
	"critical_thinking":        "[cognitive move — solo/free] When you suspect bias in your reasoning. Scans text for cognitive biases using trigger-phrase detection.",
	"sequential_thinking":      "[cognitive move — solo/free] When a problem needs step-by-step reasoning with possible revision. Chains thoughts with branching support.",
	"debugging_approach":       "[cognitive move — solo/free] When you're stuck on a bug and need to narrow down the cause systematically. Tracks hypotheses and known debugging methods.",
	"decision_framework":       "[cognitive move — solo/free] When you need to choose between options. Scores alternatives against weighted criteria.",
	"problem_decomposition":    "[cognitive move — solo/free] When a problem feels too big to tackle at once. Breaks it into sub-problems with dependencies and risks.",
	"scientific_method":        "[cognitive move — solo/free] When you need rigorous hypothesis testing. Guides through observation, hypothesis, experiment, analysis, and conclusion.",
	"mental_model":             "[cognitive move — solo/free] When you want a different perspective on a problem. Applies one of 15 mental models.",
	"metacognitive_monitoring": "[cognitive move — solo/free] When you're unsure if your reasoning is sound. Checks for overconfidence and blind spots.",
	"structured_argumentation": "[cognitive move — solo/free] When building or evaluating an argument. Structures claims, evidence, and rebuttals with severity ratings.",
	"collaborative_reasoning":  "[cognitive move — solo/free] When multiple perspectives would help. Multi-persona reasoning with stage tracking.",
	"recursive_thinking":       "[cognitive move — solo/free] When a problem has self-similar sub-problems. Recursive decomposition with base cases and depth tracking.",
	"domain_modeling":          "[cognitive move — solo/free] When understanding a domain's structure. Models entities, relationships, rules, and constraints.",
	"architecture_analysis":    "[cognitive move — solo/free] Before making architectural decisions. ATAM-lite analysis with coupling detection.",
	"stochastic_algorithm":     "[cognitive move — solo/free] When dealing with probabilistic decisions. Analyzes MDP, MCTS, bandit, Bayesian, and HMM approaches.",
	"temporal_thinking":        "[cognitive move — solo/free] When analyzing how a system behaves over time. Models states, events, transitions, and constraints.",
	"visual_reasoning":         "[cognitive move — solo/free] When analyzing diagrams or spatial relationships. Reasons about visual structures and transformations.",
	"source_comparison":        "[cognitive move — solo/free] When you have multiple sources and need to find agreement or disagreement. Produces confidence matrix.",
	"literature_review":        "[cognitive move — solo/free] When surveying a topic across multiple sources. Identifies themes, gaps, and research directions.",
	"peer_review":              "[cognitive move — solo/free] Before committing to a solution, stress-test it. Finds objections a senior engineer would raise.",
	"replication_analysis":     "[cognitive move — solo/free] When planning to replicate a claim or experiment. Identifies requirements and risks.",
	"experimental_loop":        "[cognitive move — solo/free] When running experiments iteratively. Tracks hypothesize, test, measure, and iterate cycles.",
	"research_synthesis":       "[cognitive move — solo/free] When synthesizing findings from multiple sources. Produces structured claims with evidence and confidence.",
}

// patternDescription returns the FR-8 description for a pattern handler.
// Falls back to handler.Description() if no entry in the map.
func patternDescription(handler think.PatternHandler) string {
	if desc, ok := patternDescriptions[handler.Name()]; ok {
		return desc
	}
	return handler.Description()
}

func validatePatternFieldSchema(patternName, fieldName string, schema think.FieldSchema) {
	switch schema.Type {
	case "array":
		if len(schema.Items) == 0 {
			panic(fmt.Sprintf("server: pattern %q field %q declares array schema without items", patternName, fieldName))
		}
	case "object":
		if len(schema.Properties) == 0 {
			panic(fmt.Sprintf("server: pattern %q field %q declares object schema without properties", patternName, fieldName))
		}
	case "enum":
		if len(schema.EnumValues) == 0 {
			panic(fmt.Sprintf("server: pattern %q field %q declares enum schema without values", patternName, fieldName))
		}
	case "string", "number", "integer", "boolean":
		// supported scalar types
	default:
		panic(fmt.Sprintf("server: pattern %q field %q declares unsupported schema type %q", patternName, fieldName, schema.Type))
	}
}

// registerPatternTools registers low-level cognitive moves as individual MCP tools.
func (s *Server) registerPatternTools() {
	patterns.RegisterAll() // idempotent via sync.Once
	for _, name := range think.GetAllPatterns() {
		if name == "think" {
			continue
		}
		handler := think.GetPattern(name)
		if handler == nil {
			continue
		}

		opts := []mcp.ToolOption{
			mcp.WithDescription(patternDescription(handler)),
		}

		// Add inputSchema fields from handler.SchemaFields()
		for fieldName, schema := range handler.SchemaFields() {
			validatePatternFieldSchema(name, fieldName, schema)
			switch schema.Type {
			case "string":
				fieldOpts := []mcp.PropertyOption{mcp.Description(schema.Description)}
				if schema.Required {
					fieldOpts = append(fieldOpts, mcp.Required())
				}
				opts = append(opts, mcp.WithString(fieldName, fieldOpts...))
			case "number":
				fieldOpts := []mcp.PropertyOption{mcp.Description(schema.Description)}
				if schema.Required {
					fieldOpts = append(fieldOpts, mcp.Required())
				}
				opts = append(opts, mcp.WithNumber(fieldName, fieldOpts...))
			case "integer":
				fieldOpts := []mcp.PropertyOption{mcp.Description(schema.Description)}
				if schema.Required {
					fieldOpts = append(fieldOpts, mcp.Required())
				}
				// mcp-go v0.47.0 has no WithInteger helper; publish integer fields via number registration
				// so they remain visible in inputSchema instead of being dropped silently.
				opts = append(opts, mcp.WithNumber(fieldName, fieldOpts...))
			case "boolean":
				fieldOpts := []mcp.PropertyOption{mcp.Description(schema.Description)}
				if schema.Required {
					fieldOpts = append(fieldOpts, mcp.Required())
				}
				opts = append(opts, mcp.WithBoolean(fieldName, fieldOpts...))
			case "array":
				fieldOpts := []mcp.PropertyOption{
					mcp.Description(schema.Description),
					mcp.Items(schema.Items),
				}
				if schema.Required {
					fieldOpts = append(fieldOpts, mcp.Required())
				}
				opts = append(opts, mcp.WithArray(fieldName, fieldOpts...))
			case "object":
				fieldOpts := []mcp.PropertyOption{
					mcp.Description(schema.Description),
					mcp.Properties(schema.Properties),
				}
				if schema.Required {
					fieldOpts = append(fieldOpts, mcp.Required())
				}
				opts = append(opts, mcp.WithObject(fieldName, fieldOpts...))
			case "enum":
				fieldOpts := []mcp.PropertyOption{
					mcp.Description(schema.Description),
					mcp.Enum(schema.EnumValues...),
				}
				if schema.Required {
					fieldOpts = append(fieldOpts, mcp.Required())
				}
				opts = append(opts, mcp.WithString(fieldName, fieldOpts...))
			}
		}

		// Add session_id for stateful patterns
		if think.IsStatefulPattern(name) {
			opts = append(opts, mcp.WithString("session_id",
				mcp.Description("Session ID for stateful continuation")))
		}

		// mode parameter: always present on every pattern tool.
		opts = append(opts, mcp.WithString("mode",
			mcp.Description("Thinking mode. solo: instant local processing. consensus: spawn multi-model analysis. auto (default): complexity metrics decide."),
			mcp.Enum("solo", "consensus", "auto"),
		))

		// All solo pattern tools are read-only and idempotent — they produce
		// structured analysis locally without modifying external state.
		opts = append(opts, mcp.WithToolAnnotation(mcp.ToolAnnotation{
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}))

		tool := mcp.NewTool(name, opts...)
		s.mcp.AddTool(tool, s.handlePattern)
	}
}

// handlePattern is the unified dispatcher for low-level cognitive move MCP tools.
// It extracts the tool name from the request to identify the PatternHandler,
// then executes the same logic as handleThink.
func (s *Server) handlePattern(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	patternName := request.Params.Name

	handler := think.GetPattern(patternName)
	if handler == nil {
		return mcp.NewToolResultError(fmt.Sprintf("unknown pattern %q; available: %v", patternName, think.GetAllPatterns())), nil
	}

	// Build input map from all fields declared in the handler's schema.
	// This avoids maintaining a separate hardcoded allowlist that can diverge
	// from SchemaFields() as patterns evolve.
	// Also capture mode and session_id separately — they are not pattern fields.
	input := make(map[string]any)
	requestedMode := "auto" // default
	if args, ok := request.Params.Arguments.(map[string]any); ok {
		for fieldName := range handler.SchemaFields() {
			if v, exists := args[fieldName]; exists {
				input[fieldName] = v
			}
		}
		if m, ok := args["mode"].(string); ok && m != "" {
			requestedMode = m
		}
	}
	if requestedMode != "solo" && requestedMode != "consensus" && requestedMode != "auto" {
		return mcp.NewToolResultError(fmt.Sprintf("invalid mode %q; use solo, consensus, or auto", requestedMode)), nil
	}

	sessionID := ""
	isStateful := think.IsStatefulPattern(patternName)
	if isStateful {
		sessionID = request.GetString("session_id", "")
	}

	// Fast-fail: reject consensus mode on solo-only patterns before executing handler.
	if requestedMode == "consensus" && think.GetDialogConfig(patternName) == nil {
		return mcp.NewToolResultError(fmt.Sprintf(
			"pattern %q does not support consensus mode (solo-only). Use solo or auto, or pick a pattern that supports multi-model analysis",
			patternName,
		)), nil
	}

	// Validate input
	validInput, err := handler.Validate(input)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("validation error: %v", err)), nil
	}

	// Execute pattern handler
	thinkResult, err := handler.Handle(validInput, sessionID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("pattern error: %v", err)), nil
	}

	// --- Phase 3: enforcement gate + pattern advisor ---
	gate := think.NewEnforcementGate()
	var gateDecision think.GateDecision
	if sessionID != "" {
		sess := think.GetSession(sessionID)
		gateDecision = gate.Check(patternName, sess)
	} else {
		// Stateless invocation: no session to check; treat as complete.
		gateDecision = think.GateDecision{Status: "complete"}
	}

	advisor := think.NewPatternAdvisor()
	var advisorRec think.Recommendation
	if sessionID != "" {
		sess := think.GetSession(sessionID)
		advisorRec = advisor.Evaluate(sess, thinkResult)
		if advisorRec.StatePatch != nil {
			think.UpdateSessionState(sess.ID, advisorRec.StatePatch)
		}
	} else {
		advisorRec = think.Recommendation{Action: "continue", Reason: "stateless invocation"}
	}

	// Compute complexity for mode recommendation
	complexity := think.CalculateComplexity(patternName, input, 60)

	// Determine effective mode based on requested mode + complexity + pattern capability.
	effectiveMode, modeErr := resolveEffectiveMode(requestedMode, patternName, complexity.Recommendation)
	if modeErr != nil {
		return mcp.NewToolResultError(modeErr.Error()), nil
	}

	// Build response with mode indicator
	summary := think.GenerateSummary(thinkResult, complexity.Recommendation)
	response := map[string]any{
		"pattern":                  thinkResult.Pattern,
		"status":                   thinkResult.Status,
		"summary":                  summary,
		"timestamp":                thinkResult.Timestamp,
		"data":                     thinkResult.Data,
		"mode":                     effectiveMode,
		"complexityRecommendation": complexity.Recommendation,
		"complexity": map[string]any{
			"total":     complexity.Total,
			"threshold": complexity.Threshold,
			"components": map[string]any{
				"textLength":      complexity.TextLength,
				"subItemCount":    complexity.SubItemCount,
				"structuralDepth": complexity.StructuralDepth,
				"patternBias":     complexity.PatternBias,
			},
		},
	}
	if isStateful && thinkResult.SessionID != "" {
		response["session_id"] = thinkResult.SessionID
	}
	if thinkResult.SuggestedNextPattern != "" {
		response["suggestedNextPattern"] = thinkResult.SuggestedNextPattern
	}
	if thinkResult.Metadata != nil {
		response["metadata"] = thinkResult.Metadata
	}
	if len(thinkResult.ComputedFields) > 0 {
		response["computed_fields"] = thinkResult.ComputedFields
	}

	// Phase 3: gate + advisor enrichment
	response["gate_status"] = gateDecision.Status
	if gateDecision.Reason != "" {
		response["gate_reason"] = gateDecision.Reason
	}
	response["advisor_recommendation"] = map[string]any{
		"action": advisorRec.Action,
		"target": advisorRec.Target,
		"reason": advisorRec.Reason,
	}

	// Consensus-mode graceful degradation hints.
	if effectiveMode == "consensus_recommended" {
		response["consensus_available"] = false
		response["consensus_hint"] = "Consensus mode requires dialog manager integration. Use consensus/debate tools directly for multi-model analysis."
	}

	// Extract step number from result data for policy state labels
	stepNumber := 0
	if n, ok := thinkResult.Data["thoughtNumber"]; ok {
		switch v := n.(type) {
		case int:
			stepNumber = v
		case float64:
			stepNumber = int(v)
		}
	}

	thinkState := &policies.ThinkPolicyInput{
		Pattern:    patternName,
		SessionID:  thinkResult.SessionID,
		IsStateful: isStateful,
		StepNumber: stepNumber,
	}
	return s.marshalGuidedToolResult("think", patternName, thinkState, response)
}

// resolveEffectiveMode maps (requestedMode, patternName, complexityRecommendation)
// to the effective mode string placed in the response.
//
// Pre-condition: caller has already fast-failed for consensus+solo-only combinations.
//
// Rules:
//   - "solo"      → always "solo", regardless of complexity.
//   - "consensus" → "consensus_recommended" (graceful degradation until dialog manager
//     integration is wired; caller has already validated pattern supports consensus).
//   - "auto"      → "consensus_recommended" when complexity recommends consensus AND
//     the pattern has a dialog config; "solo" otherwise.
//   - default     → error.
func resolveEffectiveMode(requestedMode, patternName, complexityRec string) (string, error) {
	switch requestedMode {
	case "solo":
		return "solo", nil
	case "consensus":
		// Graceful degradation: dialog manager not yet wired — advertise but don't block.
		// Solo-only patterns are already rejected before this function is reached.
		return "consensus_recommended", nil
	case "auto":
		if complexityRec == "consensus" && think.GetDialogConfig(patternName) != nil {
			return "consensus_recommended", nil
		}
		return "solo", nil
	default:
		return "", fmt.Errorf("invalid mode %q; use solo, consensus, or auto", requestedMode)
	}
}
