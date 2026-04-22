package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/guidance/policies"
	"github.com/thebtf/aimux/loom"
	orch "github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/server/budget"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

// validateCWD checks that cwd is a non-empty absolute path that exists on disk
// and contains no control characters that could enable path injection.
func validateCWD(cwd string) error {
	if cwd == "" {
		return fmt.Errorf("cwd must not be empty")
	}
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("cwd must be an absolute path, got %q", cwd)
	}
	// Reject control characters that could be used in injection attacks.
	if strings.ContainsAny(cwd, "\x00\n\r") {
		return fmt.Errorf("cwd contains invalid characters")
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return fmt.Errorf("cwd %q: %w", cwd, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cwd %q is not a directory", cwd)
	}
	return nil
}

// checkMinTwoCLIs returns a ToolResultError when fewer than 2 CLIs are enabled.
// Returns nil when len(enabled) >= 2 so callers can use a single-line guard.
func checkMinTwoCLIs(enabled []string) *mcp.CallToolResult {
	if len(enabled) >= 2 {
		return nil
	}
	availableMsg := "none"
	if len(enabled) == 1 {
		availableMsg = enabled[0]
	}
	return mcp.NewToolResultError(fmt.Sprintf(
		"Requires 2+ CLIs; currently %d available (%s). Cannot run multi-CLI operation.",
		len(enabled), availableMsg))
}

func (s *Server) handleConsensus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, err := request.RequireString("topic")
	if err != nil {
		return mcp.NewToolResultError("topic is required"), nil
	}

	synthesize := request.GetBool("synthesize", false)

	// Resolve participants from role preferences
	enabled := s.registry.EnabledCLIs()
	sort.Strings(enabled)
	if result := checkMinTwoCLIs(enabled); result != nil {
		return result, nil
	}

	async := request.GetBool("async", true)

	params := types.StrategyParams{
		Prompt:   topic,
		CLIs:     enabled[:2], // First 2 enabled CLIs
		MaxTurns: int(request.GetFloat("max_turns", 0)),
		Extra:    map[string]any{"synthesize": synthesize},
	}

	if async && s.loom != nil {
		taskID, loomErr := s.loom.Submit(ctx, loom.TaskRequest{
			WorkerType: loom.WorkerTypeOrchestrator,
			ProjectID:  projectIDFromContext(ctx),
			Prompt:     topic,
			Env:        FilterSensitive(sessionEnvFromContext(ctx)),
			Metadata: map[string]any{
				"strategy":  "consensus",
				"clis":      params.CLIs,
				"max_turns": params.MaxTurns,
				"extra":     params.Extra,
			},
		})
		if loomErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("loom submit: %v", loomErr)), nil
		}
		return marshalToolResult(map[string]any{"job_id": taskID, "status": "running"})
	}

	// Legacy path:
	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create("consensus", types.SessionModeOnceStateful, "")
		job := s.jobs.Create(sess.ID, "consensus")
		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		s.sendBusy(job.ID, "consensus", 0)
		go func() {
			defer s.sendIdle(job.ID)
			s.executeStrategy(jobCtx, job.ID, sess.ID, "consensus", params)
		}()
		return marshalToolResult(map[string]any{"job_id": job.ID, "session_id": sess.ID, "status": "running"})
	}

	bp, budgetErr := budget.ParseBudgetParams(request)
	if budgetErr != nil {
		return mcp.NewToolResultError(budgetErr.Error()), nil
	}
	if valErr := budget.ValidateContentBearingFields(
		bp.Fields, budget.ContentBearingFields["consensus"], bp.IncludeContent,
	); valErr != nil {
		return mcp.NewToolResultError(valErr.Error()), nil
	}

	result, err := s.orchestrator.Execute(ctx, "consensus", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("consensus failed: %v", err)), nil
	}
	consensusState := &policies.ConsensusPolicyInput{
		Synthesize: synthesize,
		Turns:      result.Turns,
		Status:     result.Status,
	}

	// Brief sync path: compact summary + content_length; full transcript on include_content=true (FR-2).
	contentLen := len(result.Content)
	rawPayload := map[string]any{
		"status": result.Status,
		"turns":  result.Turns,
	}
	if bp.IncludeContent {
		rawPayload["content"] = result.Content
		rawPayload["transcript"] = result.Content
	} else {
		rawPayload["content_length"] = contentLen
		meta := budget.BuildTruncationMeta(nil, contentLen,
			"Use consensus(include_content=true) for full transcript.")
		if meta.Truncated {
			rawPayload["truncated"] = meta.Truncated
			rawPayload["hint"] = meta.Hint
		}
	}
	filtered, _, applyErr := budget.ApplyFields(rawPayload, bp.Fields, budget.FieldWhitelist["consensus"])
	if applyErr != nil {
		return mcp.NewToolResultError(applyErr.Error()), nil
	}
	return s.marshalGuidedToolResult("consensus", "", consensusState, filtered)
}

