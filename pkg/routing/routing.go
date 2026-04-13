// Package routing resolves roles to CLI+model+effort preferences.
package routing

import (
	"fmt"
	"os"
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
	defaults        map[string]types.RolePreference
	enabledCLIs     map[string]bool
	enabledCLIsSorted []string // sorted slice for deterministic fallback iteration
	profiles        map[string]*config.CLIProfile
}

// NewRouter creates a router with configured defaults and enabled CLIs.
func NewRouter(defaults map[string]types.RolePreference, enabledCLIs []string) *Router {
	enabled := make(map[string]bool, len(enabledCLIs))
	for _, cli := range enabledCLIs {
		enabled[cli] = true
	}

	// Sort once at construction so fallback iteration is deterministic.
	sorted := make([]string, len(enabledCLIs))
	copy(sorted, enabledCLIs)
	sort.Strings(sorted)

	return &Router{
		defaults:          defaults,
		enabledCLIs:       enabled,
		enabledCLIsSorted: sorted,
	}
}

// NewRouterWithProfiles creates a router that can use capability-based fallback.
func NewRouterWithProfiles(defaults map[string]types.RolePreference, enabledCLIs []string, profiles map[string]*config.CLIProfile) *Router {
	r := NewRouter(defaults, enabledCLIs) // sorted slice set by NewRouter
	r.profiles = profiles
	return r
}

// Resolve finds the best CLI+model+effort for a given role.
// Priority: AIMUX_ROLE_* env → configured defaults → first enabled CLI.
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

	// Fallback: first enabled CLI in sorted order for deterministic behavior.
	for _, cli := range r.enabledCLIsSorted {
		return types.RolePreference{CLI: cli}, nil
	}

	return types.RolePreference{}, types.NewNotFoundError(
		fmt.Sprintf("no CLI available for role %q", role))
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
