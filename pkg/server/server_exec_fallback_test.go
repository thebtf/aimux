package server

// T015b — unit tests for buildFallbackCandidates (FR-1.4 filter branches).
//
// Five table-driven cases:
//  1. No role set → primary only (router not consulted)
//  2. Fallback available, breaker closed → included
//  3. Fallback breaker open → skipped (reason=breaker_open)
//  4. Fallback failure rate ≥95% → skipped (reason=failure_rate)
//  5. RequiresTTY=true and ConPTY unavailable → skipped (reason=no_tty)
//
// Tests run in package server (same package) so they can access unexported fields.

import (
	"runtime"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/metrics"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/types"
)

// newFallbackTestServer creates a server with two CLIs: "primary" and "fallback".
// Both are registered in the "impl" role. The caller can tweak fields (breaker,
// metrics, profile) before calling buildFallbackCandidates.
func newFallbackTestServer(t *testing.T) *Server {
	t.Helper()

	cfg := &config.Config{
		Server: config.ServerConfig{
			LogLevel: "error",
			LogFile:  t.TempDir() + "/test.log",
		},
		Roles: map[string]types.RolePreference{
			"impl": {CLI: "primary"},
		},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 1,
			CooldownSeconds:  60, // long so breaker stays open during test
			HalfOpenMaxCalls: 1,
		},
		CLIProfiles: map[string]*config.CLIProfile{
			"primary": {
				Name:    "primary",
				Binary:  "echo",
				Command: config.CommandConfig{Base: "echo"},
			},
			"fallback": {
				Name:    "fallback",
				Binary:  "echo",
				Command: config.CommandConfig{Base: "echo"},
			},
		},
		ConfigDir: t.TempDir(),
	}

	log, err := logger.New(cfg.Server.LogFile, logger.LevelError)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	t.Cleanup(func() { log.Close() })

	// NewRouter with both CLIs enabled — "primary" is the configured default for
	// role "impl". ResolveWithFallback will return both (primary first, fallback
	// second) because both are in enabledCLIs.
	router := routing.NewRouter(
		map[string]types.RolePreference{
			"impl": {CLI: "primary"},
		},
		[]string{"primary", "fallback"},
	)

	reg := driver.NewRegistry(cfg.CLIProfiles)
	srv := New(cfg, log, reg, router)
	srv.metrics = metrics.New()
	return srv
}

// cliSlice extracts CLI names from a RolePreference slice for compact assertions.
func cliSlice(prefs []types.RolePreference) []string {
	names := make([]string, len(prefs))
	for i, p := range prefs {
		names[i] = p.CLI
	}
	return names
}

// containsOnly returns true when the slice contains exactly the given names
// in any order but no extras.
func containsOnly(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	set := make(map[string]bool, len(want))
	for _, w := range want {
		set[w] = true
	}
	for _, g := range got {
		if !set[g] {
			return false
		}
	}
	return true
}

// TestBuildFallbackCandidates_NoRole verifies that when role is empty the
// function returns [primary] immediately without consulting the router.
func TestBuildFallbackCandidates_NoRole(t *testing.T) {
	srv := newFallbackTestServer(t)
	cb := srv.breakers.Get("primary")

	result := srv.buildFallbackCandidates("", "primary", cb)

	if len(result) != 1 || result[0].CLI != "primary" {
		t.Errorf("no-role: want [primary], got %v", cliSlice(result))
	}
}

// TestBuildFallbackCandidates_FallbackIncluded verifies that a healthy fallback
// CLI is included when the circuit breaker is closed and failure rate is low.
func TestBuildFallbackCandidates_FallbackIncluded(t *testing.T) {
	srv := newFallbackTestServer(t)
	cb := srv.breakers.Get("primary")

	// Breaker closed, metrics at zero — fallback should pass all three filters.
	result := srv.buildFallbackCandidates("impl", "primary", cb)

	names := cliSlice(result)
	if !containsOnly(names, "primary", "fallback") {
		t.Errorf("healthy fallback: want [primary fallback], got %v", names)
	}
	if result[0].CLI != "primary" {
		t.Errorf("primary must be first, got %v", names)
	}
}

// TestBuildFallbackCandidates_BreakerOpen verifies that a fallback CLI with an
// open circuit breaker is skipped (filter 1: reason=breaker_open).
func TestBuildFallbackCandidates_BreakerOpen(t *testing.T) {
	srv := newFallbackTestServer(t)
	cb := srv.breakers.Get("primary")

	// Trip the fallback breaker.
	srv.breakers.Get("fallback").RecordFailure(true /* permanent */)
	if srv.breakers.Get("fallback").State() != executor.BreakerOpen {
		t.Fatal("pre-condition: fallback breaker should be open")
	}

	result := srv.buildFallbackCandidates("impl", "primary", cb)

	names := cliSlice(result)
	if !containsOnly(names, "primary") {
		t.Errorf("breaker_open: want [primary] only, got %v", names)
	}
}

// TestBuildFallbackCandidates_FailureRate verifies that a fallback CLI with
// ≥95% failure rate over ≥10 requests is skipped (filter 2: reason=failure_rate).
func TestBuildFallbackCandidates_FailureRate(t *testing.T) {
	srv := newFallbackTestServer(t)
	cb := srv.breakers.Get("primary")

	// Record 10 requests for "fallback" — 10 errors → 100% failure rate.
	for i := 0; i < 10; i++ {
		srv.metrics.RecordRequest("fallback", "", 10, true /* isError */)
	}

	result := srv.buildFallbackCandidates("impl", "primary", cb)

	names := cliSlice(result)
	if !containsOnly(names, "primary") {
		t.Errorf("failure_rate: want [primary] only, got %v", names)
	}
}

// TestBuildFallbackCandidates_RequiresTTY verifies that a fallback CLI with
// RequiresTTY=true is skipped when ConPTY is unavailable (filter 3: reason=no_tty).
// On Windows (where ConPTY IS available) this test asserts the reverse: the CLI
// IS included.
func TestBuildFallbackCandidates_RequiresTTY(t *testing.T) {
	srv := newFallbackTestServer(t)
	cb := srv.breakers.Get("primary")

	// Mark fallback CLI as requiring a TTY.
	srv.cfg.CLIProfiles["fallback"].RequiresTTY = true

	result := srv.buildFallbackCandidates("impl", "primary", cb)
	names := cliSlice(result)

	if runtime.GOOS == "windows" {
		// ConPTY IS available on Windows — RequiresTTY CLI should be included.
		if !containsOnly(names, "primary", "fallback") {
			t.Errorf("windows/require_tty: want [primary fallback], got %v", names)
		}
	} else {
		// ConPTY unavailable (non-Windows) — RequiresTTY CLI should be skipped.
		if !containsOnly(names, "primary") {
			t.Errorf("non-windows/require_tty: want [primary] only, got %v", names)
		}
	}
}
