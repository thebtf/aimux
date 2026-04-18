// Package routing resolves roles to CLI+model+effort preferences.
package routing

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/types"
)

// AdvisoryRoles are roles that default to read_only=true.
var AdvisoryRoles = map[string]bool{
	"thinkdeep":  true,
	"codereview": true,
	"secaudit":   true,
	"challenge":  true,
	"planner":    true,
	// Persona roles
	"backend-architect":    true,
	"security-auditor":     true,
	"code-reviewer":        true,
	"database-architect":   true,
	"frontend-developer":   true,
	"devops-engineer":      true,
	"tdd-orchestrator":     true,
	"debugger":             true,
	"incident-responder":   true,
	"ai-engineer":          true,
	"docs-architect":       true,
	"cloud-architect":      true,
	"performance-engineer": true,
	"api-designer":         true,
	"testing-strategist":   true,
	"refactoring-expert":   true,
}

// Router resolves roles to CLI preferences.
type Router struct {
	defaults              map[string]types.RolePreference
	enabledCLIs           map[string]bool
	// alphabetical order; used only for test determinism, NOT in production Resolve.
	enabledCLIsSorted     []string
	// operator-configured priority order used in production capability-match fallback.
	enabledCLIsPrioritized []string
	profiles              map[string]*config.CLIProfile
}

// NewRouter creates a router with configured defaults and enabled CLIs.
// CLIPriority defaults to alphabetical order (useful for tests).
func NewRouter(defaults map[string]types.RolePreference, enabledCLIs []string) *Router {
	enabled := make(map[string]bool, len(enabledCLIs))
	for _, cli := range enabledCLIs {
		enabled[cli] = true
	}

	// Sort once at construction so the alphabetical slice is deterministic (test use only).
	sorted := make([]string, len(enabledCLIs))
	copy(sorted, enabledCLIs)
	sort.Strings(sorted)

	return &Router{
		defaults:               defaults,
		enabledCLIs:            enabled,
		enabledCLIsSorted:      sorted,
		enabledCLIsPrioritized: sorted, // default: same as sorted (tests)
	}
}

// NewRouterWithProfiles creates a router that can use capability-based fallback.
// cliPriority controls the tiebreak order for capability-match fallback; pass nil
// to use alphabetical order (same as NewRouter).
func NewRouterWithProfiles(defaults map[string]types.RolePreference, enabledCLIs []string, profiles map[string]*config.CLIProfile) *Router {
	return NewRouterWithPriority(defaults, enabledCLIs, profiles, nil)
}

// NewRouterWithPriority creates a router with an explicit CLI priority list.
// CLIs in cliPriority are placed first (in that order); any enabled CLI not in
// cliPriority is appended after in stable load order (from enabledCLIs slice).
func NewRouterWithPriority(defaults map[string]types.RolePreference, enabledCLIs []string, profiles map[string]*config.CLIProfile, cliPriority []string) *Router {
	r := NewRouter(defaults, enabledCLIs)
	r.profiles = profiles
	r.enabledCLIsPrioritized = buildPrioritized(enabledCLIs, cliPriority)
	return r
}

// buildPrioritized constructs the prioritized CLI list.
// Entries from priority that are enabled come first; remaining enabled CLIs
// follow in the order they appear in enabled (stable load order).
func buildPrioritized(enabled []string, priority []string) []string {
	enabledSet := make(map[string]bool, len(enabled))
	for _, cli := range enabled {
		enabledSet[cli] = true
	}

	seen := make(map[string]bool)
	result := make([]string, 0, len(enabled))

	// First: priority-ordered entries that are enabled.
	for _, cli := range priority {
		if enabledSet[cli] && !seen[cli] {
			result = append(result, cli)
			seen[cli] = true
		}
	}

	// Second: remaining enabled CLIs in stable load order.
	for _, cli := range enabled {
		if !seen[cli] {
			result = append(result, cli)
			seen[cli] = true
		}
	}

	return result
}

// KnownRoles returns the set of role names this router understands.
// Includes all configured defaults plus the hard-coded "default" role.
func (r *Router) KnownRoles() []string {
	roleSet := maps.Keys(r.defaults)
	known := slices.Collect(roleSet)
	// Ensure "default" is always present.
	if !slices.Contains(known, "default") {
		known = append(known, "default")
	}
	sort.Strings(known)
	return known
}

