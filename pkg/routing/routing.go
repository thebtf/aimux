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

// CapabilityVerifier reports whether a (cli, role) tuple has been verified
// by a real probe. Implementations live in the driver package; routing
// depends on this interface only, not on driver, to avoid an import cycle.
//
// AIMUX-16 CR-003 (FR-3): the cache exposes IsVerified for routing. miss=true
// means no entry exists for (cli, role) — caller treats declared capability
// as a soft fallback while an inline probe runs. miss=false, verified=false
// means the role probe ran and failed; caller MUST exclude the CLI for the
// role.
type CapabilityVerifier interface {
	IsVerified(cli, role string) (verified bool, miss bool)
}

// Router resolves roles to CLI preferences.
type Router struct {
	defaults              map[string]types.RolePreference
	enabledCLIs           map[string]bool
	// alphabetical order; used for deterministic ordering in ResolveWithFallback
	// (within each capability group) and in tests. NOT used by Resolve.
	enabledCLIsSorted     []string
	// operator-configured priority order used in production capability-match fallback.
	enabledCLIsPrioritized []string
	profiles              map[string]*config.CLIProfile
	// verifier consults the per-(cli, role) capability cache. Optional — when
	// nil, routing falls back to declared profile.Capabilities (legacy v4.x
	// behaviour). Set via SetCapabilityVerifier after construction so the
	// daemon wiring can install the cache without changing constructor
	// signatures consumed by tests.
	verifier CapabilityVerifier
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
// Includes all configured defaults plus every capability declared by enabled CLI
// profiles, so capability-routed roles are also reported as known. The "default"
// role is always present.
func (r *Router) KnownRoles() []string {
	roleSet := maps.Keys(r.defaults)
	known := slices.Collect(roleSet)

	seen := make(map[string]bool, len(known))
	for _, role := range known {
		seen[role] = true
	}

	for _, cli := range r.enabledCLIsPrioritized {
		if r.profiles == nil {
			break
		}
		profile, ok := r.profiles[cli]
		if !ok || profile == nil {
			continue
		}
		for _, cap := range profile.Capabilities {
			cap = strings.TrimSpace(cap)
			if cap == "" {
				continue
			}
			if !seen[cap] {
				known = append(known, cap)
				seen[cap] = true
			}
		}
	}

	if !seen["default"] {
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
// Within each group the operator-configured cli_priority order is preserved
// (stable sort on enabledCLIsPrioritized input). CLIs not enabled are excluded.
//
// AIMUX-16 CR-003 (FR-3): when a CapabilityVerifier is wired, CLIs with
// verified=false for the role are excluded from the chain entirely — the
// per-role probe ran and failed, so dispatch must not retry them. CLIs with
// no cache entry (miss=true) are kept as soft-fallback so inline probing
// can fill the cache. This matches the spec's "CLI that fails its
// capability probe for a role is excluded from fallback chains for that
// role" requirement.
func (r *Router) ResolveWithFallback(role string) []types.RolePreference {
	// Primary
	primary, err := r.Resolve(role)
	if err != nil {
		// No primary available; still build the fallback list from all enabled CLIs.
		primary = types.RolePreference{}
	}

	type candidate struct {
		pref          types.RolePreference
		hasCapability bool
	}

	var fallbacks []candidate
	for _, cli := range r.enabledCLIsPrioritized {
		if cli == primary.CLI {
			continue // will be prepended as primary
		}
		// Verifier-driven hard exclusion: when the cache says the role probe
		// ran and failed for this CLI, drop it from the chain entirely.
		if r.verifier != nil {
			verified, miss := r.verifier.IsVerified(cli, role)
			if !miss && !verified {
				continue
			}
		}
		hasCapability := r.cliHasCapability(cli, role)
		fallbacks = append(fallbacks, candidate{
			pref:          types.RolePreference{CLI: cli},
			hasCapability: hasCapability,
		})
	}

	// Stable sort: capability-matching CLIs first, then the rest. Within each
	// group the priority order from enabledCLIsPrioritized is preserved.
	sort.SliceStable(fallbacks, func(i, j int) bool {
		return fallbacks[i].hasCapability && !fallbacks[j].hasCapability
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

// SetCapabilityVerifier wires a CapabilityVerifier into routing. Subsequent
// calls to Resolve / ResolveWithFallback consult the verifier per (cli, role)
// before treating declared capabilities as authoritative.
//
// Pass nil to detach (legacy declared-only behaviour).
func (r *Router) SetCapabilityVerifier(v CapabilityVerifier) {
	r.verifier = v
}

// CapabilityVerifier returns the wired verifier (may be nil). Used by tests
// and the health-action observability surface.
func (r *Router) CapabilityVerifier() CapabilityVerifier {
	return r.verifier
}

// cliHasCapability checks whether a CLI's profile declares the given
// capability AND, when a CapabilityVerifier is wired, the role probe has not
// recorded a verified=false outcome.
//
// Decision matrix when verifier is wired (AIMUX-16 EC-3.1):
//
//	declared	verifier (verified, miss)	→ result
//	false		any				→ false (CLI does not declare role)
//	true		(true, false)			→ true  (probe verified)
//	true		(false, true)			→ true  (cache miss — declared used as fallback)
//	true		(false, false)			→ false (probe ran and failed; exclude)
//
// EC-3.2 graceful degradation: when the verifier returns miss=true (no entry
// yet), routing keeps the declared capability so dispatch is not blocked by
// a cold cache. The inline probe at dispatch time fills the cache; the next
// call observes the verified outcome.
func (r *Router) cliHasCapability(cli, capability string) bool {
	if r.profiles == nil {
		return false
	}
	profile, ok := r.profiles[cli]
	if !ok {
		return false
	}
	declared := false
	for _, cap := range profile.Capabilities {
		if cap == capability {
			declared = true
			break
		}
	}
	if !declared {
		return false
	}
	if r.verifier == nil {
		return true
	}
	verified, miss := r.verifier.IsVerified(cli, capability)
	if miss {
		// Cache cold — soft-fallback to declared so dispatch isn't blocked.
		return true
	}
	return verified
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
