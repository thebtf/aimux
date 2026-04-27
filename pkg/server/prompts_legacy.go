// Package server — legacy prompt handlers.
// These handlers pre-date the skill engine and produce prompts via in-code string building.
// They remain functional alongside the skill-engine-powered prompts registered in prompts.go.
package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/think"
)

// registerPrompts registers the legacy hand-coded MCP prompts.
// New skill-engine prompts are registered by registerSkillPrompts() in prompts.go.
func (s *Server) registerPrompts() {
	// Background execution protocol prompt
	s.mcp.AddPrompt(
		mcp.NewPrompt("background",
			mcp.WithPromptDescription("Step-by-step orchestration for running aimux CLI tasks in background"),
			mcp.WithArgument("task_description",
				mcp.ArgumentDescription("Description of the task to execute"),
			),
		),
		s.handleBackgroundPrompt,
	)

	// aimux-guide: comprehensive decision-making guide for all 13 tools
	s.mcp.AddPrompt(
		mcp.NewPrompt("guide",
			mcp.WithPromptDescription("Complete guide to aimux tools — when and how to use each of the 13 MCP tools, role routing, think patterns, investigation flow, workflows"),
		),
		s.handleGuidePrompt,
	)

	// aimux-investigate: structured investigation protocol with convergence tracking
	s.mcp.AddPrompt(
		mcp.NewPrompt("investigate",
			mcp.WithPromptDescription("Investigation protocol — structured deep analysis with convergence tracking"),
			mcp.WithArgument("topic",
				mcp.ArgumentDescription("What to investigate"),
			),
		),
		s.handleInvestigatePrompt,
	)

	// aimux-workflow: declarative multi-step pipeline builder
	s.mcp.AddPrompt(
		mcp.NewPrompt("workflow",
			mcp.WithPromptDescription("Build declarative multi-step execution pipelines"),
			mcp.WithArgument("goal",
				mcp.ArgumentDescription("What the workflow should accomplish"),
			),
		),
		s.handleWorkflowPrompt,
	)
}

func (s *Server) handleBackgroundPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	taskDesc := ""
	if args := request.Params.Arguments; args != nil {
		if desc, exists := args["task_description"]; exists && desc != "" {
			taskDesc = desc
		}
	}

	var sb strings.Builder
	if taskDesc != "" {
		sb.WriteString(fmt.Sprintf("## Background Task: %s\n\n", taskDesc))
		sb.WriteString("The Layer 5 purge removed background CLI-launching MCP tools on this branch.\n\n")
	} else {
		sb.WriteString("## Background Execution Status\n\n")
	}
	sb.WriteString("### What remains available\n")
	sb.WriteString("- `status(job_id=\"...\")` for existing async job inspection\n")
	sb.WriteString("- `sessions(action=\"list\"|\"health\"|\"info\")` for session and Loom task state\n")
	sb.WriteString("- Think pattern tools for in-process reasoning\n")
	sb.WriteString("- `deepresearch(topic=\"...\")` for synchronous research\n")

	return mcp.NewGetPromptResult(
		"Background execution status",
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleAssistant,
				mcp.NewTextContent(sb.String()),
			),
		},
	), nil
}

func (s *Server) handleGuidePrompt(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	// Fetch live data.
	enabledCLIs := s.registry.EnabledCLIs()
	sessionCount := s.sessions.Count()
	snap := s.metrics.Snapshot()
	thinkPatterns := think.GetAllPatterns()

	var sb strings.Builder

	sb.WriteString("# aimux — AI CLI Multiplexer\n\n")

	// Live status block.
	sb.WriteString("## Current Status\n\n")
	if len(enabledCLIs) == 0 {
		sb.WriteString("**Enabled CLIs:** none detected (run `go build` and probe CLIs)\n")
	} else {
		sb.WriteString(fmt.Sprintf("**Enabled CLIs (%d):** %s\n", len(enabledCLIs), strings.Join(enabledCLIs, ", ")))
	}
	sb.WriteString(fmt.Sprintf("**Active Sessions:** %d\n", sessionCount))
	sb.WriteString(fmt.Sprintf("**Total Requests:** %d | **Error Rate:** %.1f%%\n\n",
		snap.TotalRequests, snap.ErrorRate*100))

	// Tool selection table (static reduced surface).
	sb.WriteString("## Tool Selection — \"I need to...\"\n\n")
	sb.WriteString("| I need to... | Use | Key params |\n")
	sb.WriteString("|---|---|---|\n")
	sb.WriteString("| Check async job status | status | job_id |\n")
	sb.WriteString("| Manage sessions | sessions | action |\n")
	sb.WriteString("| Structured reasoning/analysis | think pattern tool | tool-specific args |\n")
	sb.WriteString("| Deep research via Gemini | deepresearch | topic |\n\n")
	sb.WriteString("| Check/apply binary updates | upgrade | action, mode |\n\n")

	// Dynamic CLI table — only show what is actually enabled.
	sb.WriteString("## Your Available CLIs\n\n")
	if len(enabledCLIs) == 0 {
		sb.WriteString("No CLIs detected on PATH. Install at least one AI CLI (codex, claude, gemini, etc.).\n\n")
	} else {
		sb.WriteString("| CLI | Status |\n")
		sb.WriteString("|-----|--------|\n")
		for _, cli := range enabledCLIs {
			sb.WriteString(fmt.Sprintf("| %s | available |\n", cli))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Notes\n")
	sb.WriteString("Pipeline v5 packages remain in the repository as dormant implementation seams pending the Layer 5 redesign.\n\n")

	// Think patterns — live from registry.
	sb.WriteString(fmt.Sprintf("## Think Patterns (%d registered)\n\n", len(thinkPatterns)))
	sb.WriteString(strings.Join(thinkPatterns, ", "))
	sb.WriteString("\n\n")

	// Anti-patterns (static).
	sb.WriteString("## Anti-Patterns\n")
	sb.WriteString("- DON'T expect exec/agent/workflow tools on this branch — they were purged in Layer 5\n")
	sb.WriteString("- DON'T call think without the pattern-specific tool parameters\n")
	sb.WriteString("- DON'T assume dormant pipeline packages are active runtime surface\n")

	return mcp.NewGetPromptResult(
		"aimux tool guide",
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleAssistant,
				mcp.NewTextContent(sb.String()),
			),
		},
	), nil
}

