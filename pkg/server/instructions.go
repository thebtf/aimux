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
	_ = agentCount
	cliCount := len(warmCLIs)
	if !warmupComplete {
		cliCount = len(allProfiles)
	}

	patternCount := len(think.GetAllPatterns())
	toolCount := 4 + patternCount

	lines := []string{
		fmt.Sprintf("aimux — AI CLI Multiplexer (%d tools, %d CLIs, %d think patterns)", toolCount, cliCount, patternCount),
		"",
		"aimux currently exposes a reduced MCP surface: server state tools, deep research, upgrade control, and dedicated think pattern tools.",
		"",
		"## Available CLIs",
	}

	lines = append(lines, renderAvailableCLIs(warmCLIs, warmupComplete, allProfiles, roleMap)...)
	lines = append(lines,
		"",
		"## First Actions",
		`1. sessions(action="health") — discover server state and available CLIs`,
		`2. think or another pattern tool — run structured reasoning in-process`,
		"",
		"## Tool Tags",
		"- [solo] — runs in-process, no CLI spawn, free",
		"- [delegate] — uses external services without exposing the old CLI-launching MCP surface",
		"- [manage] — server state management, no cost",
		"",
		"## Reference",
		"For complete tool reference, think pattern examples, and role routing details, request the `guide` MCP prompt.",
		"",
		"## Anti-Patterns",
		"- Don't expect exec/agent/workflow tools on this branch — they were removed in the Layer 5 purge",
		"- Don't assume dormant pipeline packages are active runtime surface",
		"- Don't skip structured reasoning when a problem needs explicit evidence and tradeoff analysis",
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
