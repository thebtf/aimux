// Package driver — warmup probe for health-gating CLIs at daemon startup.
//
// AIMUX-16 CR-003 (FR-3): warmup performs two distinct probes per CLI.
//
//  1. Liveness: legacy `{"ok":true}` JSON probe — gates the CLI's presence in
//     the routing pool. Same contract as v4.x.
//  2. Per-(cli, role) capability: for each role declared in profile.Capabilities,
//     a role-shaped probe verifies the CLI actually responds with role-aware
//     output. Result lands in the CapabilityCache; routing reads the cache.
//
// The capability probe pass is best-effort — a CLI that fails liveness is
// still excluded; a CLI that passes liveness but fails capability for role X
// stays in the pool but is excluded from role-X dispatch via the cache.
package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/executor"
	pipeExec "github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/resolve"
	"github.com/thebtf/aimux/pkg/types"
)

const (
	// defaultWarmupTimeout is used when neither profile nor server config sets a timeout.
	defaultWarmupTimeout = 15 * time.Second

	// defaultProbePrompt instructs the CLI to emit a minimal JSON response.
	// A well-behaved CLI that can process prompts will reply with {"ok":true}.
	defaultProbePrompt = `reply with JSON: {"ok": true}`

	// defaultCapabilityProbeTimeout bounds the wall-clock budget of a single
	// per-(cli, role) probe. AIMUX-16 EC-3.2 documents graceful degradation
	// when the budget is exceeded — declared capability stays as fallback.
	defaultCapabilityProbeTimeout = 5 * time.Second
)

// warmupResult captures the outcome of a single CLI probe.
type warmupResult struct {
	cli     string
	passed  bool
	isQuota bool // quota error → CLI stays enabled, apply cooldown
	err     error
}

// RunWarmup probes every enabled CLI with a minimal JSON prompt and marks
// non-responsive CLIs as unavailable in the registry for this daemon lifetime.
//
// Opt-out: set AIMUX_WARMUP=false to skip all probes (binary-only detection).
//
// Quota errors do NOT exclude a CLI — they trigger model cooldown instead and
// leave the CLI available for subsequent requests.
func RunWarmup(ctx context.Context, reg *Registry, cfg *config.Config) error {
	return runWarmupWithExec(ctx, reg, cfg, pipeExec.New(), nil)
}

// RunWarmupWithCache is RunWarmup plus a per-(cli, role) capability probe pass.
// Each declared capability for each live CLI is probed with a role-shaped
// prompt; results land in cache. Routing reads cache to gate per-role
// dispatch. Cache may be nil — in that case behaviour matches RunWarmup.
//
// AIMUX-16 CR-003 (FR-3, EC-3.1, EC-3.2, EC-3.3).
func RunWarmupWithCache(ctx context.Context, reg *Registry, cfg *config.Config, cache *CapabilityCache) error {
	return runWarmupWithExec(ctx, reg, cfg, pipeExec.New(), cache)
}

// runWarmupWithExec is the testable core of RunWarmup. It accepts an injected
// executor so tests can supply a mock without spawning real processes.
// cache may be nil — capability probes are skipped in that case.
func runWarmupWithExec(ctx context.Context, reg *Registry, cfg *config.Config, exec types.Executor, cache *CapabilityCache) error {
	// Env var takes precedence: AIMUX_WARMUP=false skips probes regardless of config.
	if os.Getenv("AIMUX_WARMUP") == "false" {
		return nil
	}
	// Config-level gate: warmup_enabled: false in default.yaml disables probes.
	// WarmupEnabled defaults to true (set in config.Load); only explicit false disables.
	if cfg != nil && !cfg.Server.WarmupEnabled {
		return nil
	}

	// Probe every CLI with a resolved binary, not just the currently-enabled ones.
	// This lets refresh-warmup re-enable a CLI that a prior warmup marked
	// unavailable (e.g. transient timeout) once its probe passes again.
	clis := reg.ProbeableCLIs()
	if len(clis) == 0 {
		return nil
	}

	results := make(chan warmupResult, len(clis))
	var wg sync.WaitGroup

	for _, name := range clis {
		profile, err := reg.Get(name)
		if err != nil {
			// Profile not found — skip; registry already excludes it.
			continue
		}

		wg.Add(1)
		go func(cliName string, prof *config.CLIProfile) {
			defer wg.Done()
			result := probeOne(ctx, exec, cliName, prof, cfg)
			results <- result
		}(name, profile)
	}

	// Close results channel once all goroutines finish.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Track which CLIs survived liveness (quota counts as alive — CLI is healthy,
	// just rate-limited). Capability probes only run for survivors so we don't
	// waste budget on a dead CLI.
	alive := make([]string, 0, len(clis))
	for r := range results {
		if r.isQuota {
			// Quota: CLI is healthy, just rate-limited. Model cooldown is handled
			// by the executor layer during actual requests. Mark available so a
			// previously-unavailable CLI can come back online on the next refresh.
			reg.SetAvailable(r.cli, true)
			alive = append(alive, r.cli)
			continue
		}
		// Always set availability explicitly from the probe outcome. Passing
		// probes re-enable CLIs that an earlier warmup marked unavailable.
		reg.SetAvailable(r.cli, r.passed)
		if r.passed {
			alive = append(alive, r.cli)
		}
	}

	// AIMUX-16 CR-003: per-(cli, role) capability probes. Skipped when cache is
	// nil (legacy RunWarmup path) so existing tests retain v4.x behavior.
	if cache != nil {
		runCapabilityProbes(ctx, reg, cfg, exec, cache, alive)
	}

	return nil
}