// Resolve finds the best CLI+model+effort for a given role.
// Priority: AIMUX_ROLE_* env → configured defaults → capability-match in priority order.
// Returns NotFoundError when the role is unknown and no capable CLI is found.
func (r *Router) Resolve(role string) (types.RolePreference, error) {
	// Check env override: AIMUX_ROLE_CODING=codex:gpt-5.3-codex:medium
	envKey := "AIMUX_ROLE_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_"))
	if envVal := os.Getenv(envKey); envVal != "" {
		pref, err := parseEnvPreference(envVal)
		if err != nil {
			return types.RolePreference{}, types.NewValidationError(
				fmt.Sprintf("invalid %s value %q: %v", envKey, envVal, err))
		}
		if r.isEnabled(pref.CLI) {
			return pref, nil
		}
		// CLI from env not available, fall through to defaults
	}

	// Check configured defaults
	if pref, ok := r.defaults[role]; ok {
		if r.isEnabled(pref.CLI) {
			return pref, nil
		}
	}

	// Capability-match fallback: iterate enabledCLIsPrioritized, pick first CLI
	// whose profile capabilities include the role. Unknown roles that no CLI
	// declares capability for return NotFoundError immediately — no silent fallback.
	for _, cli := range r.enabledCLIsPrioritized {
		if r.cliHasCapability(cli, role) {
			return types.RolePreference{CLI: cli}, nil
		}
	}

	// Build the list of CLIs that do have capabilities for diagnostic context.
	var capableCLIs []string
	for _, cli := range r.enabledCLIsPrioritized {
		if r.profiles != nil {
			if p, ok := r.profiles[cli]; ok && len(p.Capabilities) > 0 {
				capableCLIs = append(capableCLIs, cli)
			}
		}
	}

	if len(capableCLIs) == 0 {
		return types.RolePreference{}, types.NewNotFoundError(
			fmt.Sprintf("no CLI available for role %q (no enabled CLIs)", role))
	}

	return types.RolePreference{}, types.NewNotFoundError(
		fmt.Sprintf("unknown role %q; no enabled CLI declares capability for it (capable CLIs: %s)",
			role, strings.Join(capableCLIs, ", ")))
}

// IsAdvisory returns true if the role defaults to read-only mode.
func IsAdvisory(role string) bool {
	return AdvisoryRoles[role]
}

// isEnabled checks if a CLI is in the enabled set.
func (r *Router) isEnabled(cli string) bool {
	return r.enabledCLIs[cli]
}

// ResolveWithFallback returns an ordered list of CLIs to try for a role.
// The primary CLI (from Resolve) comes first. Fallbacks are sorted so that
// CLIs whose Capabilities list contains the role name come before others.
// CLIs that are not enabled are excluded entirely.
func (r *Router) ResolveWithFallback(role string) []types.RolePreference {
	// Primary
	primary, err := r.Resolve(role)
	if err != nil {
		// No primary available; still build the fallback list from all enabled CLIs.
		primary = types.RolePreference{}
	}

	type candidate struct {
		pref         types.RolePreference
		hasCapability bool
	}

	var fallbacks []candidate
	for _, cli := range r.enabledCLIsSorted {
		if cli == primary.CLI {
			continue // will be prepended as primary
		}
		hasCapability := r.cliHasCapability(cli, role)
		fallbacks = append(fallbacks, candidate{
			pref:          types.RolePreference{CLI: cli},
			hasCapability: hasCapability,
		})
	}

	// Stable sort: capability-matching CLIs first, then the rest. Within each
	// group, sort by name for deterministic ordering in tests.
	sort.SliceStable(fallbacks, func(i, j int) bool {
		if fallbacks[i].hasCapability != fallbacks[j].hasCapability {
			return fallbacks[i].hasCapability // capability-match goes first
		}
		return fallbacks[i].pref.CLI < fallbacks[j].pref.CLI
	})

	result := make([]types.RolePreference, 0, 1+len(fallbacks))
	if primary.CLI != "" {
		result = append(result, primary)
	}
	for _, c := range fallbacks {
		result = append(result, c.pref)
	}
	return result
}

// cliHasCapability checks whether a CLI's profile declares the given capability.
// Returns false when profiles are not loaded or the CLI has no profile.
func (r *Router) cliHasCapability(cli, capability string) bool {
	if r.profiles == nil {
		return false
	}
	profile, ok := r.profiles[cli]
	if !ok {
		return false
	}
	for _, cap := range profile.Capabilities {
		if cap == capability {
			return true
		}
	}
	return false
}

// parseEnvPreference parses "cli:model:effort" format from env vars.
func parseEnvPreference(val string) (types.RolePreference, error) {
	parts := strings.SplitN(val, ":", 3)
	if len(parts) == 0 || parts[0] == "" {
		return types.RolePreference{}, fmt.Errorf("empty CLI name")
	}

	pref := types.RolePreference{CLI: parts[0]}
	if len(parts) >= 2 && parts[1] != "" {
		pref.Model = parts[1]
	}
	if len(parts) >= 3 && parts[2] != "" {
		pref.ReasoningEffort = parts[2]
	}

	return pref, nil
}
