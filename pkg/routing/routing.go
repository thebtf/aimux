// Package routing resolves roles to CLI+model+effort preferences.
package routing

import (
	"fmt"
	"os"
	"strings"

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
	defaults    map[string]types.RolePreference
	enabledCLIs map[string]bool
}

// NewRouter creates a router with configured defaults and enabled CLIs.
func NewRouter(defaults map[string]types.RolePreference, enabledCLIs []string) *Router {
	enabled := make(map[string]bool, len(enabledCLIs))
	for _, cli := range enabledCLIs {
		enabled[cli] = true
	}

	return &Router{
		defaults:    defaults,
		enabledCLIs: enabled,
	}
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

	// Fallback: first enabled CLI
	for cli := range r.enabledCLIs {
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
