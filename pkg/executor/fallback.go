package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/thebtf/aimux/pkg/metrics"
	"github.com/thebtf/aimux/pkg/types"
)

// fallbackVerbose caches AIMUX_FALLBACK_VERBOSE at process start.
// The env var is expected to be static for the lifetime of the process.
// Cached here to avoid repeated os.Getenv calls on every fallback attempt.
var fallbackVerbose = os.Getenv("AIMUX_FALLBACK_VERBOSE") != "false"

// ErrQuotaExhausted is wrapped by RunWithModelFallback when a model is rate-limited.
// The outer CLI-fallback router detects this via errors.Is to advance to the next CLI.
var ErrQuotaExhausted = errors.New("quota exhausted")

// ErrModelUnavailable is wrapped when a model is inaccessible (wrong account, not enabled, etc).
// Like ErrQuotaExhausted, flagged via errors.Is so the outer router advances CLI.
var ErrModelUnavailable = errors.New("model unavailable")

// errorClassToResult maps an ErrorClass to the result label used in structured logs and metrics.
func errorClassToResult(ec ErrorClass) string {
	switch ec {
	case ErrorClassNone:
		return metrics.FallbackResultSuccess
	case ErrorClassQuota:
		return metrics.FallbackResultRateLimit
	case ErrorClassModelUnavailable:
		return metrics.FallbackResultUnavailable
	case ErrorClassTransient:
		return metrics.FallbackResultTransient
	case ErrorClassFatal:
		return metrics.FallbackResultFatal
	default:
		return metrics.FallbackResultTransient
	}
}

// RunWithModelFallback is the canonical model-fallback state machine used by both
// the server and the agents runner. It iterates the model chain, applying cooldown
// tracking, transient-retry logic, and fatal-error short-circuit in a single place.
//
// On quota error: marks model cooled down, tries next model.
// On transient error: retries same model once, then applies full classification to
// the retry result.
// On fatal error: returns immediately.
// On success: returns result.
// When all models are on cooldown the returned error contains "rate limit" so that
// callers that check for retriable conditions will advance to the next CLI.
//
// counter may be nil; if non-nil, aimux_fallback_attempts_total is incremented on
// every attempt. AIMUX_FALLBACK_VERBOSE=false suppresses logFn calls (counter still
// increments).
func RunWithModelFallback(
	ctx context.Context,
	exec types.Executor,
	baseArgs types.SpawnArgs,
	models []string,
	modelFlag string,
	cooldown types.ModelCooldownTracker,
	cooldownDuration time.Duration,
	logFn func(format string, args ...any),
	counter *metrics.FallbackCounter,
) (*types.Result, error) {
	cli := baseArgs.CLI
	verbose := fallbackVerbose
	attemptIdx := 0

	if cooldownDuration == 0 {
		cooldownDuration = 24 * time.Hour // default: 24h (spark weekly limits can exhaust for days)
	}

	available := cooldown.FilterAvailable(cli, models)
	if len(available) == 0 {
		// Include "rate limit" so callers can treat this as a retriable condition.
		return nil, fmt.Errorf("all models on cooldown (rate limit) for CLI %s", cli)
	}

	// recordAttempt emits a structured log line and increments the fallback counter.
	// cooldownRemaining is the number of seconds until the model becomes available again
	// (0 if the model was not just cooled down).
	recordAttempt := func(idx int, model string, ec ErrorClass, latencyMs, cooldownRemaining int64) {
		result := errorClassToResult(ec)
		if counter != nil {
			counter.Inc(cli, model, result)
		}
		if verbose && logFn != nil {
			modelLabel := model
			if modelLabel == "" {
				modelLabel = "<unset>"
			}
			logFn("module=executor.fallback attempt=%d cli=%s model=%s result=%s error_class=%d latency_ms=%d cooldown_seconds_remaining=%d",
				idx, cli, modelLabel, result, int(ec), latencyMs, cooldownRemaining)
		}
	}

	var lastErr error
	for _, model := range available {
		attemptIdx++
		args := baseArgs
		args.Args = ReplaceModelFlag(baseArgs.Args, modelFlag, model)

		start := time.Now()
		result, err := exec.Run(ctx, args)
		latencyMs := time.Since(start).Milliseconds()

		var content, stderr string
		var exitCode int
		if result != nil {
			content = result.Content
			exitCode = result.ExitCode
		}
		if err != nil {
			stderr = err.Error()
		}

		errClass := ClassifyError(content, stderr, exitCode)

		switch errClass {
		case ErrorClassNone:
			recordAttempt(attemptIdx, model, errClass, latencyMs, 0)
			return result, err

		case ErrorClassQuota:
			cooldown.MarkCooledDown(cli, model, cooldownDuration)
			recordAttempt(attemptIdx, model, errClass, latencyMs, int64(cooldownDuration.Seconds()))
			lastErr = fmt.Errorf("%w for %s:%s", ErrQuotaExhausted, cli, model)
			continue

		case ErrorClassModelUnavailable:
			cooldown.MarkCooledDown(cli, model, cooldownDuration)
			recordAttempt(attemptIdx, model, errClass, latencyMs, int64(cooldownDuration.Seconds()))
			lastErr = fmt.Errorf("%w for %s:%s", ErrModelUnavailable, cli, model)
			continue

		case ErrorClassTransient:
			// Retry same model once, then apply full classification to the retry result.
			recordAttempt(attemptIdx, model, errClass, latencyMs, 0)
			attemptIdx++
			retryStart := time.Now()
			result2, err2 := exec.Run(ctx, args)
			retryLatencyMs := time.Since(retryStart).Milliseconds()
			var c2, s2 string
			var ec2 int
			if result2 != nil {
				c2 = result2.Content
				ec2 = result2.ExitCode
			}
			if err2 != nil {
				s2 = err2.Error()
			}
			retryClass := ClassifyError(c2, s2, ec2)
			switch retryClass {
			case ErrorClassNone:
				recordAttempt(attemptIdx, model, retryClass, retryLatencyMs, 0)
				return result2, err2
			case ErrorClassQuota:
				cooldown.MarkCooledDown(cli, model, cooldownDuration)
				recordAttempt(attemptIdx, model, retryClass, retryLatencyMs, int64(cooldownDuration.Seconds()))
				lastErr = fmt.Errorf("%w for %s:%s (on transient retry)", ErrQuotaExhausted, cli, model)
				continue
			case ErrorClassModelUnavailable:
				cooldown.MarkCooledDown(cli, model, cooldownDuration)
				recordAttempt(attemptIdx, model, retryClass, retryLatencyMs, int64(cooldownDuration.Seconds()))
				lastErr = fmt.Errorf("%w for %s:%s (on transient retry)", ErrModelUnavailable, cli, model)
				continue
			case ErrorClassFatal:
				recordAttempt(attemptIdx, model, retryClass, retryLatencyMs, 0)
				if err2 == nil {
					err2 = fmt.Errorf("fatal error detected in output")
				}
				return result2, fmt.Errorf("fatal error on %s:%s: %w", cli, model, err2)
			default:
				recordAttempt(attemptIdx, model, retryClass, retryLatencyMs, 0)
				lastErr = fmt.Errorf("transient error on %s:%s after retry", cli, model)
				continue
			}

		case ErrorClassFatal:
			recordAttempt(attemptIdx, model, errClass, latencyMs, 0)
			if err == nil {
				err = fmt.Errorf("fatal error detected in output")
			}
			return result, fmt.Errorf("fatal error on %s:%s: %w", cli, model, err)
		}
	}

	// Include "rate limit" in the error message when all models were cooled down due to
	// quota or model-unavailability. This allows the outer CLI-fallback router to advance
	// to the next CLI rather than treating the failure as a permanent error.
	if errors.Is(lastErr, ErrQuotaExhausted) || errors.Is(lastErr, ErrModelUnavailable) {
		return nil, fmt.Errorf("all models exhausted (rate limit) for CLI %s: %w", cli, lastErr)
	}
	return nil, fmt.Errorf("all models exhausted for CLI %s: %w", cli, lastErr)
}

