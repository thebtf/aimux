package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/thebtf/aimux/pkg/routing"
)

// buildInstructions generates the MCP server instructions string at connect time
// using live server state. This replaces the static const legacyInstructions.
//
// Parameters:
//   - warmCLIs: CLI names that passed warmup (e.g., ["codex", "claude", "gemini"])
//   - warmupComplete: whether warmup has finished; false shows allProfiles with suffix
//   - allProfiles: all configured CLI profile names (shown when warmup incomplete)
//   - agentCount: number of registered agents
//   - roleMap: role → CLI mapping (e.g., "coding" → "codex"); may be nil/empty
//
// Output constraints: ≤120 lines, ≤16000 chars (len/4 ≤ 4000 tokens).
func buildInstructions(
	warmCLIs []string,
	warmupComplete bool,
	allProfiles []string,
	agentCount int,
	roleMap map[string]string,
) string {
	var b strings.Builder

	// Section 1: Header
	cliCount := len(warmCLIs)
	if !warmupComplete {
		cliCount = len(allProfiles)
	}
	fmt.Fprintf(&b, "aimux — AI CLI Multiplexer (36 tools, %d CLIs, 23 think patterns)\n\n", cliCount)

	// Section 2: Value proposition
	b.WriteString("aimux delegates work to external AI CLIs — free for you, no token cost from your context.")
	b.WriteString(" Use aimux for implementation, review, debugging, and multi-model consensus instead of native subagents.\n\n")

	// Section 3: Available CLIs
	if warmupComplete {
		if len(warmCLIs) == 0 {
			b.WriteString(`No CLIs available — check warmup status with sessions(action="health")`)
			b.WriteString("\n\n")
		} else {
			b.WriteString("## Available CLIs\n")
			sorted := make([]string, len(warmCLIs))
			copy(sorted, warmCLIs)
			sort.Strings(sorted)
			for _, cli := range sorted {
				if role, ok := roleMap[cli]; ok && role != "" {
					fmt.Fprintf(&b, "- %s: %s\n", cli, role)
				} else {
					fmt.Fprintf(&b, "- %s\n", cli)
				}
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString("## Available CLIs\n")
		sorted := make([]string, len(allProfiles))
		copy(sorted, allProfiles)
		sort.Strings(sorted)
		for _, cli := range sorted {
			fmt.Fprintf(&b, "- %s\n", cli)
		}
		b.WriteString("(warmup in progress — some CLIs may be unavailable)\n\n")
	}

	// Section 4: First actions
	b.WriteString("## First Actions\n")
	b.WriteString("1. sessions(action=\"health\") — discover server state and available CLIs\n")
	b.WriteString("2. agents(action=\"find\", prompt=\"your task\") — find the best agent for delegation\n\n")

	// Section 5: Tool tags
	b.WriteString("## Tool Tags\n")
	b.WriteString("- [solo] — runs in-process, no CLI spawn, free\n")
	b.WriteString("- [delegate] — dispatches to external AI CLI, free for you\n")
	b.WriteString("- [manage] — server state management, no cost\n\n")

	// Section 6: Guide reference
	b.WriteString("## Reference\n")
	b.WriteString("For complete tool reference, think pattern examples, and role routing details, request the `guide` MCP prompt.\n\n")

	// Section 7: Anti-patterns
	b.WriteString("## Anti-Patterns\n")
	b.WriteString("- Don't specify cli= when role= is enough — let routing pick the best CLI\n")
	b.WriteString("- Don't use sync exec for tasks >30s — use async=true\n")
	b.WriteString("- Don't skip investigate for complex bugs — jumping to fix wastes time\n")
	b.WriteString("- Don't run consensus with 1 CLI — needs 2+ for comparison\n")
	b.WriteString("- Don't call exec for tasks an agent can handle — use agent-exec first\n")

	return b.String()
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
