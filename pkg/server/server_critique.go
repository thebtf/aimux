package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/resolve"
	"github.com/thebtf/aimux/pkg/types"
)

// builtinLenses maps lens names to system prompt templates used for critique.
var builtinLenses = map[string]string{
	"security":         "You are a security expert. Review the following artifact for vulnerabilities, injection risks, auth issues, data exposure. Produce findings as JSON array.",
	"api-design":       "You are an API design expert. Review for consistency, naming, error handling, versioning.",
	"spec-compliance":  "You are a spec compliance auditor. Check if implementation matches specification.",
	"adversarial":      "You are a devil's advocate. Find every flaw, weakness, and assumption.",
}

// critiqueResponsePrompt is appended to every lens prompt to enforce structured output.
const critiqueResponsePrompt = "\n\nRespond with JSON: {findings: [{severity, location, issue, suggested_fix}], summary: string}"

// maxArtifactBytes is the maximum allowed size for a critique artifact.
const maxArtifactBytes = 100 * 1024

// handleCritique runs an artifact through a critique lens using the configured CLI.
func (s *Server) handleCritique(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	artifact, err := request.RequireString("artifact")
	if err != nil {
		return mcp.NewToolResultError("artifact is required"), nil
	}

	if len(artifact) > maxArtifactBytes {
		return mcp.NewToolResultError(fmt.Sprintf("artifact too large (%d bytes, max %d)", len(artifact), maxArtifactBytes)), nil
	}

	lens := request.GetString("lens", "")
	cliOverride := request.GetString("cli", "")
	maxFindings := int(request.GetFloat("max_findings", 10))
	if maxFindings <= 0 {
		maxFindings = 10
	}

	// Build the prompt from the lens template.
	var lensTemplate string
	if lens != "" {
		tmpl, ok := builtinLenses[lens]
		if !ok {
			// Unknown lens: reject immediately with the list of valid lenses.
			var known []string
			for k := range builtinLenses {
				known = append(known, k)
			}
			return mcp.NewToolResultError(fmt.Sprintf(
				"unknown lens %q; valid lenses: %s",
				lens, strings.Join(known, ", "),
			)), nil
		}
		lensTemplate = tmpl
	} else {
		lensTemplate = "You are a critical expert reviewer. Examine the following artifact thoroughly and find issues."
	}

	prompt := lensTemplate +
		"\n\n<artifact>\n" + artifact + "\n</artifact>" +
		critiqueResponsePrompt

	// Resolve CLI: explicit override takes precedence; otherwise route via "critic" role,
	// falling back to "default" when the "critic" role is not configured.
	cli := cliOverride
	if cli == "" {
		pref, resolveErr := s.router.Resolve("critic")
		if resolveErr != nil {
			// "critic" role not configured — fall back to default role.
			defPref, defErr := s.router.Resolve("default")
			if defErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("no CLI available: %v", defErr)), nil
			}
			cli = defPref.CLI
		} else {
			cli = pref.CLI
		}
	}

	profile, profileErr := s.registry.Get(cli)
	if profileErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("CLI %q not available: %v", cli, profileErr)), nil
	}

	args := types.SpawnArgs{
		CLI:            cli,
		Command:        resolve.CommandBinary(profile.Command.Base),
		Args:           resolve.BuildPromptArgs(profile, "", "", false, prompt),
		TimeoutSeconds: profile.TimeoutSeconds,
	}

	result, runErr := s.executor.Run(ctx, args)
	if runErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("critique execution failed: %v", runErr)), nil
	}

	// Attempt to parse structured JSON findings from the output.
	findings, summary, parseErr := parseCritiqueOutput(result.Content, maxFindings)
	if parseErr != nil {
		// Non-JSON output: return raw content as a single finding.
		findings = []map[string]any{
			{
				"severity":      "unknown",
				"location":      "full output",
				"issue":         result.Content,
				"suggested_fix": "",
			},
		}
		summary = "Raw output — structured parse failed: " + parseErr.Error()
	}

	payload := map[string]any{
		"findings": findings,
		"summary":  summary,
		"cli_used": cli,
		"tokens":   len(result.Content),
	}
	if lens != "" {
		payload["lens"] = lens
	}

	return marshalToolResult(payload)
}

// parseCritiqueOutput extracts findings and summary from a JSON response.
// It searches for the outermost JSON object in the output to handle
// CLI wrappers that prefix/suffix content around the JSON payload.
func parseCritiqueOutput(output string, maxFindings int) ([]map[string]any, string, error) {
	// Find the first '{' to handle any preamble text.
	start := strings.Index(output, "{")
	if start < 0 {
		return nil, "", fmt.Errorf("no JSON object found in output")
	}

	// Find the matching closing brace.
	depth := 0
	end := -1
	for i := start; i < len(output); i++ {
		switch output[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil, "", fmt.Errorf("malformed JSON: unmatched braces")
	}

	jsonStr := output[start : end+1]

	var parsed struct {
		Findings []map[string]any `json:"findings"`
		Summary  string           `json:"summary"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, "", fmt.Errorf("JSON unmarshal: %w", err)
	}

	findings := parsed.Findings
	if len(findings) > maxFindings {
		findings = findings[:maxFindings]
	}

	return findings, parsed.Summary, nil
}
