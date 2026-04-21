package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/thebtf/aimux/pkg/routing"
)

const (
	instructionToolCount    = 36
	instructionPatternCount = 23
)

func buildInstructions(
	warmCLIs []string,
	warmupComplete bool,
	allProfiles []string,
	agentCount int,
	roleMap map[string]string,
) string {
	_ = agentCount

	cliCount := len(allProfiles)
	if warmupComplete {
		cliCount = len(warmCLIs)
	}

	lines := []string{
		fmt.Sprintf(
			"aimux — AI CLI Multiplexer (%d tools, %d CLIs, %d think patterns)",
			instructionToolCount,
			cliCount,
			instructionPatternCount,
		),
		"",
		"aimux delegates work to external AI CLIs — free for you, no token cost from your context. Use aimux for implementation, review, debugging, and multi-model consensus instead of native subagents.",
		"",
	}

	switch {
	case warmupComplete && len(warmCLIs) > 0:
		lines = append(lines, "## Available CLIs")
		for _, cli := range sortedStrings(warmCLIs) {
			role := displayRoleForCLI(cli, roleMap)
			if role == "" {
				lines = append(lines, fmt.Sprintf("- %s", cli))
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", cli, role))
		}
	case warmupComplete:
		lines = append(lines, `No CLIs available — check warmup status with sessions(action="health")`)
	default:
		lines = append(lines, "## Available CLIs")
		for _, cli := range sortedStrings(allProfiles) {
			lines = append(lines, fmt.Sprintf("- %s", cli))
		}
		lines = append(lines, "(warmup in progress — some CLIs may be unavailable)")
	}

	lines = append(
		lines,
		"",
		"## First Actions",
		`1. sessions(action="health") — discover server state and available CLIs`,
		`2. agents(action="find", prompt="your task") — find the best agent for delegation`,
		"",
		"## Tool Tags",
		"- [solo] — runs in-process, no CLI spawn, free",
		"- [delegate] — dispatches to external AI CLI, free for you",
		"- [manage] — server state management, no cost",
		"",
		"## Reference",
		"For complete tool reference, think pattern examples, and role routing details, request the `guide` MCP prompt.",
		"",
		"## Anti-Patterns",
		"- Don't specify cli= when role= is enough — let routing pick the best CLI",
		"- Don't use sync exec for tasks >30s — use async=true",
		"- Don't skip investigate for complex bugs — jumping to fix wastes time",
		"- Don't run consensus with 1 CLI — needs 2+ for comparison",
		"- Don't call exec for tasks an agent can handle — use agent-exec first",
	)

	return strings.Join(lines, "\n")
}

// buildRoleMap extracts a role→CLI mapping from the router for display in instructions.
// Returns nil if r is nil.
func buildRoleMap(r *routing.Router) map[string]string {
	if r == nil {
		return nil
	}
	result := make(map[string]string)
	for _, role := range r.KnownRoles() {
		if pref, err := r.Resolve(role); err == nil && pref.CLI != "" {
			result[role] = pref.CLI
		}
	}
	return result
}

func displayRoleForCLI(cli string, roleMap map[string]string) string {
	if len(roleMap) == 0 {
		return ""
	}

	roles := make([]string, 0, len(roleMap))
	for role, mappedCLI := range roleMap {
		if mappedCLI == cli && strings.TrimSpace(role) != "" {
			roles = append(roles, role)
		}
	}
	if len(roles) == 0 {
		return ""
	}

	sort.Strings(roles)
	return roles[0]
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
