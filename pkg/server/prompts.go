// Package server — skill-engine-powered prompt handlers.
// registerSkillPrompts() iterates engine.Skills() and registers each as an MCP prompt.
// handleSkillPrompt() renders the skill template with live server data at request time.
package server

import (
	"context"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"

	inv "github.com/thebtf/aimux/pkg/investigate"
	"github.com/thebtf/aimux/pkg/skills"
	"github.com/thebtf/aimux/pkg/think"
)

// registerSkillPrompts registers one MCP prompt per non-fragment skill from the engine.
// Conflicts with legacy prompts (from registerPrompts) are skipped with a warning.
func (s *Server) registerSkillPrompts() {
	if s.skillEngine == nil {
		return
	}
	legacy := legacyPromptNames()
	for _, meta := range s.skillEngine.Skills() {
		promptName := "aimux-" + meta.Name
		if legacy[promptName] {
			s.log.Warn("skill prompt %q conflicts with legacy prompt — skipping (legacy takes precedence)", promptName)
			continue
		}

		// Build MCP prompt options: description + args from frontmatter.
		opts := []mcp.PromptOption{
			mcp.WithPromptDescription(meta.Description),
		}
		for _, arg := range meta.Args {
			opts = append(opts, mcp.WithArgument(arg.Name, mcp.ArgumentDescription(arg.Description)))
		}

		// Capture meta.Name for the closure — loop variable is reused.
		name := meta.Name
		s.mcp.AddPrompt(
			mcp.NewPrompt(promptName, opts...),
			s.handleSkillPrompt(name),
		)
	}
}

// handleSkillPrompt returns a prompt handler closure for the named skill.
// The closure builds SkillData from live server state and renders the template.
func (s *Server) handleSkillPrompt(name string) func(context.Context, mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	return func(_ context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		data := s.buildSkillData(req)
		rendered, err := s.skillEngine.Render(name, data)
		if err != nil {
			return nil, fmt.Errorf("render skill %q: %w", name, err)
		}

		meta := s.skillEngine.Get(name)
		title := name
		if meta != nil {
			title = meta.Description
		}

		return mcp.NewGetPromptResult(
			title,
			[]mcp.PromptMessage{
				mcp.NewPromptMessage(mcp.RoleAssistant, mcp.NewTextContent(rendered)),
			},
		), nil
	}
}

// buildSkillData populates a SkillData struct from live server state and request arguments.
func (s *Server) buildSkillData(req mcp.GetPromptRequest) *skills.SkillData {
	enabledCLIs := s.registry.EnabledCLIs()

	// Compute role routing for the standard set of roles.
	roleRouting := make(map[string]string)
	standardRoles := []string{
		"coding", "codereview", "debug", "secaudit",
		"analyze", "refactor", "testgen", "planner", "thinkdeep",
	}
	for _, role := range standardRoles {
		if pref, err := s.router.Resolve(role); err == nil {
			roleRouting[role] = pref.CLI
		}
	}

	// Metrics snapshot.
	snap := s.metrics.Snapshot()

	// Past investigation reports from cwd.
	var pastReports []skills.ReportInfo
	if cwd, err := os.Getwd(); err == nil {
		if entries, err := inv.ListReports(cwd); err == nil {
			for _, e := range entries {
				pastReports = append(pastReports, skills.ReportInfo{
					Topic:    e.Topic,
					Date:     e.Date,
					Filename: e.Filename,
				})
			}
		}
	}

	// Agent list.
	var agentInfos []skills.AgentInfo
	for _, a := range s.agentReg.List() {
		agentInfos = append(agentInfos, skills.AgentInfo{
			Name:        a.Name,
			Description: a.Description,
			Role:        a.Role,
		})
	}

	// Think patterns.
	thinkPatterns := think.GetAllPatterns()

	// Args from the MCP request.
	args := make(map[string]string)
	if req.Params.Arguments != nil {
		for k, v := range req.Params.Arguments {
			args[k] = v
		}
	}

	hasGemini := false
	for _, cli := range enabledCLIs {
		if cli == "gemini" {
			hasGemini = true
			break
		}
	}

	return &skills.SkillData{
		EnabledCLIs:     enabledCLIs,
		CLICount:        len(enabledCLIs),
		HasMultipleCLIs: len(enabledCLIs) > 1,
		HasGemini:       hasGemini,
		RoleRouting:     roleRouting,
		TotalRequests:   snap.TotalRequests,
		ErrorRate:       snap.ErrorRate,
		PastReports:     pastReports,
		Agents:          agentInfos,
		ThinkPatterns:   thinkPatterns,
		Args:            args,
	}
}
