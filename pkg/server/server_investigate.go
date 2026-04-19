package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/guidance"
	inv "github.com/thebtf/aimux/pkg/investigate"
	"github.com/thebtf/aimux/pkg/guidance/policies"
	"github.com/thebtf/aimux/pkg/parser"
	"github.com/thebtf/aimux/pkg/resolve"
	"github.com/thebtf/aimux/pkg/server/budget"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/types"
)

// marshalToolResult marshals data to JSON and returns an MCP tool result.
// Returns an error result if marshaling fails instead of silently returning empty.
func marshalToolResult(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("internal error: response serialization failed: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// buildGuidancePlan resolves the policy for tool from the server registry and computes the NextActionPlan.
// Returns the plan and a boolean indicating whether plan.State is "report_ready".
// Falls back to a zero plan when no policy is registered for the tool.
//
// NOTE: s may be nil during tests or early init; in that case only the zero plan is returned.
func (s *Server) buildGuidancePlan(tool, action string, stateSnapshot, rawResult any) (guidance.NextActionPlan, bool) {
	zero := guidance.NextActionPlan{}
	if s == nil || s.guidanceReg == nil {
		return zero, false
	}
	policy, ok := s.guidanceReg.Get(tool)
	if !ok {
		return zero, false
	}
	plan, err := policy.BuildPlan(guidance.PolicyInput{
		Action:        action,
		StateSnapshot: stateSnapshot,
		RawResult:     rawResult,
	})
	if err != nil {
		return zero, false
	}
	return plan, plan.State == "report_ready"
}

func (s *Server) marshalGuidedToolResult(tool, action string, stateSnapshot any, rawResult any) (*mcp.CallToolResult, error) {
	plan, _ := s.buildGuidancePlan(tool, action, stateSnapshot, rawResult)
	return s.marshalGuidedToolResultWithPlan(plan, tool, action, stateSnapshot, rawResult)
}

// marshalGuidedToolResultWithPlan assembles the guided response envelope using a pre-computed plan,
// avoiding a redundant BuildPlan call when the caller already computed it.
func (s *Server) marshalGuidedToolResultWithPlan(plan guidance.NextActionPlan, tool, action string, stateSnapshot, rawResult any) (*mcp.CallToolResult, error) {
	payload := guidance.NewResponseBuilder().BuildPayload(plan, guidance.HandlerResult{
		Tool:   tool,
		Action: action,
		State:  stateSnapshot,
		Result: rawResult,
	})
	return marshalToolResult(payload)
}

func (s *Server) handleThink(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	patternName, err := request.RequireString("pattern")
	if err != nil {
		return mcp.NewToolResultError("pattern is required"), nil
	}

	handler := think.GetPattern(patternName)
	if handler == nil {
		return mcp.NewToolResultError(fmt.Sprintf("unknown pattern %q; available: %v", patternName, think.GetAllPatterns())), nil
	}

	// Build input map from all optional MCP params
	input := make(map[string]any)
	optionalStrings := []string{
		"issue", "topic", "thought", "decision", "problem", "task",
		"modelName", "approachName", "domainName", "timeFrame", "operation",
		"observation", "question", "hypothesis", "experiment", "analysis",
		"conclusion", "algorithmType", "problemDefinition", "baseCase",
		"recursiveCase", "convergenceCheck", "diagramType", "description",
		"methodology", "knowledgeAssessment", "result", "stage", "branchId",
		"artifact", "claim",
		"hypothesis_text", "confidence", "findings_text", "hypothesis_action",
		"entry_type", "entry_text", "link_to",
		"contribution_type", "contribution_text", "persona_id",
		"argument_type", "argument_text", "supports_claim_id",
	}
	for _, key := range optionalStrings {
		if v := request.GetString(key, ""); v != "" {
			input[key] = v
		}
	}

	sessionID := request.GetString("session_id", "")

	// Pass through structured, numeric, and boolean params from the raw arguments
	if args, ok := request.Params.Arguments.(map[string]any); ok {
		forwardKeys := []string{
			// Structured
			"criteria", "options", "components", "sources", "findings",
			"subProblems", "dependencies",
			"risks", "stakeholders", "entities", "relationships", "rules",
			"constraints", "states", "events", "transitions", "transformations",
			"elements", "claims", "biases", "uncertainties", "cognitiveProcesses",
			"parameters", "argument", "contribution", "entry", "hypothesisUpdate",
			// Numeric
			"confidence", "thoughtNumber", "totalThoughts", "currentDepth",
			"maxDepth", "iterations", "revisesThought", "branchFromThought",
			"step_number", "contribution_confidence",
			// Boolean
			"isRevision",
			"next_step_needed",
		}
		for _, key := range forwardKeys {
			if v, exists := args[key]; exists {
				input[key] = v
			}
		}
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

	// Compute complexity for mode recommendation
	complexity := think.CalculateComplexity(patternName, input, 60)

	// Build response with mode indicator
	summary := think.GenerateSummary(thinkResult, complexity.Recommendation)
	response := map[string]any{
		"pattern":   thinkResult.Pattern,
		"status":    thinkResult.Status,
		"summary":   summary,
		"timestamp": thinkResult.Timestamp,
		"data":      thinkResult.Data,
		"mode":      complexity.Recommendation,
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

	// Extract step number from result data so the policy can produce accurate state labels.
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

func (s *Server) handleInvestigate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action, err := request.RequireString("action")
	if err != nil {
		return mcp.NewToolResultError("action is required"), nil
	}

	switch action {
	case "start":
		topic := request.GetString("topic", "")
		if topic == "" {
			return mcp.NewToolResultError("topic required for start"), nil
		}
		domainName := request.GetString("domain", "")
		if domainName == "" {
			domainName = inv.AutoDetectDomain(topic)
		}
		if inv.GetDomain(domainName) == nil {
			return mcp.NewToolResultError(fmt.Sprintf("unknown domain %q; valid: %v", domainName, inv.DomainNames())), nil
		}

		sess := s.sessions.Create("investigate", types.SessionModeOnceStateful, "")
		state := inv.CreateInvestigation(sess.ID, topic, domainName)

		result := map[string]any{
			"session_id":     sess.ID,
			"topic":          state.Topic,
			"domain":         state.Domain,
			"coverage_areas": state.CoverageAreas,
			"guidance": func() string {
				base := fmt.Sprintf("Begin investigation [%s domain]. ", state.Domain)
				if len(state.CoverageAreas) > 0 {
					base += fmt.Sprintf("Recommended first area: %s. ", state.CoverageAreas[0])
				}
				return base + "Read implementations, not descriptions. Then call finding action."
			}(),
		}
		if domainName == "" {
			result["available_domains"] = inv.DomainNames()
		}
		return s.marshalGuidedToolResult("investigate", action, state, result)

	case "auto":
		topic := request.GetString("topic", "")
		if topic == "" {
			return mcp.NewToolResultError("topic required for auto"), nil
		}
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		domainName := request.GetString("domain", "")
		if domainName == "" {
			domainName = inv.AutoDetectDomain(topic)
		}
		if inv.GetDomain(domainName) == nil {
			return mcp.NewToolResultError(fmt.Sprintf("unknown domain %q; valid: %v", domainName, inv.DomainNames())), nil
		}

		delegateCLI := request.GetString("cli", "")
		var delegateModel, delegateEffort string
		if delegateCLI == "" {
			if pref, resolveErr := s.router.Resolve("analyze"); resolveErr == nil && pref.CLI != "" {
				delegateCLI = pref.CLI
				delegateModel = pref.Model
				delegateEffort = pref.ReasoningEffort
			}
		}
		if delegateCLI == "" {
			delegateCLI = "codex"
		}

		profile, profileErr := s.registry.Get(delegateCLI)
		if profileErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("CLI %q not configured for investigate auto", delegateCLI)), nil
		}

		cwd := request.GetString("cwd", "")
		if cwd != "" {
			cwd = filepath.Clean(cwd)
			if info, err := os.Stat(cwd); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("cwd %q not found: %v", cwd, err)), nil
			} else if !info.IsDir() {
				return mcp.NewToolResultError(fmt.Sprintf("cwd %q is not a directory", cwd)), nil
			}
		}

		sess := s.sessions.Create("investigate", types.SessionModeOnceStateful, cwd)
		s.sessions.Update(sess.ID, func(ss *session.Session) {
			ss.Metadata = map[string]any{
				"source": "delegate",
				"cli":    delegateCLI,
			}
		})
		state := inv.CreateInvestigation(sess.ID, topic, domainName)
		job := s.jobs.Create(sess.ID, delegateCLI)

		autoPrompt := inv.BuildAutoDelegatePrompt(topic, state.CoverageAreas)
		args := types.SpawnArgs{
			CLI:            delegateCLI,
			Command:        resolve.CommandBinary(profile.Command.Base),
			Args:           resolve.BuildPromptArgs(profile, delegateModel, delegateEffort, false, autoPrompt),
			CWD:            cwd,
			TimeoutSeconds: profile.TimeoutSeconds,
			OnOutput:       s.progressSink(job.ID, profile.OutputFormat),
		}

		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		s.sendBusy(job.ID, "investigate:auto:"+delegateCLI, profile.TimeoutSeconds*1000)
		go func() {
			s.jobs.StartJob(job.ID, 0)
			s.sessions.Update(sess.ID, func(ss *session.Session) {
				ss.Status = types.SessionStatusRunning
			})
			defer s.sendIdle(job.ID)

			result, runErr := s.executor.Run(jobCtx, args)
			if runErr != nil {
				s.jobs.FailJob(job.ID, types.NewExecutorError(runErr.Error(), runErr, ""))
				s.sessions.Update(sess.ID, func(ss *session.Session) {
					ss.Status = types.SessionStatusFailed
				})
				return
			}
			if result.Error != nil {
				s.jobs.FailJob(job.ID, result.Error)
				s.sessions.Update(sess.ID, func(ss *session.Session) {
					ss.Status = types.SessionStatusFailed
				})
				return
			}

			parsed, cliSessionID := parser.ParseContent(result.Content, profile.OutputFormat)
			if cliSessionID != "" {
				s.sessions.Update(sess.ID, func(ss *session.Session) {
					ss.CLISessionID = cliSessionID
				})
			}

			delegateFinding := inv.DelegateFindingFromOutput(delegateCLI, topic, parsed, state.CoverageAreas)
			if _, _, findErr := inv.AddFinding(sess.ID, delegateFinding); findErr != nil {
				s.jobs.FailJob(job.ID, types.NewExecutorError(findErr.Error(), findErr, ""))
				s.sessions.Update(sess.ID, func(ss *session.Session) {
					ss.Status = types.SessionStatusFailed
				})
				return
			}

			updatedState := inv.GetInvestigation(sess.ID)
			delegateReport := ""
			if updatedState != nil {
				delegateReport = inv.GenerateReport(updatedState)
			}
			s.sessions.Update(sess.ID, func(ss *session.Session) {
				if ss.Metadata == nil {
					ss.Metadata = make(map[string]any)
				}
				ss.Metadata["source"] = "delegate"
				ss.Metadata["cli"] = delegateCLI
				ss.Metadata["delegate_report"] = delegateReport
				ss.Status = types.SessionStatusCompleted
				ss.Turns++
			})
			completeContent := parsed
			if delegateReport != "" {
				completeContent = parsed + "\n\n" + delegateReport
			}
			s.jobs.CompleteJob(job.ID, completeContent, result.ExitCode)
		}()

		return s.marshalGuidedToolResult("investigate", action, state, map[string]any{
			"job_id":     job.ID,
			"session_id": sess.ID,
			"status":     "running",
			"cli":        delegateCLI,
		})

	case "finding":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for finding"), nil
		}
		desc := request.GetString("description", "")
		if desc == "" {
			return mcp.NewToolResultError("description required for finding"), nil
		}
		source := request.GetString("source", "")
		if source == "" {
			return mcp.NewToolResultError("source required for finding"), nil
		}
		severity := request.GetString("severity", "P2")
		validSeverities := map[string]bool{"P0": true, "P1": true, "P2": true, "P3": true}
		if !validSeverities[severity] {
			return mcp.NewToolResultError(fmt.Sprintf("invalid severity %q: must be one of P0, P1, P2, P3", severity)), nil
		}

		confidence := request.GetString("confidence", "")
		validConfidences := map[string]bool{"": true, "VERIFIED": true, "INFERRED": true, "SPECULATIVE": true, "CONTRADICTED": true}
		if !validConfidences[confidence] {
			return mcp.NewToolResultError(fmt.Sprintf("invalid confidence %q: must be one of VERIFIED, INFERRED, SPECULATIVE, CONTRADICTED or empty", confidence)), nil
		}

		input := inv.FindingInput{
			Description:  desc,
			Severity:     inv.Severity(severity),
			Source:       source,
			Confidence:   inv.Confidence(confidence),
			CoverageArea: request.GetString("coverage_area", ""),
			Corrects:     request.GetString("corrects", ""),
		}

		finding, correction, findErr := inv.AddFinding(sessionID, input)
		if findErr != nil {
			return mcp.NewToolResultError(findErr.Error()), nil
		}

		result := map[string]any{
			"finding_id": finding.ID,
			"hint":       "Continue investigating, then call assess to check convergence + coverage.",
		}
		if correction != nil {
			result["correction"] = map[string]any{
				"corrected":      correction.OriginalID,
				"original_claim": correction.OriginalClaim,
				"new_claim":      correction.CorrectedClaim,
			}
		}
		state := inv.GetInvestigation(sessionID)
		return s.marshalGuidedToolResult("investigate", action, state, result)

	case "assess":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for assess"), nil
		}
		assessResult, assessErr := inv.Assess(sessionID)
		if assessErr != nil {
			return mcp.NewToolResultError(assessErr.Error()), nil
		}
		state := inv.GetInvestigation(sessionID)
		return s.marshalGuidedToolResult("investigate", action, state, assessResult)

	case "report":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for report"), nil
		}
		state := inv.GetInvestigation(sessionID)
		if state == nil {
			return mcp.NewToolResultError(fmt.Sprintf("investigation %q not found", sessionID)), nil
		}

		report := inv.GenerateReport(state)
		forceReport := request.GetBool("force", false)

		result := map[string]any{
			"report":            report,
			"findings_count":    len(state.Findings),
			"corrections_count": len(state.Corrections),
			"iterations":        state.Iteration,
			"force":             forceReport,
			"metadata": map[string]any{
				"force": forceReport,
			},
		}

		cwd := request.GetString("cwd", "")
		if cwd != "" {
			if err := validateCWD(cwd); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			savedPath, saveErr := inv.SaveReport(cwd, state.Topic, report)
			if saveErr == nil {
				result["saved_to"] = savedPath
			}
		}

		reportPlan, isReady := s.buildGuidancePlan("investigate", action, state, result)
		if isReady {
			inv.DeleteInvestigation(sessionID)
		}
		return s.marshalGuidedToolResultWithPlan(reportPlan, "investigate", action, state, result)

	case "status":
		sessionID := request.GetString("session_id", "")
		if sessionID == "" {
			return mcp.NewToolResultError("session_id required for status"), nil
		}
		state := inv.GetInvestigation(sessionID)
		if state == nil {
			return mcp.NewToolResultError(fmt.Sprintf("investigation %q not found", sessionID)), nil
		}

		// Brief: session_id, topic, domain, status, finding_count, coverage_progress (FR-2).
		totalAreas := len(state.CoverageAreas)
		checkedAreas := 0
		for _, a := range state.CoverageAreas {
			if state.CoverageChecked[a] {
				checkedAreas++
			}
		}
		coverageProgress := 0.0
		if totalAreas > 0 {
			coverageProgress = float64(checkedAreas) / float64(totalAreas)
		}
		result := map[string]any{
			"session_id":        sessionID,
			"topic":             state.Topic,
			"domain":            state.Domain,
			"status":            "active",
			"finding_count":     len(state.Findings),
			"coverage_progress": coverageProgress,
		}
		whitelist := budget.FieldWhitelist["investigate/status"]
		filtered, _, applyErr := budget.ApplyFields(result, nil, whitelist)
		if applyErr != nil {
			return mcp.NewToolResultError(applyErr.Error()), nil
		}
		return s.marshalGuidedToolResult("investigate", action, state, filtered)

	case "list":
		bp, budgetErr := budget.ParseBudgetParams(request)
		if budgetErr != nil {
			return mcp.NewToolResultError(budgetErr.Error()), nil
		}

		active := inv.ListInvestigations()

		// Build brief rows: session_id, topic, domain, status, finding_count (FR-2).
		type investigationBrief struct {
			SessionID    string `json:"session_id"`
			Topic        string `json:"topic"`
			Domain       string `json:"domain"`
			Status       string `json:"status"`
			FindingCount int    `json:"finding_count"`
		}

		// Collect briefs from active investigations. Domain comes directly from the
		// summary (no per-item GetInvestigation lookup — avoids N+1 on busy servers).
		allBriefs := make([]investigationBrief, len(active))
		for i, s := range active {
			allBriefs[i] = investigationBrief{
				SessionID:    s.SessionID,
				Topic:        s.Topic,
				Domain:       s.Domain,
				Status:       "active",
				FindingCount: s.FindingsCount,
			}
		}

		page, meta := budget.PaginateSingle(allBriefs, bp.Limit, bp.Offset)
		// investigations + pagination are the wrapper keys. fields= filtering applies
		// to per-row fields only; the wrapper is returned as-is so "investigations"
		// (not listed in the per-row whitelist) is not inadvertently dropped.
		result := map[string]any{
			"investigations": page,
			"pagination":     meta,
		}
		return s.marshalGuidedToolResult("investigate", action, nil, result)

	case "recall":
		bp, budgetErr := budget.ParseBudgetParams(request)
		if budgetErr != nil {
			return mcp.NewToolResultError(budgetErr.Error()), nil
		}
		if valErr := budget.ValidateContentBearingFields(
			bp.Fields,
			budget.ContentBearingFields["investigate/recall"],
			bp.IncludeContent,
		); valErr != nil {
			return mcp.NewToolResultError(valErr.Error()), nil
		}

		topic := request.GetString("topic", "")
		if topic == "" {
			return mcp.NewToolResultError("topic required for recall"), nil
		}
		cwd := request.GetString("cwd", "")
		if cwd != "" {
			if err := validateCWD(cwd); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
		} else {
			cwd, _ = os.Getwd()
		}
		recallResult, err := inv.RecallReport(cwd, topic)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("recall error: %v", err)), nil
		}
		if recallResult == nil {
			// Return available topics to help the user
			reports, _ := inv.ListReports(cwd)
			topics := make([]string, 0, len(reports))
			for _, r := range reports {
				topics = append(topics, r.Topic)
			}
			notFoundPayload := map[string]any{
				"found":            false,
				"message":          fmt.Sprintf("No report found matching %q", topic),
				"available_topics": topics,
			}
			return s.marshalGuidedToolResult("investigate", action, nil, notFoundPayload)
		}

		// Brief: session_id, topic, finding_count, content_length; content omitted unless include_content=true (FR-2).
		//
		// NB: for action=recall only, "session_id" carries the persisted report
		// *filename*, not an in-memory investigation ID. Filenames are stable
		// across aimux restarts while investigation IDs are not; using the
		// filename lets callers round-trip recall results without consulting
		// other state. Other investigate actions use the real session ID.
		// Documented for callers in CHANGELOG and the investigate tool description.
		contentLength := len(recallResult.Content)
		payload := map[string]any{
			"found":          true,
			"session_id":     recallResult.Filename, // filename — stable identifier; see doc note above
			"topic":          recallResult.Topic,
			"date":           recallResult.Date,
			"finding_count":  0, // report file doesn't surface count separately
			"content_length": contentLength,
		}
		if bp.IncludeContent {
			payload["content"] = recallResult.Content
		} else if contentLength > 0 {
			meta := budget.BuildTruncationMeta(nil, contentLength,
				"Use investigate(action=recall, topic=..., include_content=true) for full report.")
			if meta.Truncated {
				payload["truncated"] = meta.Truncated
				payload["hint"] = meta.Hint
			}
		}
		whitelist := budget.FieldWhitelist["investigate/recall"]
		filtered, _, applyErr := budget.ApplyFields(payload, bp.Fields, whitelist)
		if applyErr != nil {
			return mcp.NewToolResultError(applyErr.Error()), nil
		}
		return s.marshalGuidedToolResult("investigate", action, nil, filtered)

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
	}
}
