package server

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/agents"
	"github.com/thebtf/aimux/pkg/resolve"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func (s *Server) handleAgents(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action, err := request.RequireString("action")
	if err != nil {
		return mcp.NewToolResultError("action is required"), nil
	}

	switch action {
	case "list":
		agentList := s.agentReg.List()
		// Return summaries without full content (content can be 500KB+ total)
		summaries := make([]map[string]any, len(agentList))
		for i, a := range agentList {
			summaries[i] = map[string]any{
				"name":        a.Name,
				"description": a.Description,
				"role":        a.Role,
				"domain":      a.Domain,
				"source":      a.Source,
				"tools":       a.Tools,
			}
		}
		return marshalToolResult(map[string]any{"agents": summaries, "count": len(summaries)})

	case "find":
		query := request.GetString("prompt", "")
		if query == "" {
			return mcp.NewToolResultError("prompt required as search query for find"), nil
		}
		matches := s.agentReg.Find(query)
		return marshalToolResult(map[string]any{"query": query, "matches": matches, "count": len(matches)})

	case "info":
		agentName := request.GetString("agent", "")
		if agentName == "" {
			return mcp.NewToolResultError("agent name required for info"), nil
		}
		agent, agentErr := s.agentReg.Get(agentName)
		if agentErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("agent %q not found", agentName)), nil
		}
		return marshalToolResult(agent)

	case "run":
		prompt := request.GetString("prompt", "")
		if prompt == "" {
			return mcp.NewToolResultError("prompt is required for run"), nil
		}
		cwd := request.GetString("cwd", "")

		// Auto-discover agents from the caller's project directory if it differs
		// from the initial discovery path. Additive — does not remove existing agents.
		if cwd != "" && cwd != s.projectDir {
			s.agentReg.Discover(cwd, "")
		}

		agentName := request.GetString("agent", "")
		var agent *agents.Agent
		if agentName == "" {
			// Return candidate list instead of auto-selecting.
			// The calling LLM knows the task context better than keyword matching.
			candidates := agents.ListCandidates(s.agentReg, prompt, 20)
			return marshalToolResult(map[string]any{
				"action":  "choose_agent",
				"message": "No agent specified. Review the candidates below and call again with agent=<name>.",
				"candidates": candidates,
				"hint":    "Pick the agent whose 'when' description best matches your task. Use the 'agent' tool directly with agent=<name> for fastest execution.",
			})
		} else {
			var agentErr error
			agent, agentErr = s.agentReg.Get(agentName)
			if agentErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("agent %q not found", agentName)), nil
			}
		}

		fullPrompt := agent.Content + "\n\n" + prompt
		role := agent.Role
		if role == "" {
			role = "default"
		}

		// Resolve CLI from agent role
		cli := ""
		pref, resolveErr := s.router.Resolve(role)
		if resolveErr == nil {
			cli = pref.CLI
		}
		if cli == "" {
			cli = "codex" // default executor
		}

		profile, profileErr := s.registry.Get(cli)
		if profileErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("CLI %q not configured for agent role %q", cli, role)), nil
		}

		readOnly := routing.IsAdvisory(role)
		args := types.SpawnArgs{
			CLI:            cli,
			Command:        resolve.CommandBinary(profile.Command.Base),
			Args:           resolve.BuildPromptArgs(profile, pref.Model, pref.ReasoningEffort, readOnly, fullPrompt),
			CWD:            cwd,
			TimeoutSeconds: profile.TimeoutSeconds,
		}

		cb := s.breakers.Get(cli)
		async := request.GetBool("async", false)

		if async {
			if err := s.checkConcurrencyLimit(); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
			job := s.jobs.Create(sess.ID, cli)
			s.log.Info("agents: run agent=%s cli=%s role=%s job=%s", agentName, cli, role, job.ID)
			jobCtx, jobCancel := context.WithCancel(context.Background())
			s.jobs.RegisterCancel(job.ID, jobCancel)
			go s.executeJob(jobCtx, job.ID, sess.ID, role, args, cb, profile.OutputFormat)
			return marshalToolResult(map[string]any{
				"agent":      agentName,
				"job_id":     job.ID,
				"session_id": sess.ID,
				"status":     "running",
			})
		}

		sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
		job := s.jobs.Create(sess.ID, cli)
		s.log.Info("agents: run agent=%s cli=%s role=%s job=%s", agentName, cli, role, job.ID)
		s.executeJob(ctx, job.ID, sess.ID, role, args, cb, profile.OutputFormat)

		j := s.jobs.GetSnapshot(job.ID)
		if j == nil {
			return mcp.NewToolResultError("agent job disappeared"), nil
		}
		if j.Status == types.JobStatusFailed && j.Error != nil {
			return mcp.NewToolResultError(fmt.Sprintf("agent %q failed: %v", agentName, j.Error)), nil
		}

		return marshalToolResult(map[string]any{
			"agent":      agentName,
			"session_id": sess.ID,
			"status":     string(j.Status),
			"content":    j.Content,
		})

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q", action)), nil
	}
}

