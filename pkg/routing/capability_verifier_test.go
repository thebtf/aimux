// AIMUX-16 CR-003 (FR-3, EC-3.1, EC-3.2):
// routing-side tests for the CapabilityVerifier integration.
package routing_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/types"
)

// stubVerifier is a deterministic CapabilityVerifier for tests.
// Keys in `entries` map are "cli|role" to avoid struct keys in literals.
type stubVerifier struct {
	verified map[string]bool // "cli|role" → verified
	misses   map[string]bool // "cli|role" → miss=true (no entry)
}

func (s *stubVerifier) IsVerified(cli, role string) (bool, bool) {
	key := cli + "|" + role
	if s.misses[key] {
		return false, true
	}
	v, ok := s.verified[key]
	if !ok {
		return false, true
	}
	return v, false
}

func makeProfilesAB(t *testing.T) map[string]*config.CLIProfile {
	t.Helper()
	return map[string]*config.CLIProfile{
		"cli-a": {
			Name:         "cli-a",
			Capabilities: []string{"coding", "review"},
		},
		"cli-b": {
			Name:         "cli-b",
			Capabilities: []string{"coding"},
		},
	}
}

// TestRouter_Verifier_ExcludesUnverifiedCLI verifies that when the cache
// records verified=false for (cli-a, coding), routing skips cli-a for that
// role even though the profile declares it.
func TestRouter_Verifier_ExcludesUnverifiedCLI(t *testing.T) {
	profiles := makeProfilesAB(t)
	r := routing.NewRouterWithProfiles(
		map[string]types.RolePreference{}, // no defaults — force capability fallback
		[]string{"cli-a", "cli-b"},
		profiles,
	)

	r.SetCapabilityVerifier(&stubVerifier{
		verified: map[string]bool{
			"cli-a|coding": false, // probed and failed
			"cli-b|coding": true,
		},
	})

	pref, err := r.Resolve("coding")
	if err != nil {
		t.Fatalf("Resolve(coding): %v", err)
	}
	// cli-a is excluded → fallback chooses cli-b.
	if pref.CLI != "cli-b" {
		t.Errorf("Resolve(coding).CLI = %q, want cli-b (cli-a excluded by verifier)", pref.CLI)
	}
}

// TestRouter_Verifier_CacheMissUsesDeclared verifies EC-3.2 graceful
// degradation: when no cache entry exists for (cli, role), routing falls
// back to declared capability so dispatch is not blocked while inline probe
// runs.
func TestRouter_Verifier_CacheMissUsesDeclared(t *testing.T) {
	profiles := makeProfilesAB(t)
	r := routing.NewRouterWithProfiles(
		map[string]types.RolePreference{},
		[]string{"cli-a", "cli-b"},
		profiles,
	)

	// Empty verifier — every Get returns miss=true.
	r.SetCapabilityVerifier(&stubVerifier{})

	pref, err := r.Resolve("coding")
	if err != nil {
		t.Fatalf("Resolve(coding): %v", err)
	}
	// cli-a comes first in priority; cache miss → declared used → cli-a wins.
	if pref.CLI != "cli-a" {
		t.Errorf("Resolve(coding).CLI = %q, want cli-a (cache miss, declared used)", pref.CLI)
	}
}

// TestRouter_Verifier_AllExcluded_NotFound verifies that when every CLI's
// capability probe failed for a role, Resolve returns NotFoundError —
// routing must NOT silently fall back to declared.
func TestRouter_Verifier_AllExcluded_NotFound(t *testing.T) {
	profiles := makeProfilesAB(t)
	r := routing.NewRouterWithProfiles(
		map[string]types.RolePreference{},
		[]string{"cli-a", "cli-b"},
		profiles,
	)

	r.SetCapabilityVerifier(&stubVerifier{
		verified: map[string]bool{
			"cli-a|coding": false,
			"cli-b|coding": false,
		},
	})

	_, err := r.Resolve("coding")
	if err == nil {
		t.Fatal("expected NotFoundError when every CLI is excluded by verifier")
	}
}

// TestRouter_Verifier_NilVerifier_LegacyBehavior verifies that detaching the
// verifier (SetCapabilityVerifier(nil)) restores v4.x declared-only routing.
func TestRouter_Verifier_NilVerifier_LegacyBehavior(t *testing.T) {
	profiles := makeProfilesAB(t)
	r := routing.NewRouterWithProfiles(
		map[string]types.RolePreference{},
		[]string{"cli-a", "cli-b"},
		profiles,
	)

	// First wire a verifier that excludes cli-a, then detach.
	r.SetCapabilityVerifier(&stubVerifier{
		verified: map[string]bool{"cli-a|coding": false},
	})
	r.SetCapabilityVerifier(nil) // detach

	pref, err := r.Resolve("coding")
	if err != nil {
		t.Fatalf("Resolve(coding): %v", err)
	}
	// Without verifier, declared-first wins → cli-a.
	if pref.CLI != "cli-a" {
		t.Errorf("Resolve(coding).CLI = %q, want cli-a (no verifier, declared-only)", pref.CLI)
	}
}

// TestRouter_Verifier_ResolveWithFallback_OrderRespected verifies that the
// fallback list excludes verified=false CLIs but keeps cache-miss CLIs in
// the chain so the inline probe path can fill the cache.
func TestRouter_Verifier_ResolveWithFallback_OrderRespected(t *testing.T) {
	profiles := map[string]*config.CLIProfile{
		"cli-a": {Name: "cli-a", Capabilities: []string{"coding"}},
		"cli-b": {Name: "cli-b", Capabilities: []string{"coding"}},
		"cli-c": {Name: "cli-c", Capabilities: []string{"coding"}},
	}

	r := routing.NewRouterWithPriority(
		map[string]types.RolePreference{},
		[]string{"cli-a", "cli-b", "cli-c"},
		profiles,
		[]string{"cli-a", "cli-b", "cli-c"},
	)

	r.SetCapabilityVerifier(&stubVerifier{
		verified: map[string]bool{
			"cli-a|coding": true,  // verified
			"cli-b|coding": false, // excluded
			// cli-c absent → cache miss → kept in chain via declared fallback
		},
	})

	chain := r.ResolveWithFallback("coding")
	var clis []string
	for _, p := range chain {
		clis = append(clis, p.CLI)
	}

	// cli-b must be excluded; cli-a (verified) primary, cli-c (miss/declared)
	// kept as a soft fallback so inline probing has a path forward.
	for _, c := range clis {
		if c == "cli-b" {
			t.Errorf("ResolveWithFallback chain includes cli-b (verified=false); chain=%v", clis)
		}
	}
	if len(clis) == 0 || clis[0] != "cli-a" {
		t.Errorf("primary CLI: want cli-a, got chain=%v", clis)
	}
}