// runCapabilityProbes iterates every alive CLI's declared capabilities and
// records the role-shaped probe outcome in the cache.
//
// Probes run sequentially per CLI but in parallel across CLIs — one goroutine
// per CLI, each iterating its declared capabilities in profile order. This
// bounds the total wall-clock to the slowest CLI's probe budget × |roles|
// rather than fanning out a goroutine per (cli, role) tuple.
//
// Definitive failures (probe ran and the CLI did not acknowledge the role,
// quota errors wrapped explicitly, decode failures) are stored as
// verified=false so the refresher does not retry every tick. Transient
// context errors (DeadlineExceeded / Canceled) intentionally leave the slot
// UNTOUCHED — the spec's EC-3.2 graceful-degradation contract requires that
// a probe timeout surface as cache miss to the routing layer, allowing the
// declared capability to act as soft fallback while the next probe retries.
func runCapabilityProbes(ctx context.Context, reg *Registry, cfg *config.Config, exec types.Executor, cache *CapabilityCache, alive []string) {
	if len(alive) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, cli := range alive {
		profile, err := reg.Get(cli)
		if err != nil || profile == nil || len(profile.Capabilities) == 0 {
			continue
		}
		wg.Add(1)
		go func(cliName string, prof *config.CLIProfile) {
			defer wg.Done()
			for _, role := range prof.Capabilities {
				role = strings.TrimSpace(role)
				if role == "" {
					continue
				}
				verified, perr := probeRole(ctx, exec, cliName, prof, cfg, role)
				if isTransientProbeError(perr) {
					// EC-3.2: leave the slot empty so routing treats it as
					// miss → declared fallback. The refresher tick retries
					// the probe at the next interval.
					continue
				}
				cache.Set(cliName, role, verified, perr)
			}
		}(cli, profile)
	}
	wg.Wait()
}

// probeOne executes a single warmup probe for one CLI and returns the result.
func probeOne(ctx context.Context, exec types.Executor, name string, profile *config.CLIProfile, cfg *config.Config) warmupResult {
	timeout := resolveTimeout(profile, cfg)
	probePrompt := resolveProbePrompt(profile)

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := types.SpawnArgs{
		CLI:            name,
		Command:        resolve.CommandBinary(profile.Command.Base),
		Args:           resolve.BuildPromptArgs(profile, "", "", false, probePrompt),
		TimeoutSeconds: int(timeout.Seconds()),
	}

	result, err := exec.Run(probeCtx, args)
	if err != nil {
		// Check if this is a quota/rate-limit error — keep CLI enabled.
		if isQuotaError(err.Error()) {
			return warmupResult{cli: name, passed: true, isQuota: true}
		}
		return warmupResult{cli: name, passed: false, err: err}
	}

	// Parse stdout as JSON: {"ok": true} → pass; anything else → fail.
	if parseWarmupResponse(result.Content) {
		return warmupResult{cli: name, passed: true}
	}

	// Non-JSON or {"ok": false} — exclude CLI.
	return warmupResult{cli: name, passed: false}
}

// probeRole sends a role-shaped probe to (cli, role) and returns the verified
// outcome. The probe asks the CLI to acknowledge the role string in a JSON
// envelope; a CLI that returns valid JSON but does NOT echo the role string
// is treated as alive-but-role-incapable — the cache records verified=false.
//
// EC-3.2 graceful degradation: callers (runCapabilityProbes, refreshOnce) MUST
// inspect the returned err with isTransientProbeError. On context.Canceled
// or context.DeadlineExceeded the slot is left untouched so the routing
// layer sees a cache miss and uses the declared capability as soft fallback;
// the next refresher tick retries.
func probeRole(ctx context.Context, exec types.Executor, name string, profile *config.CLIProfile, cfg *config.Config, role string) (bool, error) {
	timeout := resolveCapabilityProbeTimeout(profile, cfg)
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := buildRoleProbePrompt(role)
	args := types.SpawnArgs{
		CLI:            name,
		Command:        resolve.CommandBinary(profile.Command.Base),
		Args:           resolve.BuildPromptArgs(profile, "", "", false, prompt),
		TimeoutSeconds: int(timeout.Seconds()),
	}

	result, err := exec.Run(probeCtx, args)
	if err != nil {
		// Quota during capability probe — CLI is alive, treat liveness probe's
		// quota handling as authoritative. Capability stays unverified for now;
		// the next refresher tick will retry once the quota window clears.
		if isQuotaError(err.Error()) {
			return false, fmt.Errorf("capability probe quota: %w", err)
		}
		return false, err
	}
	if parseRoleProbeResponse(result.Content, role) {
		return true, nil
	}
	return false, fmt.Errorf("capability probe %s/%s: response did not acknowledge role", name, role)
}

