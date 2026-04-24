package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/think"
)

// buildInstructions generates the MCP server instructions string at connect time
// using live server state. This replaces the static const aimuxInstructions.
func buildInstructions(
	warmCLIs []string,
	warmupComplete bool,
	allProfiles []string,
	agentCount int,
	roleMap map[string]string,
) string {
	cliCount := len(warmCLIs)
	if !warmupComplete {
		cliCount = len(allProfiles)
	}

	patternCount := len(think.GetAllPatterns())

	lines := []string{
		fmt.Sprintf("aimux — AI CLI Multiplexer (%d tools, %d CLIs, %d think patterns)", 36, cliCount, patternCount),
		"",
		"aimux delegates work to external AI CLIs — free for you, no token cost from your context. Use aimux for implementation, review, debugging, and multi-model consensus instead of native subagents.",
		"",
		"## Available CLIs",
	}

	lines = append(lines, renderAvailableCLIs(warmCLIs, warmupComplete, allProfiles, roleMap)...)
	lines = append(lines,
		"",
		fmt.Sprintf("## Agent Discovery\n%d named agents currently registered for delegation.", agentCount),
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

func renderAvailableCLIs(
	warmCLIs []string,
	warmupComplete bool,
	allProfiles []string,
	roleMap map[string]string,
) []string {
	if warmupComplete {
		if len(warmCLIs) == 0 {
			return []string{`No CLIs available — check warmup status with sessions(action="health")`}
		}

		sortedCLIs := append([]string(nil), warmCLIs...)
		sort.Strings(sortedCLIs)
		rolesByCLI := invertRoleMap(roleMap)

		lines := make([]string, 0, len(sortedCLIs))
		for _, cli := range sortedCLIs {
			roles := rolesByCLI[cli]
			if len(roles) == 0 {
				lines = append(lines, fmt.Sprintf("- %s", cli))
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s: %s", cli, strings.Join(roles, ", ")))
		}
		return lines
	}

	sortedProfiles := append([]string(nil), allProfiles...)
	sort.Strings(sortedProfiles)

	lines := make([]string, 0, len(sortedProfiles))
	for _, profile := range sortedProfiles {
		lines = append(lines, fmt.Sprintf("- %s (warmup in progress — some CLIs may be unavailable)", profile))
	}
	return lines
}

func invertRoleMap(roleMap map[string]string) map[string][]string {
	result := make(map[string][]string)
	for role, cli := range roleMap {
		if role == "" || cli == "" {
			continue
		}
		result[cli] = append(result[cli], role)
	}
	for cli := range result {
		sort.Strings(result[cli])
	}
	return result
}

// buildRoleMap extracts a role→CLI mapping from the router for display in instructions.
// Returns nil if r is nil.
func buildRoleMap(r *routing.Router) map[string]string {
	if r == nil {
		return nil
	}

	result := make(map[string]string)
	for _, role := range r.KnownRoles() {
		pref, err := r.Resolve(role)
		if err != nil || pref.CLI == "" {
			continue
		}
		result[role] = pref.CLI
	}
	return result
}
