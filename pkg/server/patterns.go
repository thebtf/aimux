package server

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/guidance/policies"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
)

// patternDescriptions maps pattern names to FR-8 formatted descriptions.
// Format: "[solo — free] {situation}. {function}."
var patternDescriptions = map[string]string{
	"think":                    "[solo — free] Record and reflect on a thought. Use as a scratchpad when reasoning through a problem.",
	"critical_thinking":        "[solo — free] When you suspect bias in your reasoning. Scans text for cognitive biases using trigger-phrase detection.",
	"sequential_thinking":      "[solo — free] When a problem needs step-by-step reasoning with possible revision. Chains thoughts with branching support.",
	"debugging_approach":       "[solo — free] When you're stuck on a bug and need to narrow down the cause systematically. Tracks hypotheses, suggests 18 known methods. Use before asking the user for help.",
	"decision_framework":       "[solo — free] When you need to choose between options and can't decide. Scores alternatives against weighted criteria.",
	"problem_decomposition":    "[solo — free] When a problem feels too big to tackle at once. Breaks it into sub-problems with dependencies and risks.",
	"scientific_method":        "[solo — free] When you need rigorous hypothesis testing. Guides through observation → hypothesis → experiment → analysis → conclusion.",
	"mental_model":             "[solo — free] When you want a different perspective on a problem. Applies one of 15 mental models (first principles, inversion, etc.).",
	"metacognitive_monitoring": "[solo — free] When you're unsure if your reasoning is sound. Checks for overconfidence and blind spots.",
	"structured_argumentation": "[solo — free] When building or evaluating an argument. Structures claims, evidence, and rebuttals with severity ratings.",
	"collaborative_reasoning":  "[solo — free] When multiple perspectives would help. Multi-persona reasoning with stage tracking.",
	"recursive_thinking":       "[solo — free] When a problem has self-similar sub-problems. Recursive decomposition with base cases and depth tracking.",
	"domain_modeling":          "[solo — free] When understanding a domain's structure. Models entities, relationships, rules, and constraints.",
	"architecture_analysis":    "[solo — free] Before making architectural decisions. ATAM-lite analysis with coupling detection.",
	"stochastic_algorithm":     "[solo — free] When dealing with probabilistic decisions. Analyzes MDP, MCTS, bandit, Bayesian, and HMM approaches.",
	"temporal_thinking":        "[solo — free] When analyzing how a system behaves over time. Models states, events, transitions, and constraints.",
	"visual_reasoning":         "[solo — free] When analyzing diagrams or spatial relationships. Reasons about visual structures and transformations.",
	"source_comparison":        "[solo — free] When you have multiple sources and need to find agreement/disagreement. Produces confidence matrix.",
	"literature_review":        "[solo — free] When surveying a topic across multiple sources. Identifies themes, gaps, and research directions.",
	"peer_review":              "[solo — free] Before committing to a solution, stress-test it. Finds objections a senior engineer would raise.",
	"replication_analysis":     "[solo — free] When planning to replicate a claim or experiment. Identifies requirements and risks.",
	"experimental_loop":        "[solo — free] When running experiments iteratively. Tracks hypothesize → test → measure → iterate cycles.",
	"research_synthesis":       "[solo — free] When synthesizing findings from multiple sources. Produces structured claims with evidence and confidence.",
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
	}
}

// registerPatternTools registers all 23 think patterns as individual MCP tools.
// Called from registerTools() replacing the single "think" tool.
func (s *Server) registerPatternTools() {
	patterns.RegisterAll() // idempotent via sync.Once
	for _, name := range think.GetAllPatterns() {
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

// handlePattern is the unified dispatcher for all 23 per-pattern MCP tools.
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
	input := make(map[string]any)
	if args, ok := request.Params.Arguments.(map[string]any); ok {
		for fieldName := range handler.SchemaFields() {
			if v, exists := args[fieldName]; exists {
				input[fieldName] = v
			}
		}
	}

	sessionID := request.GetString("session_id", "")

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

	// Compute complexity for mode recommendation
	complexity := think.CalculateComplexity(patternName, input, 60)

	// Build response with mode indicator
	summary := think.GenerateSummary(thinkResult, complexity.Recommendation)
	response := map[string]any{
		"pattern":   thinkResult.Pattern,
		"status":    thinkResult.Status,
		"summary":   summary,
		"timestamp": thinkResult.Timestamp,
		"data":                   thinkResult.Data,
		"mode":                   "solo",
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
	if thinkResult.SessionID != "" {
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
		IsStateful: policies.IsStatefulPattern(patternName),
		StepNumber: stepNumber,
	}
	return s.marshalGuidedToolResult("think", patternName, thinkState, response)
}