// buildRoleProbePrompt returns the canonical role-shaped probe text.
//
// The prompt asks the CLI to echo the role string in a JSON envelope, which
// distinguishes "alive but ignoring role context" from "alive and follows
// role context". A CLI that returns plain `{"ok":true}` is recorded as
// unverified for the role.
//
// AIMUX-16 CR-003 task note: a richer per-role prompt mapping (e.g. ask the
// CLI to perform a small role-shaped task) is a future iteration; the v1
// contract is the role-echo handshake which is sufficient to satisfy
// "по факту, не по таблице" while keeping the probe deterministic.
func buildRoleProbePrompt(role string) string {
	return fmt.Sprintf(`reply with JSON acknowledging role %q: {"role": %q, "ok": true}`, role, role)
}

// parseRoleProbeResponse returns true when content contains valid JSON with
// both ok=true AND role matching the expected role. Tolerates CLI preamble
// text via the same first-'{' scan as parseWarmupResponse.
func parseRoleProbeResponse(content, expectedRole string) bool {
	start := strings.Index(content, "{")
	if start < 0 {
		return false
	}
	var resp struct {
		OK   bool   `json:"ok"`
		Role string `json:"role"`
	}
	if err := json.NewDecoder(strings.NewReader(content[start:])).Decode(&resp); err != nil {
		return false
	}
	return resp.OK && resp.Role == expectedRole
}

// resolveCapabilityProbeTimeout returns the per-probe budget for the role
// probe. The fallback chain matches the contract documented in
// config/default.yaml + pkg/config/config.go:
//
//  1. profile.WarmupTimeoutSeconds (per-CLI override) when > 0.
//  2. cfg.Driver.CapabilityProbeTimeoutSeconds when > 0.
//  3. cfg.Server.WarmupTimeoutSeconds when > 0 (operator-tuned global
//     warmup budget — a zero capability_probe_timeout_seconds MUST inherit
//     the warmup budget, otherwise capability probes time out earlier than
//     normal warmup on installs that already raised the global timeout).
//  4. defaultCapabilityProbeTimeout (hard floor — 5s).
func resolveCapabilityProbeTimeout(profile *config.CLIProfile, cfg *config.Config) time.Duration {
	if profile != nil && profile.WarmupTimeoutSeconds > 0 {
		return time.Duration(profile.WarmupTimeoutSeconds) * time.Second
	}
	if cfg != nil {
		if cfg.Driver.CapabilityProbeTimeoutSeconds > 0 {
			return time.Duration(cfg.Driver.CapabilityProbeTimeoutSeconds) * time.Second
		}
		if cfg.Server.WarmupTimeoutSeconds > 0 {
			return time.Duration(cfg.Server.WarmupTimeoutSeconds) * time.Second
		}
	}
	return defaultCapabilityProbeTimeout
}

// MakeCapabilityProbeFn returns a ProbeFn closure wired to a real executor.
// The refresher and inline-miss paths use this to issue per-(cli, role)
// probes from the driver-package boundary; tests may pass a custom ProbeFn.
func MakeCapabilityProbeFn(reg *Registry, cfg *config.Config, exec types.Executor) ProbeFn {
	return func(ctx context.Context, cli, role string) (bool, error) {
		profile, err := reg.Get(cli)
		if err != nil {
			return false, err
		}
		return probeRole(ctx, exec, cli, profile, cfg, role)
	}
}

// parseWarmupResponse returns true when content contains valid JSON with ok=true.
// Scans for the first '{' to skip CLI preamble text, then decodes with
// json.NewDecoder which correctly handles braces inside string literals.
func parseWarmupResponse(content string) bool {
	start := strings.Index(content, "{")
	if start < 0 {
		return false
	}

	var resp struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(strings.NewReader(content[start:])).Decode(&resp); err != nil {
		return false
	}
	return resp.OK
}

// resolveTimeout picks the effective warmup timeout for a profile.
// Profile-level setting wins; falls back to server config; then hard default.
func resolveTimeout(profile *config.CLIProfile, cfg *config.Config) time.Duration {
	if profile.WarmupTimeoutSeconds > 0 {
		return time.Duration(profile.WarmupTimeoutSeconds) * time.Second
	}
	if cfg != nil && cfg.Server.WarmupTimeoutSeconds > 0 {
		return time.Duration(cfg.Server.WarmupTimeoutSeconds) * time.Second
	}
	return defaultWarmupTimeout
}

// resolveProbePrompt picks the effective probe prompt for a profile.
func resolveProbePrompt(profile *config.CLIProfile) string {
	if profile.WarmupProbePrompt != "" {
		return profile.WarmupProbePrompt
	}
	return defaultProbePrompt
}

// isQuotaError returns true when the error message indicates a quota/rate-limit.
// Mirrors the patterns in executor.ClassifyError without importing executor directly.
func isQuotaError(msg string) bool {
	ec := executor.ClassifyError(msg, "", 1)
	return ec == executor.ErrorClassQuota
}