func (s *Server) handleDebate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, err := request.RequireString("topic")
	if err != nil {
		return mcp.NewToolResultError("topic is required"), nil
	}

	synthesize := request.GetBool("synthesize", true)

	enabled := s.registry.EnabledCLIs()
	sort.Strings(enabled)
	if result := checkMinTwoCLIs(enabled); result != nil {
		return result, nil
	}

	async := request.GetBool("async", true)

	params := types.StrategyParams{
		Prompt:   topic,
		CLIs:     enabled[:2],
		MaxTurns: int(request.GetFloat("max_turns", 6)),
		Extra:    map[string]any{"synthesize": synthesize},
	}

	if async && s.loom != nil {
		taskID, loomErr := s.loom.Submit(ctx, loom.TaskRequest{
			WorkerType: loom.WorkerTypeOrchestrator,
			ProjectID:  projectIDFromContext(ctx),
			Prompt:     topic,
			Env:        FilterSensitive(sessionEnvFromContext(ctx)),
			Metadata: map[string]any{
				"strategy":  "debate",
				"clis":      params.CLIs,
				"max_turns": params.MaxTurns,
				"extra":     params.Extra,
			},
		})
		if loomErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("loom submit: %v", loomErr)), nil
		}
		return marshalToolResult(map[string]any{"job_id": taskID, "status": "running"})
	}

	// Legacy path:
	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create("debate", types.SessionModeOnceStateful, "")
		job := s.jobs.Create(sess.ID, "debate")
		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		s.sendBusy(job.ID, "debate", 0)
		go func() {
			defer s.sendIdle(job.ID)
			s.executeStrategy(jobCtx, job.ID, sess.ID, "debate", params)
		}()
		return marshalToolResult(map[string]any{"job_id": job.ID, "session_id": sess.ID, "status": "running"})
	}

	bpDebate, budgetErrDebate := budget.ParseBudgetParams(request)
	if budgetErrDebate != nil {
		return mcp.NewToolResultError(budgetErrDebate.Error()), nil
	}
	if valErr := budget.ValidateContentBearingFields(
		bpDebate.Fields, budget.ContentBearingFields["debate"], bpDebate.IncludeContent,
	); valErr != nil {
		return mcp.NewToolResultError(valErr.Error()), nil
	}

	result, err := s.orchestrator.Execute(ctx, "debate", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("debate failed: %v", err)), nil
	}
	debateState := &policies.DebatePolicyInput{
		Turns:      result.Turns,
		MaxTurns:   params.MaxTurns,
		Synthesize: synthesize,
		Status:     result.Status,
	}

	// Brief sync path: compact summary + content_length; full transcript on include_content=true (FR-2).
	debateContentLen := len(result.Content)
	debatePayload := map[string]any{
		"status": result.Status,
		"turns":  result.Turns,
	}
	if bpDebate.IncludeContent {
		debatePayload["content"] = result.Content
		debatePayload["transcript"] = result.Content
	} else {
		debatePayload["content_length"] = debateContentLen
		debateMeta := budget.BuildTruncationMeta(nil, debateContentLen,
			"Use debate(include_content=true) for full transcript.")
		if debateMeta.Truncated {
			debatePayload["truncated"] = debateMeta.Truncated
			debatePayload["hint"] = debateMeta.Hint
		}
	}
	debateFiltered, _, debateApplyErr := budget.ApplyFields(debatePayload, bpDebate.Fields, budget.FieldWhitelist["debate"])
	if debateApplyErr != nil {
		return mcp.NewToolResultError(debateApplyErr.Error()), nil
	}
	return s.marshalGuidedToolResult("debate", "", debateState, debateFiltered)
}