func (s *Server) handleAgentRun(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.rateLimiter.Allow("agent") {
		return mcp.NewToolResultError("rate limit exceeded — try again shortly"), nil
	}
	agentName, err := request.RequireString("agent")
	if err != nil {
		return mcp.NewToolResultError("agent is required"), nil
	}
	prompt, err := request.RequireString("prompt")
	if err != nil {
		return mcp.NewToolResultError("prompt is required"), nil
	}

	agent, agentErr := s.agentReg.Get(agentName)
	if agentErr != nil {
		available := s.agentReg.List()
		names := make([]string, len(available))
		for i, a := range available {
			names[i] = a.Name
		}
		return mcp.NewToolResultError(fmt.Sprintf("agent %q not found; available: %v", agentName, names)), nil
	}

	// Resolve CLI: explicit param > agent meta > role routing > default
	role := agent.Role
	if role == "" {
		role = "default"
	}

	cli := request.GetString("cli", "")
	if cli == "" {
		if v, ok := agent.Meta["cli"]; ok && v != "" {
			cli = v
		}
	}

	var rolePref types.RolePreference
	if pref, resolveErr := s.router.Resolve(role); resolveErr == nil {
		rolePref = pref
		if cli == "" && pref.CLI != "" {
			cli = pref.CLI
		}
	}
	if cli == "" {
		cli = "codex"
	}

	cwd := request.GetString("cwd", "")
	maxTurns := int(request.GetFloat("max_turns", 0))
	async := request.GetBool("async", false)

	// Agent frontmatter overrides for model, effort, timeout
	model := agent.Model
	effort := agent.Effort
	if rolePref.CLI != "" {
		envKey := "AIMUX_ROLE_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_"))
		hasEnv := os.Getenv(envKey) != ""
		if rolePref.Model != "" && (hasEnv || model == "") {
			model = rolePref.Model
		}
		if rolePref.ReasoningEffort != "" && (hasEnv || effort == "") {
			effort = rolePref.ReasoningEffort
		}
	}
	timeoutSeconds := agent.Timeout
	if ts := int(request.GetFloat("timeout_seconds", 0)); ts > 0 {
		timeoutSeconds = ts
	}

	cliResolver := resolve.NewProfileResolver(s.cfg.CLIProfiles)

	runCfg := agents.RunConfig{
		Agent:    agent,
		CLI:      cli,
		Prompt:   prompt,
		CWD:      cwd,
		MaxTurns: maxTurns,
		Timeout:  timeoutSeconds,
		Model:    model,
		Effort:   effort,
		Executor: s.executor,
		Resolver: cliResolver,
	}

	// T011: wire model-fallback chain from the resolved CLI profile.
	if agentProfile, profileErr := s.registry.Get(cli); profileErr == nil && len(agentProfile.ModelFallback) > 0 {
		runCfg.ModelFallback = agentProfile.ModelFallback
		runCfg.ModelFlag = agentProfile.ModelFlag
		runCfg.CooldownSeconds = agentProfile.CooldownSeconds
		runCfg.CooldownTracker = s.cooldownTracker
	}

	if async {
		if err := s.checkConcurrencyLimit(); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sess := s.sessions.Create(cli, types.SessionModeOnceStateful, cwd)
		job := s.jobs.Create(sess.ID, cli)
		jobCtx, jobCancel := context.WithCancel(context.Background())
		s.jobs.RegisterCancel(job.ID, jobCancel)
		s.sendBusy(job.ID, "agent:"+agentName, agentBusyEstimateMs(timeoutSeconds, maxTurns))

		runCfg.OnOutput = func(cli, line string) {
			s.sendJobProgress(job.ID, line)
		}

		go func() {
			defer s.sendIdle(job.ID)
			s.jobs.StartJob(job.ID, 0)
			s.sessions.Update(sess.ID, func(sess *session.Session) {
				sess.Status = types.SessionStatusRunning
			})
			result, runErr := agents.RunAgent(jobCtx, runCfg)
			if runErr != nil {
				s.jobs.FailJob(job.ID, types.NewExecutorError(runErr.Error(), runErr, "agent_run"))
				s.sessions.Update(sess.ID, func(sess *session.Session) {
					sess.Status = types.SessionStatusFailed
				})
				return
			}
			s.jobs.CompleteJob(job.ID, result.Content, 0)
			s.sessions.Update(sess.ID, func(sess *session.Session) {
				sess.Status = types.SessionStatusCompleted
				sess.Turns = result.Turns
			})
		}()

		return marshalToolResult(map[string]any{
			"agent":      agentName,
			"cli":        cli,
			"job_id":     job.ID,
			"session_id": sess.ID,
			"status":     "running",
		})
	}

	result, runErr := agents.RunAgent(ctx, runCfg)
	if runErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("agent %q failed: %v", agentName, runErr)), nil
	}

	return marshalToolResult(map[string]any{
		"agent":       agentName,
		"cli":         cli,
		"status":      result.Status,
		"turns":       result.Turns,
		"content":     result.Content,
		"duration_ms": result.DurationMS,
		"turn_log":    result.TurnLog,
	})
}