// DetectModelFromArgs extracts the current model value from a CLI args slice.
// Returns "" if no model flag is found.
func DetectModelFromArgs(args []string, modelFlag string) string {
	if modelFlag == "" {
		return ""
	}
	eqPrefix := modelFlag + "="
	for i := 0; i < len(args); i++ {
		if args[i] == modelFlag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(args[i], eqPrefix) {
			return strings.TrimPrefix(args[i], eqPrefix)
		}
	}
	return ""
}

// BuildModelChain constructs the full fallback chain from explicit models and
// suffix-strip rules. The current model (from args) is detected and suffix-stripped
// variants are appended after the explicit list.
//
// Example: currentModel="gpt-5.3-codex-spark", explicit=[], suffixes=["-spark"]
// → chain=["gpt-5.3-codex-spark", "gpt-5.3-codex"]
//
// This survives model version upgrades — no hardcoded model names needed.
func BuildModelChain(currentModel string, explicitModels []string, suffixStrip []string) []string {
	seen := make(map[string]bool)
	var chain []string

	// Start with explicit models if provided.
	for _, m := range explicitModels {
		if !seen[m] {
			chain = append(chain, m)
			seen[m] = true
		}
	}

	// If current model is not in the explicit list, prepend it.
	if currentModel != "" && !seen[currentModel] {
		chain = append([]string{currentModel}, chain...)
		seen[currentModel] = true
	}

	// Generate suffix-stripped variants from the current model.
	for _, suffix := range suffixStrip {
		if strings.HasSuffix(currentModel, suffix) {
			stripped := strings.TrimSuffix(currentModel, suffix)
			if stripped != "" && !seen[stripped] {
				chain = append(chain, stripped)
				seen[stripped] = true
			}
		}
	}

	return chain
}