func (s *Server) handleDialog(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prompt, err := request.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError("prompt is required"), nil
	}

	// Parse + validate budget params BEFORE any orchestrator work so that invalid
	// requests (bad fields, missing include_content, etc.) return errors without
	// spawning CLIs, creating job history, or mutating session state.
	bpDialog, budgetErrDialog := budget.ParseBudgetParams(request)
	if budgetErrDialog != nil {
		return mcp.NewToolResultError(budgetErrDialog.Error()), nil
	}
	if valErr := budget.ValidateContentBearingFields(
		bpDialog.Fields, budget.ContentBearingFields["dialog"], bpDialog.IncludeContent,
	); valErr != nil {
		return mcp.NewToolResultError(valErr.Error()), nil
	}

	enabled := s.registry.EnabledCLIs()
	sort.Strings(enabled)
	if result := checkMinTwoCLIs(enabled); result != nil {
		return result, nil
	}

	sessionID := request.GetString("session_id", "")
	cwd := request.GetString("cwd", "")

	params := types.StrategyParams{
		Prompt:   prompt,
		CLIs:     enabled[:2],
		MaxTurns: int(request.GetFloat("max_turns", 6)),
		CWD:      cwd,
	}

	// Session resume: load prior turn history from existing session job.
	if sessionID != "" {
		existing := s.sessions.Get(sessionID)
		if existing == nil {
			return mcp.NewToolResultError(fmt.Sprintf("session %q not found", sessionID)), nil
		}
		if cwd == "" {
			params.CWD = existing.CWD
		}
		// Find prior turn history from the most recent completed job for this session.
		priorTurns := s.findDialogTurnHistory(existing.ID)
		if len(priorTurns) > 0 {
			params.Extra = map[string]any{"prior_turns": priorTurns}
		}
		s.log.Info("dialog: resuming session=%s with %d prior bytes of turn history", sessionID, len(priorTurns))
	}

	// Create or reuse session for persistence.
	var sess *session.Session
	if sessionID != "" {
		sess = s.sessions.Get(sessionID)
	}
	if sess == nil {
		sess = s.sessions.Create("dialog", types.SessionModeOnceStateful, params.CWD)
		s.sessions.Update(sess.ID, func(ss *session.Session) {
			ss.Status = types.SessionStatusRunning
		})
	}

	result, err := s.orchestrator.Execute(ctx, "dialog", params)
	if err != nil {
		s.sessions.Update(sess.ID, func(ss *session.Session) {
			ss.Status = types.SessionStatusFailed
		})
		return mcp.NewToolResultError(fmt.Sprintf("dialog failed: %v", err)), nil
	}

	// Persist turn history in a job so it can be recalled on resume.
	// Job content stores the JSON turn history; full dialog text is returned directly.
	job := s.jobs.Create(sess.ID, "dialog")
	s.jobs.StartJob(job.ID, 0)
	turnContent := ""
	if len(result.TurnHistory) > 0 {
		turnContent = string(result.TurnHistory)
	}
	s.jobs.CompleteJob(job.ID, turnContent, 0)

	s.sessions.Update(sess.ID, func(ss *session.Session) {
		ss.Status = types.SessionStatusCompleted
		ss.Turns = result.Turns
	})

	// Brief sync path: compact summary + content_length; full transcript on include_content=true (FR-2).
	// bpDialog was parsed + validated at handler entry (above EnabledCLIs check).
	dialogContentLen := len(result.Content)
	dialogPayload := map[string]any{
		"session_id":   sess.ID,
		"status":       result.Status,
		"turns":        result.Turns,
		"participants": result.Participants,
	}
	if bpDialog.IncludeContent {
		dialogPayload["content"] = result.Content
		dialogPayload["transcript"] = result.Content
	} else {
		dialogPayload["content_length"] = dialogContentLen
		dialogMeta := budget.BuildTruncationMeta(nil, dialogContentLen,
			"Use dialog(session_id=..., include_content=true) for full transcript.")
		if dialogMeta.Truncated {
			dialogPayload["truncated"] = dialogMeta.Truncated
			dialogPayload["hint"] = dialogMeta.Hint
		}
	}
	dialogState := &policies.DialogPolicyInput{
		SessionID:    sess.ID,
		Turns:        result.Turns,
		Status:       result.Status,
		Participants: result.Participants,
	}
	dialogFiltered, _, dialogApplyErr := budget.ApplyFields(dialogPayload, bpDialog.Fields, budget.FieldWhitelist["dialog"])
	if dialogApplyErr != nil {
		return mcp.NewToolResultError(dialogApplyErr.Error()), nil
	}
	return s.marshalGuidedToolResult("dialog", "", dialogState, dialogFiltered)
}

