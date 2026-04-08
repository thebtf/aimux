// Package server — legacy prompt handlers.
// These handlers pre-date the skill engine and produce prompts via in-code string building.
// They remain functional alongside the skill-engine-powered prompts registered in prompts.go.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	inv "github.com/thebtf/aimux/pkg/investigate"
	"github.com/thebtf/aimux/pkg/think"
)

// registerPrompts registers the legacy hand-coded MCP prompts.
// New skill-engine prompts are registered by registerSkillPrompts() in prompts.go.
func (s *Server) registerPrompts() {
	// Background execution protocol prompt
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-background",
			mcp.WithPromptDescription("Step-by-step orchestration for running aimux CLI tasks in background"),
			mcp.WithArgument("task_description",
				mcp.ArgumentDescription("Description of the task to execute"),
			),
		),
		s.handleBackgroundPrompt,
	)

	// aimux-guide: comprehensive decision-making guide for all 13 tools
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-guide",
			mcp.WithPromptDescription("Complete guide to aimux tools — when and how to use each of the 13 MCP tools, role routing, think patterns, investigation flow, workflows"),
		),
		s.handleGuidePrompt,
	)

	// aimux-investigate: structured investigation protocol with convergence tracking
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-investigate",
			mcp.WithPromptDescription("Investigation protocol — structured deep analysis with convergence tracking"),
			mcp.WithArgument("topic",
				mcp.ArgumentDescription("What to investigate"),
			),
		),
		s.handleInvestigatePrompt,
	)

	// aimux-workflow: declarative multi-step pipeline builder
	s.mcp.AddPrompt(
		mcp.NewPrompt("aimux-workflow",
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

	// Analyze task keywords to recommend the best role.
	recommendedRole := "coding"
	roleReason := "general implementation tasks default to the coding role"
	if taskDesc != "" {
		lower := strings.ToLower(taskDesc)
		switch {
		case strings.Contains(lower, "review") || strings.Contains(lower, "audit") || strings.Contains(lower, "analyze"):
			recommendedRole = "codereview"
			roleReason = "task involves reviewing or analyzing — codereview role activates the best review CLI"
		case strings.Contains(lower, "security") || strings.Contains(lower, "vuln") || strings.Contains(lower, "owasp"):
			recommendedRole = "secaudit"
			roleReason = "security keyword detected — secaudit role activates OWASP-aware CLIs"
		case strings.Contains(lower, "test") || strings.Contains(lower, "spec") || strings.Contains(lower, "coverage"):
			recommendedRole = "testgen"
			roleReason = "testing keyword detected — testgen role activates test-specialized CLIs"
		case strings.Contains(lower, "plan") || strings.Contains(lower, "design") || strings.Contains(lower, "architect"):
			recommendedRole = "planner"
			roleReason = "planning/design keyword detected — planner role activates architecture-aware CLIs"
		case strings.Contains(lower, "debug") || strings.Contains(lower, "bug") || strings.Contains(lower, "fix") || strings.Contains(lower, "crash"):
			recommendedRole = "debug"
			roleReason = "debug/fix keyword detected — debug role activates tracing-capable CLIs"
		case strings.Contains(lower, "refactor") || strings.Contains(lower, "cleanup") || strings.Contains(lower, "reorganize"):
			recommendedRole = "refactor"
			roleReason = "refactoring keyword detected — refactor role targets structure-preserving CLIs"
		case strings.Contains(lower, "research") || strings.Contains(lower, "investigate") || strings.Contains(lower, "explore"):
			recommendedRole = "analyze"
			roleReason = "research/exploration keyword detected — analyze role activates Gemini's long-context reasoning"
		}
	}

	var sb strings.Builder
	if taskDesc != "" {
		sb.WriteString(fmt.Sprintf("## Background Task: %s\n\n", taskDesc))
		sb.WriteString("### Recommended Execution\n\n")
		sb.WriteString(fmt.Sprintf("```\nexec(prompt=%q, role=%q, async=true)\n```\n\n", taskDesc, recommendedRole))
		sb.WriteString("Then poll for completion:\n")
		sb.WriteString("```\nstatus(job_id=\"<from exec response>\")\n```\n\n")
		sb.WriteString(fmt.Sprintf("### Why `%s` role?\n%s.\n\n", recommendedRole, roleReason))
		sb.WriteString("### Role Reference\n")
	} else {
		sb.WriteString("## Background Execution Protocol\n\n")
		sb.WriteString("Use `exec` with `async=true` for any task that may take >30s.\n\n")
		sb.WriteString("```\nexec(prompt=\"<your task>\", role=\"<role>\", async=true)\n```\n\n")
		sb.WriteString("Poll with:\n```\nstatus(job_id=\"<from exec response>\")\n```\n\n")
		sb.WriteString("### Roles\n")
	}
	sb.WriteString("| Role | Best for |\n")
	sb.WriteString("|------|----------|\n")
	sb.WriteString("| coding | Implementation, new features, boilerplate |\n")
	sb.WriteString("| codereview | Code review, analysis, critique |\n")
	sb.WriteString("| debug | Bug tracing, crash analysis |\n")
	sb.WriteString("| secaudit | Security audit, OWASP, vulnerability review |\n")
	sb.WriteString("| analyze | Research, exploration, holistic analysis |\n")
	sb.WriteString("| refactor | Refactoring, cleanup, reorganization |\n")
	sb.WriteString("| testgen | Test generation, coverage improvement |\n")
	sb.WriteString("| planner | Planning, design, architecture decisions |\n")
	sb.WriteString("| thinkdeep | Deep analysis, difficult reasoning |\n")

	return mcp.NewGetPromptResult(
		"Background execution protocol",
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

	// Tool selection table (static — tools don't change at runtime).
	sb.WriteString("## Tool Selection — \"I need to...\"\n\n")
	sb.WriteString("| I need to... | Use | Key params |\n")
	sb.WriteString("|---|---|---|\n")
	sb.WriteString("| Run a prompt on an AI CLI | exec | prompt, role, cli, async |\n")
	sb.WriteString("| Get consensus from multiple models | consensus | topic, synthesize |\n")
	sb.WriteString("| Have models debate a decision | debate | topic, max_turns |\n")
	sb.WriteString("| Multi-turn discussion between CLIs | dialog | prompt, max_turns |\n")
	sb.WriteString("| Structured reasoning/analysis | think | pattern (see below) |\n")
	sb.WriteString("| Deep investigation with tracking | investigate | action, topic, domain |\n")
	sb.WriteString("| Run a codebase audit | audit | cwd, mode (quick/standard/deep) |\n")
	sb.WriteString("| Execute a project agent | agent | agent (name), prompt |\n")
	sb.WriteString("| Chain multiple steps | workflow | steps (JSON), input |\n")
	sb.WriteString("| Check async job status | status | job_id |\n")
	sb.WriteString("| Manage sessions | sessions | action |\n")
	sb.WriteString("| Discover available agents | agents | action (list/find) |\n")
	sb.WriteString("| Deep research via Gemini | deepresearch | topic |\n\n")

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

	// Roles (static mapping).
	sb.WriteString("## Roles (exec tool)\n")
	sb.WriteString("coding → codex (code generation, TDD)\n")
	sb.WriteString("codereview → gemini (code review, analysis)\n")
	sb.WriteString("debug → codex (debugging, tracing)\n")
	sb.WriteString("secaudit → codex (security audit, OWASP)\n")
	sb.WriteString("analyze → gemini (holistic analysis)\n")
	sb.WriteString("refactor → codex (refactoring)\n")
	sb.WriteString("testgen → codex (test generation)\n")
	sb.WriteString("docgen → codex (documentation)\n")
	sb.WriteString("planner → codex (planning, architecture)\n")
	sb.WriteString("thinkdeep → codex (deep analysis)\n\n")

	// Think patterns — live from registry.
	sb.WriteString(fmt.Sprintf("## Think Patterns (%d registered)\n\n", len(thinkPatterns)))
	sb.WriteString(strings.Join(thinkPatterns, ", "))
	sb.WriteString("\n\n")

	// Investigation flow (static — protocol doesn't change).
	sb.WriteString("## Investigation Flow\n")
	sb.WriteString("1. `investigate(action=\"start\", topic=\"...\", domain=\"auto\")` → session_id + coverage areas\n")
	sb.WriteString("2. `investigate(action=\"finding\", session_id, description, source, severity, confidence)` × N\n")
	sb.WriteString("3. `investigate(action=\"assess\", session_id)` → convergence, coverage, recommendation\n")
	sb.WriteString("4. `investigate(action=\"report\", session_id, cwd)` → saved markdown report\n")
	sb.WriteString("5. `investigate(action=\"recall\", topic=\"...\")` → find past reports\n\n")
	sb.WriteString("Domains: generic, debugging, security, performance, architecture, research\n\n")

	// Workflow example (static).
	sb.WriteString("## Workflow Example\n")
	sb.WriteString("```json\n{\"steps\": [\n")
	sb.WriteString("  {\"id\": \"analyze\", \"tool\": \"exec\", \"params\": {\"role\": \"analyze\", \"prompt\": \"{{input}}\"}},\n")
	sb.WriteString("  {\"id\": \"review\", \"tool\": \"think\", \"params\": {\"pattern\": \"peer_review\", \"artifact\": \"{{analyze.content}}\"}},\n")
	sb.WriteString("  {\"id\": \"fix\", \"tool\": \"exec\", \"params\": {\"role\": \"coding\", \"prompt\": \"Fix: {{review.content}}\"}, \"condition\": \"{{review.content}} contains 'revision'\"}\n")
	sb.WriteString("]}\n```\n\n")

	// Anti-patterns (static).
	sb.WriteString("## Anti-Patterns\n")
	sb.WriteString("- DON'T specify cli= when role= is enough — let routing pick the best CLI\n")
	sb.WriteString("- DON'T use sync exec for tasks >30s — use async=true\n")
	sb.WriteString("- DON'T skip investigate for complex bugs — jumping to fix wastes time\n")
	sb.WriteString("- DON'T call think without a pattern — every call needs pattern= param\n")
	sb.WriteString("- DON'T run consensus with 1 CLI — needs 2+ for meaningful comparison\n")

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

	// Without a topic, emit the generic protocol guide.
	if topic == "" {
		content := "# aimux Investigation Protocol\n\n" +
			"Provide a `topic` argument to get a concrete, ready-to-execute investigation plan.\n\n" +
			"## Flow\n" +
			"1. `investigate(action=\"start\", topic=\"...\", domain=\"auto\")` → session_id\n" +
			"2. `investigate(action=\"finding\", session_id=\"...\", description=\"...\", source=\"...\", severity=\"P0-P3\", confidence=\"VERIFIED\")` × N\n" +
			"3. `investigate(action=\"assess\", session_id=\"...\")` → convergence decision\n" +
			"4. `investigate(action=\"report\", session_id=\"...\", cwd=\"/project\")` → report file\n\n" +
			"**Domains (auto-detected):** generic, debugging, security, performance, architecture, research\n"
		return mcp.NewGetPromptResult(
			"Investigation protocol",
			[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(content))},
		), nil
	}

	// Auto-detect domain.
	domain := inv.AutoDetectDomain(topic)
	domainAlgo := inv.GetDomain(domain)

	// List related past reports.
	cwd, _ := os.Getwd()
	pastReports, _ := inv.ListReports(cwd)
	topicLower := strings.ToLower(topic)
	var relatedReports []string
	for _, r := range pastReports {
		if strings.Contains(strings.ToLower(r.Topic), topicLower) ||
			strings.Contains(strings.ToLower(r.Filename), strings.ReplaceAll(topicLower, " ", "-")) {
			relatedReports = append(relatedReports, fmt.Sprintf("- %s (%s, %d bytes)", r.Topic, r.Date, r.Size))
		}
		if len(relatedReports) >= 5 {
			break
		}
	}

	// Get think patterns for domain-specific recommendations.
	allPatterns := think.GetAllPatterns()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Investigation Plan: %s\n\n", topic))
	sb.WriteString(fmt.Sprintf("**Domain:** %s (auto-detected from topic keywords)\n", domain))
	sb.WriteString(fmt.Sprintf("**Description:** %s\n\n", domainAlgo.Description))

	sb.WriteString("**Coverage Areas:**\n")
	for _, area := range domainAlgo.CoverageAreas {
		sb.WriteString(fmt.Sprintf("- %s\n", area))
	}
	sb.WriteString("\n")

	if len(relatedReports) > 0 {
		sb.WriteString("**Related Past Reports:**\n")
		for _, r := range relatedReports {
			sb.WriteString(r + "\n")
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("**Related Past Reports:** none found\n\n")
	}

	sb.WriteString("### Execution Steps\n\n")
	sb.WriteString("**1. Start investigation:**\n")
	sb.WriteString(fmt.Sprintf("```\ninvestigate(action=\"start\", topic=%q, domain=%q)\n```\n\n", topic, domain))

	sb.WriteString("**2. Investigate each coverage area systematically:**\n\n")
	for _, area := range domainAlgo.CoverageAreas {
		method := domainAlgo.Methods[area]
		if method == "" {
			method = "Investigate this area thoroughly."
		}
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", area, method))
		sb.WriteString(fmt.Sprintf("  ```\n  investigate(action=\"finding\", session_id=\"<from step 1>\", description=\"<your finding>\", source=\"<file:line or tool output>\", severity=\"P0-P3\", confidence=\"VERIFIED\", coverage_area=%q)\n  ```\n\n", area))
	}

	sb.WriteString("**3. After 5+ findings, assess convergence:**\n")
	sb.WriteString("```\ninvestigate(action=\"assess\", session_id=\"<id>\")\n```\n")
	sb.WriteString("→ If `CONTINUE`: investigate more areas\n")
	sb.WriteString("→ If `COMPLETE`: generate report\n\n")

	sb.WriteString("**4. Generate report:**\n")
	sb.WriteString(fmt.Sprintf("```\ninvestigate(action=\"report\", session_id=\"<id>\", cwd=%q)\n```\n\n", cwd))

	sb.WriteString("### Domain-Specific Guidance\n\n")

	if len(domainAlgo.AntiPatterns) > 0 {
		sb.WriteString("**Anti-patterns to avoid:**\n")
		for _, ap := range domainAlgo.AntiPatterns {
			sb.WriteString(fmt.Sprintf("- %s\n", ap))
		}
		sb.WriteString("\n")
	}

	if len(domainAlgo.Patterns) > 0 {
		sb.WriteString("**Watch for these patterns:**\n")
		for _, p := range domainAlgo.Patterns {
			sb.WriteString(fmt.Sprintf("- [%s] %s → %s\n", p.Severity, p.Indicator, p.FixApproach))
		}
		sb.WriteString("\n")
	}

	// Domain angles → recommend think patterns.
	angles := domainAlgo.Angles
	if len(angles) == 0 {
		angles = inv.DefaultAngles
	}
	sb.WriteString("### Cross-Tool Enhancement\n\n")
	sb.WriteString("When assess suggests a think call, use one of these patterns:\n\n")
	for _, angle := range angles {
		// Only list if the pattern is actually registered.
		registered := false
		for _, p := range allPatterns {
			if p == angle.ThinkPattern {
				registered = true
				break
			}
		}
		if registered {
			sb.WriteString(fmt.Sprintf("- **%s** (`think(pattern=%q, ...)`): %s\n", angle.Label, angle.ThinkPattern, angle.Description))
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
		// No goal — emit generic reference.
		sb.WriteString("# aimux Workflow Builder\n\n")
		sb.WriteString("Provide a `goal` argument to get a ready-to-execute pipeline JSON.\n\n")
		sb.WriteString("## Step Schema\n")
		sb.WriteString("```json\n{\n  \"id\": \"step_name\",\n  \"tool\": \"exec|think|investigate|consensus|audit\",\n  \"params\": { ... },\n  \"condition\": \"{{prev.content}} contains 'keyword'\",\n  \"on_error\": \"stop|skip|retry\"\n}\n```\n\n")
		sb.WriteString(fmt.Sprintf("**exec roles:** coding, codereview, debug, secaudit, analyze, refactor, testgen, planner, thinkdeep\n"))
		sb.WriteString(fmt.Sprintf("**think patterns (%d):** %s\n\n", len(thinkPatterns), strings.Join(thinkPatterns, ", ")))
		sb.WriteString("**Template variables:** `{{input}}`, `{{step_id.content}}`, `{{step_id.status}}`\n")
		return mcp.NewGetPromptResult(
			"Workflow builder guide",
			[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(sb.String()))},
		), nil
	}

	// Analyze the goal to determine the best pipeline shape.
	lower := strings.ToLower(goal)

	type workflowStep struct {
		ID        string
		Tool      string
		Params    map[string]any
		Condition string
		OnError   string
	}

	var steps []workflowStep
	var rationale []string

	switch {
	case strings.Contains(lower, "security") || strings.Contains(lower, "audit") || strings.Contains(lower, "vuln"):
		steps = []workflowStep{
			{ID: "audit", Tool: "audit", Params: map[string]any{"cwd": "{{input}}", "mode": "standard"}},
			{ID: "synthesize", Tool: "think", Params: map[string]any{"pattern": "research_synthesis", "artifact": "{{audit.content}}"}},
			{ID: "fix_plan", Tool: "exec", Params: map[string]any{"role": "planner", "prompt": fmt.Sprintf("Create a prioritized fix plan for: %s. Audit findings: {{synthesize.content}}", goal)}},
		}
		rationale = []string{
			"Step 1 (audit): full codebase scan with OWASP-aware analysis",
			"Step 2 (think/research_synthesis): group and prioritize findings",
			"Step 3 (exec/planner): produce actionable remediation plan",
		}

	case strings.Contains(lower, "bug") || strings.Contains(lower, "debug") || strings.Contains(lower, "fix") || strings.Contains(lower, "crash"):
		steps = []workflowStep{
			{ID: "investigate", Tool: "exec", Params: map[string]any{"role": "debug", "prompt": fmt.Sprintf("Investigate and identify root cause: %s. Input context: {{input}}", goal)}},
			{ID: "analyze", Tool: "think", Params: map[string]any{"pattern": "debugging_approach", "problem": "{{investigate.content}}"}},
			{ID: "fix", Tool: "exec", Params: map[string]any{"role": "coding", "prompt": "Implement fix based on root cause analysis: {{analyze.content}}"}, Condition: "{{analyze.content}} contains 'root cause'"},
			{ID: "verify", Tool: "exec", Params: map[string]any{"role": "testgen", "prompt": "Write regression tests for the fix: {{fix.content}}"}, Condition: "{{fix.content}} contains 'fix'"},
		}
		rationale = []string{
			"Step 1 (exec/debug): deep root cause investigation",
			"Step 2 (think/debugging_approach): structured hypothesis and elimination",
			"Step 3 (exec/coding): implement fix only if root cause identified",
			"Step 4 (exec/testgen): regression tests to prevent recurrence",
		}

	case strings.Contains(lower, "review") || strings.Contains(lower, "quality") || strings.Contains(lower, "critique"):
		steps = []workflowStep{
			{ID: "analyze", Tool: "exec", Params: map[string]any{"role": "analyze", "prompt": fmt.Sprintf("%s — initial analysis: {{input}}", goal)}},
			{ID: "review", Tool: "think", Params: map[string]any{"pattern": "peer_review", "artifact": "{{analyze.content}}"}},
			{ID: "validate", Tool: "consensus", Params: map[string]any{"topic": "{{review.content}}", "synthesize": true}},
		}
		rationale = []string{
			"Step 1 (exec/analyze): holistic initial analysis",
			"Step 2 (think/peer_review): structured critique with objections",
			"Step 3 (consensus): multi-model validation of the review",
		}

	case strings.Contains(lower, "refactor") || strings.Contains(lower, "cleanup") || strings.Contains(lower, "reorganize"):
		steps = []workflowStep{
			{ID: "plan", Tool: "exec", Params: map[string]any{"role": "planner", "prompt": fmt.Sprintf("Design refactoring plan for: %s. Target: {{input}}", goal)}},
			{ID: "decompose", Tool: "think", Params: map[string]any{"pattern": "problem_decomposition", "problem": "{{plan.content}}"}},
			{ID: "implement", Tool: "exec", Params: map[string]any{"role": "refactor", "prompt": "Execute refactoring as planned: {{decompose.content}}"}, Condition: "{{decompose.content}} contains 'step'"},
		}
		rationale = []string{
			"Step 1 (exec/planner): design refactoring strategy before touching code",
			"Step 2 (think/problem_decomposition): break into safe atomic steps",
			"Step 3 (exec/refactor): execute only if steps were identified",
		}

	case strings.Contains(lower, "consensus") || strings.Contains(lower, "compare") || strings.Contains(lower, "evaluate") || strings.Contains(lower, "decide"):
		steps = []workflowStep{
			{ID: "propose", Tool: "exec", Params: map[string]any{"role": "planner", "prompt": fmt.Sprintf("Enumerate options for: %s", goal)}},
			{ID: "frame", Tool: "think", Params: map[string]any{"pattern": "decision_framework", "decision": goal, "options": "{{propose.content}}"}},
			{ID: "validate", Tool: "consensus", Params: map[string]any{"topic": "{{frame.content}}", "synthesize": true}},
		}
		rationale = []string{
			"Step 1 (exec/planner): generate candidate options",
			"Step 2 (think/decision_framework): structured tradeoff analysis",
			"Step 3 (consensus): multi-model vote + synthesis",
		}

	default:
		// Generic: analyze → review → implement.
		steps = []workflowStep{
			{ID: "analyze", Tool: "exec", Params: map[string]any{"role": "analyze", "prompt": fmt.Sprintf("%s — initial analysis. Input: {{input}}", goal)}},
			{ID: "review", Tool: "think", Params: map[string]any{"pattern": "peer_review", "artifact": "{{analyze.content}}"}},
			{ID: "implement", Tool: "exec", Params: map[string]any{"role": "coding", "prompt": fmt.Sprintf("Implement: %s. Based on analysis: {{review.content}}", goal)}, Condition: "{{review.content}} contains 'recommendation'"},
		}
		rationale = []string{
			"Step 1 (exec/analyze): understand scope and requirements",
			"Step 2 (think/peer_review): critique the analysis",
			"Step 3 (exec/coding): implement only if analysis yielded recommendations",
		}
	}

	sb.WriteString(fmt.Sprintf("## Workflow for: %s\n\n", goal))
	sb.WriteString("### Ready-to-Execute Pipeline\n\n")
	sb.WriteString("Copy this JSON into the workflow tool's `steps` parameter:\n\n")
	sb.WriteString("```json\n")

	// Marshal steps to clean JSON.
	type jsonStep struct {
		ID        string         `json:"id"`
		Tool      string         `json:"tool"`
		Params    map[string]any `json:"params"`
		Condition string         `json:"condition,omitempty"`
		OnError   string         `json:"on_error,omitempty"`
	}
	jsonSteps := make([]jsonStep, len(steps))
	for i, st := range steps {
		jsonSteps[i] = jsonStep{
			ID:        st.ID,
			Tool:      st.Tool,
			Params:    st.Params,
			Condition: st.Condition,
			OnError:   st.OnError,
		}
	}
	if b, err := json.MarshalIndent(jsonSteps, "", "  "); err == nil {
		sb.Write(b)
	}
	sb.WriteString("\n```\n\n")

	sb.WriteString("### Why this pipeline?\n")
	for _, r := range rationale {
		sb.WriteString(fmt.Sprintf("- %s\n", r))
	}
	sb.WriteString("\n")

	sb.WriteString("### Available Steps\n\n")
	sb.WriteString("**exec roles:** coding, codereview, debug, secaudit, analyze, refactor, testgen, planner, thinkdeep\n\n")
	sb.WriteString(fmt.Sprintf("**think patterns (%d):** %s\n\n", len(thinkPatterns), strings.Join(thinkPatterns, ", ")))
	sb.WriteString("**Conditions:** `{{step_id.content}} contains 'keyword'` or `{{step_id.status}} == 'completed'`\n\n")
	sb.WriteString("**Error handling:** `on_error: \"stop\"` (default) | `\"skip\"` | `\"retry\"`\n")

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
		"aimux-background":  true,
		"aimux-guide":       true,
		"aimux-investigate": true,
		"aimux-workflow":    true,
	}
}