func (s *Server) handleInvestigatePrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	topic := ""
	if args := request.Params.Arguments; args != nil {
		if t, exists := args["topic"]; exists && t != "" {
			topic = t
		}
	}

	// Without a topic, emit the generic archival guide.
	if topic == "" {
		content := "# aimux Investigation Protocol\n\n" +
			"The live `investigate` MCP tool was removed by the Layer 5 purge on this branch.\n\n" +
			"Use this prompt as an archival checklist only: define the topic, gather evidence, challenge assumptions, and synthesize conclusions with think pattern tools.\n"
		return mcp.NewGetPromptResult(
			"Investigation protocol",
			[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(content))},
		), nil
	}

	allPatterns := think.GetAllPatterns()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Investigation Plan: %s\n\n", topic))
	sb.WriteString("The live `investigate` MCP tool is not available on this branch.\n\n")

	sb.WriteString("### Execution Steps\n\n")
	sb.WriteString("1. Define the exact claim or failure mode.\n")
	sb.WriteString("2. Gather direct evidence from code, logs, tests, or runtime state.\n")
	sb.WriteString("3. Use think pattern tools to challenge assumptions and compare hypotheses.\n")
	sb.WriteString("4. Write the conclusion only after evidence and alternatives are captured.\n\n")

	sb.WriteString("### Cross-Tool Enhancement\n\n")
	sb.WriteString("Useful registered think patterns for investigations:\n\n")
	for _, pattern := range allPatterns {
		if pattern == "critical_thinking" || pattern == "debugging_approach" || pattern == "decision_framework" || pattern == "peer_review" {
			sb.WriteString(fmt.Sprintf("- `%s`\n", pattern))
		}
	}

	return mcp.NewGetPromptResult(
		fmt.Sprintf("Investigation plan: %s", topic),
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleAssistant,
				mcp.NewTextContent(sb.String()),
			),
		},
	), nil
}

func (s *Server) handleWorkflowPrompt(_ context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	goal := ""
	if args := request.Params.Arguments; args != nil {
		if g, exists := args["goal"]; exists && g != "" {
			goal = g
		}
	}

	// Gather live data.
	thinkPatterns := think.GetAllPatterns()

	var sb strings.Builder

	if goal == "" {
		// No goal — emit current branch status.
		sb.WriteString("# aimux Workflow Builder\n\n")
		sb.WriteString("The live `workflow` MCP tool was removed by the Layer 5 purge. Pipeline v5 packages remain dormant in-repo pending redesign.\n\n")
		sb.WriteString(fmt.Sprintf("Registered think patterns (%d): %s\n", len(thinkPatterns), strings.Join(thinkPatterns, ", ")))
		return mcp.NewGetPromptResult(
			"Workflow builder guide",
			[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String()))},
		), nil
	}

	sb.WriteString(fmt.Sprintf("## Workflow for: %s\n\n", goal))
	sb.WriteString("The live `workflow` MCP tool is not exposed on this branch. Treat this prompt as a redesign note only.\n\n")
	sb.WriteString("Suggested manual sequence:\n")
	sb.WriteString("1. Use a think pattern tool to decompose the goal.\n")
	sb.WriteString("2. Use `deepresearch` if external research is needed.\n")
	sb.WriteString("3. Use `sessions` and `status` only for existing daemon state inspection.\n\n")
	sb.WriteString(fmt.Sprintf("Registered think patterns (%d): %s\n", len(thinkPatterns), strings.Join(thinkPatterns, ", ")))

	return mcp.NewGetPromptResult(
		fmt.Sprintf("Workflow: %s", goal),
		[]mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleAssistant,
				mcp.NewTextContent(sb.String()),
			),
		},
	), nil
}

// legacyPromptNames returns the set of prompt names registered by registerPrompts().
// Used by registerSkillPrompts() to detect conflicts.
func legacyPromptNames() map[string]bool {
	return map[string]bool{
		"background":  true,
		"guide":       true,
		"investigate": true,
		"workflow":    true,
	}
}