// findDialogTurnHistory scans jobs for the most recent dialog turn history
// stored as JSON in job.Content for the given session.
func (s *Server) findDialogTurnHistory(sessionID string) []byte {
	jobs := s.jobs.ListBySession(sessionID)
	// Walk in reverse to find the most recent completed dialog job with content.
	for i := len(jobs) - 1; i >= 0; i-- {
		j := jobs[i]
		if j.Status == types.JobStatusCompleted && j.Content != "" {
			return []byte(j.Content)
		}
	}
	return nil
}

func (s *Server) handleAudit(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cwd := request.GetString("cwd", "")
	if err := validateCWD(cwd); err != nil {
		return mcp.NewToolResultError("invalid cwd: " + err.Error()), nil
	}
	mode := request.GetString("mode", "standard")
	async := request.GetBool("async", true)

	params := types.StrategyParams{
		Prompt: fmt.Sprintf("Audit codebase at %q", cwd),
		CWD:    cwd,
		Extra: map[string]any{
			"mode":              mode,
			"parallel_scanners": s.cfg.Server.Audit.ParallelScanners,
			"scanner_role":      s.cfg.Server.Audit.ScannerRole,
			"validator_role":    s.cfg.Server.Audit.ValidatorRole,
		},
	}

	if async && s.loom != nil {
		taskID, loomErr := s.loom.Submit(ctx, loom.TaskRequest{
			WorkerType: loom.WorkerTypeOrchestrator,
			ProjectID:  projectIDFromContext(ctx),
			Prompt:     params.Prompt,
			CWD:        cwd,
			Env:        FilterSensitive(sessionEnvFromContext(ctx)),
			Metadata: map[string]any{
				"strategy": "audit",
				"extra":    params.Extra,
			},
		})
		if loomErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("loom submit: %v", loomErr)), nil
		}
		return marshalToolResult(map[string]any{"job_id": taskID, "status": "running"})
	}

	// Legacy path:
	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create("audit", types.SessionModeOnceStateless, cwd)
		job := s.jobs.Create(sess.ID, "audit")
		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		s.sendBusy(job.ID, "audit", 0)
		go func() {
			defer s.sendIdle(job.ID)
			s.jobs.StartJob(job.ID, 0)
			result, stratErr := s.orchestrator.Execute(jobCtx, "audit", params)
			if stratErr != nil {
				s.jobs.FailJob(job.ID, types.NewExecutorError(stratErr.Error(), stratErr, ""))
				return
			}
			s.jobs.CompleteJob(job.ID, result.Content, 0)
		}()
		return marshalToolResult(map[string]any{"job_id": job.ID, "status": "running"})
	}

	bpAudit, budgetErrAudit := budget.ParseBudgetParams(request)
	if budgetErrAudit != nil {
		return mcp.NewToolResultError(budgetErrAudit.Error()), nil
	}
	if valErr := budget.ValidateContentBearingFields(
		bpAudit.Fields, budget.ContentBearingFields["audit"], bpAudit.IncludeContent,
	); valErr != nil {
		return mcp.NewToolResultError(valErr.Error()), nil
	}

	result, err := s.orchestrator.Execute(ctx, "audit", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("audit failed: %v", err)), nil
	}

	// Brief sync path: compact summary + content_length; full output on include_content=true (FR-2).
	auditContentLen := len(result.Content)
	auditPayload := map[string]any{
		"status": result.Status,
		"turns":  result.Turns,
	}
	if bpAudit.IncludeContent {
		auditPayload["content"] = result.Content
		auditPayload["transcript"] = result.Content
	} else {
		auditPayload["content_length"] = auditContentLen
		auditMeta := budget.BuildTruncationMeta(nil, auditContentLen,
			"Use audit(cwd=..., async=false, include_content=true) for full output.")
		if auditMeta.Truncated {
			auditPayload["truncated"] = auditMeta.Truncated
			auditPayload["hint"] = auditMeta.Hint
		}
	}
	auditFiltered, _, auditApplyErr := budget.ApplyFields(auditPayload, bpAudit.Fields, budget.FieldWhitelist["audit"])
	if auditApplyErr != nil {
		return mcp.NewToolResultError(auditApplyErr.Error()), nil
	}
	return marshalToolResult(auditFiltered)
}

