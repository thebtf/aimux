// Package driver — warmup probe for health-gating CLIs at daemon startup.
package driver

import (
	"context"
	"encoding/json"
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
	return runWarmupWithExec(ctx, reg, cfg, pipeExec.New())
}

// runWarmupWithExec is the testable core of RunWarmup. It accepts an injected
// executor so tests can supply a mock without spawning real processes.
func runWarmupWithExec(ctx context.Context, reg *Registry, cfg *config.Config, exec types.Executor) error {
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

	for r := range results {
		if r.isQuota {
			// Quota: CLI is healthy, just rate-limited. Model cooldown is handled
			// by the executor layer during actual requests. Mark available so a
			// previously-unavailable CLI can come back online on the next refresh.
			reg.SetAvailable(r.cli, true)
			continue
		}
		// Always set availability explicitly from the probe outcome. Passing
		// probes re-enable CLIs that an earlier warmup marked unavailable.
		reg.SetAvailable(r.cli, r.passed)
	}

	return nil
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
