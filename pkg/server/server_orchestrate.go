package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/guidance/policies"
	orch "github.com/thebtf/aimux/pkg/orchestrator"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func (s *Server) handleConsensus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("consensus") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
	topic, err := request.RequireString("topic")
	if err != nil {
		return mcp.NewToolResultError("topic is required"), nil
	}

	synthesize := request.GetBool("synthesize", false)

	// Resolve participants from role preferences
	enabled := s.registry.EnabledCLIs()
	if len(enabled) < 2 {
		return mcp.NewToolResultError("consensus requires at least 2 CLIs"), nil
	}

	async := request.GetBool("async", true)

	params := types.StrategyParams{
		Prompt:   topic,
		CLIs:     enabled[:2], // First 2 enabled CLIs
		MaxTurns: int(request.GetFloat("max_turns", 0)),
		Extra:    map[string]any{"synthesize": synthesize},
	}

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

	result, err := s.orchestrator.Execute(ctx, "consensus", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("consensus failed: %v", err)), nil
	}
	consensusState := &policies.ConsensusPolicyInput{
		Synthesize: synthesize,
		Turns:      result.Turns,
		Status:     result.Status,
	}
	return s.marshalGuidedToolResult("consensus", "", consensusState, result)
}

func (s *Server) handleDebate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("debate") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
	topic, err := request.RequireString("topic")
	if err != nil {
		return mcp.NewToolResultError("topic is required"), nil
	}

	synthesize := request.GetBool("synthesize", true)

	enabled := s.registry.EnabledCLIs()
	if len(enabled) < 2 {
		return mcp.NewToolResultError("debate requires at least 2 CLIs"), nil
	}

	async := request.GetBool("async", true)

	params := types.StrategyParams{
		Prompt:   topic,
		CLIs:     enabled[:2],
		MaxTurns: int(request.GetFloat("max_turns", 6)),
		Extra:    map[string]any{"synthesize": synthesize},
	}

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
	return s.marshalGuidedToolResult("debate", "", debateState, result)
}

func (s *Server) handleDialog(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("dialog") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
	prompt, err := request.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError("prompt is required"), nil
	}

	enabled := s.registry.EnabledCLIs()
	if len(enabled) < 2 {
		return mcp.NewToolResultError("dialog requires at least 2 CLIs"), nil
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

	rawPayload := map[string]any{
		"session_id":   sess.ID,
		"status":       result.Status,
		"turns":        result.Turns,
		"content":      result.Content,
		"participants": result.Participants,
	}
	dialogState := &policies.DialogPolicyInput{
		SessionID:    sess.ID,
		Turns:        result.Turns,
		Status:       result.Status,
		Participants: result.Participants,
	}
	return s.marshalGuidedToolResult("dialog", "", dialogState, rawPayload)
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
	if !s.rateLimiter.Allow("audit") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
	cwd := request.GetString("cwd", "")
	mode := request.GetString("mode", "standard")
	async := request.GetBool("async", true)

	params := types.StrategyParams{
		Prompt: fmt.Sprintf("Audit codebase at %s", cwd),
		CWD:    cwd,
		Extra: map[string]any{
			"mode":              mode,
			"parallel_scanners": s.cfg.Server.Audit.ParallelScanners,
			"scanner_role":      s.cfg.Server.Audit.ScannerRole,
			"validator_role":    s.cfg.Server.Audit.ValidatorRole,
		},
	}

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

	result, err := s.orchestrator.Execute(ctx, "audit", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("audit failed: %v", err)), nil
	}
	return marshalToolResult(result)
}

// handleWorkflow executes a declarative multi-step pipeline as a single MCP call.
func (s *Server) handleWorkflow(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("workflow") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
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

	result, err := s.orchestrator.Execute(ctx, "workflow", params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("workflow failed: %v", err)), nil
	}
	workflowState := buildWorkflowPolicyInput(name, result)
	return s.marshalGuidedToolResult("workflow", "", workflowState, result)
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