// handleWorkflow executes a declarative multi-step pipeline as a single MCP call.
func (s *Server) handleWorkflow(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := request.GetString("name", "workflow")
	stepsJSON, err := request.RequireString("steps")
	if err != nil {
		return mcp.NewToolResultError("steps is required"), nil
	}
	input := request.GetString("input", "")
	async := request.GetBool("async", true)

	// Parse steps from JSON array string
	var steps []orch.WorkflowStep
	if err := json.Unmarshal([]byte(stepsJSON), &steps); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid steps JSON: %v", err)), nil
	}

	def := orch.WorkflowDefinition{
		Name:  name,
		Steps: steps,
		Input: input,
	}
	defJSON, err := json.Marshal(def)
	if err != nil {
		return mcp.NewToolResultError("internal error: failed to marshal workflow definition"), nil
	}

	params := types.StrategyParams{
		Extra: map[string]any{
			"workflow": string(defJSON),
		},
	}

	if async && s.loom != nil {
		taskID, loomErr := s.loom.Submit(ctx, loom.TaskRequest{
			WorkerType: loom.WorkerTypeOrchestrator,
			ProjectID:  projectIDFromContext(ctx),
			Env:        FilterSensitive(sessionEnvFromContext(ctx)),
			Metadata: map[string]any{
				"strategy": "workflow",
				"extra":    params.Extra,
			},
		})
		if loomErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("loom submit: %v", loomErr)), nil
		}
		return marshalToolResult(map[string]any{"job_id": taskID, "status": "running"})
	}

	// Legacy path:
	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create("workflow", types.SessionModeOnceStateless, "")
		job := s.jobs.Create(sess.ID, "workflow")
		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		s.sendBusy(job.ID, "workflow", 0)
		go func() {
			defer s.sendIdle(job.ID)
			s.jobs.StartJob(job.ID, 0)
			result, stratErr := s.orchestrator.Execute(jobCtx, "workflow", params)
			if stratErr != nil {
				s.jobs.FailJob(job.ID, types.NewExecutorError(stratErr.Error(), stratErr, ""))
				return
			}
			s.jobs.CompleteJob(job.ID, result.Content, 0)
		}()
		return marshalToolResult(map[string]any{"job_id": job.ID, "status": "running"})
	}

	bpWorkflow, budgetErrWorkflow := budget.ParseBudgetParams(request)
	if budgetErrWorkflow != nil {
		return mcp.NewToolResultError(budgetErrWorkflow.Error()), nil
	}
	if valErr := budget.ValidateContentBearingFields(
		bpWorkflow.Fields, budget.ContentBearingFields["workflow"], bpWorkflow.IncludeContent,
	); valErr != nil {
		return mcp.NewToolResultError(valErr.Error()), nil
	}

	result, err := s.orchestrator.Execute(ctx, "workflow", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("workflow failed: %v", err)), nil
	}

	// Brief sync path: compact summary + content_length; full output on include_content=true (FR-2).
	wfContentLen := len(result.Content)
	wfPayload := map[string]any{
		"status": result.Status,
		"turns":  result.Turns,
	}
	if bpWorkflow.IncludeContent {
		wfPayload["content"] = result.Content
		wfPayload["transcript"] = result.Content
	} else {
		wfPayload["content_length"] = wfContentLen
		wfMeta := budget.BuildTruncationMeta(nil, wfContentLen,
			"Use workflow(steps=..., include_content=true) for full output.")
		if wfMeta.Truncated {
			wfPayload["truncated"] = wfMeta.Truncated
			wfPayload["hint"] = wfMeta.Hint
		}
	}
	workflowState := buildWorkflowPolicyInput(name, result)
	wfFiltered, _, wfApplyErr := budget.ApplyFields(wfPayload, bpWorkflow.Fields, budget.FieldWhitelist["workflow"])
	if wfApplyErr != nil {
		return mcp.NewToolResultError(wfApplyErr.Error()), nil
	}
	return s.marshalGuidedToolResult("workflow", "", workflowState, wfFiltered)
}

// buildWorkflowPolicyInput derives a WorkflowPolicyInput from the raw strategy result.
func buildWorkflowPolicyInput(name string, result *types.StrategyResult) *policies.WorkflowPolicyInput {
	input := &policies.WorkflowPolicyInput{
		Name:       name,
		TotalSteps: result.Turns,
		Status:     result.Status,
	}

	wfResult, ok := result.Extra["workflow"].(orch.WorkflowResult)
	if !ok {
		return input
	}

	completed := 0
	failedAt := 0
	for i, step := range wfResult.Steps {
		switch step.Status {
		case "completed":
			completed++
		case "failed":
			if failedAt == 0 {
				failedAt = i + 1
			}
		}
	}
	input.TotalSteps = len(wfResult.Steps)
	input.CompletedSteps = completed
	input.FailedAtStep = failedAt
	return input
}

// executeStrategy runs an orchestrator strategy in background and updates job/session state.
func (s *Server) executeStrategy(ctx context.Context, jobID, sessionID, strategyName string, params types.StrategyParams) {
	s.jobs.StartJob(jobID, 0)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusRunning
	})

	result, err := s.orchestrator.Execute(ctx, strategyName, params)
	if err != nil {
		s.jobs.FailJob(jobID, types.NewExecutorError(err.Error(), err, ""))
		s.sessions.Update(sessionID, func(sess *session.Session) {
			sess.Status = types.SessionStatusFailed
		})
		return
	}

	data, _ := json.Marshal(result)
	s.jobs.CompleteJob(jobID, string(data), 0)
	s.sessions.Update(sessionID, func(sess *session.Session) {
		sess.Status = types.SessionStatusCompleted
		sess.Turns += result.Turns
	})
}
