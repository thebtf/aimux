package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/thebtf/aimux/pkg/executor/redact"
	"github.com/thebtf/aimux/pkg/metrics"
	"github.com/thebtf/aimux/pkg/types"
)

// fallbackVerboseFlag caches AIMUX_FALLBACK_VERBOSE at process start using an
// atomic bool. The env var is expected to be static for the lifetime of the
// process; caching avoids a repeated os.Getenv syscall on every fallback attempt.
// Tests may override via setFallbackVerboseForTest (see export_test.go).
var fallbackVerboseFlag atomic.Bool

func init() {
	fallbackVerboseFlag.Store(os.Getenv("AIMUX_FALLBACK_VERBOSE") != "false")
}

// ErrQuotaExhausted is wrapped by RunWithModelFallback when a model is rate-limited.
// The outer CLI-fallback router detects this via errors.Is to advance to the next CLI.
var ErrQuotaExhausted = errors.New("quota exhausted")

// ErrModelUnavailable is wrapped when a model is inaccessible (wrong account, not enabled, etc).
// Like ErrQuotaExhausted, flagged via errors.Is so the outer router advances CLI.
var ErrModelUnavailable = errors.New("model unavailable")

// RecordResultToBreaker maps an ErrorClass to the corresponding breaker
// state transition per AIMUX-16 FR-2 / EC-2.3:
//
//   - ErrorClassNone        → RecordSuccess (HalfOpen probe → Closed)
//   - ErrorClassFatal       → RecordFailure(permanent=true) — trips breaker
//     immediately; auth/config errors are not transient.
//   - ErrorClassQuota       → RecordFailure(permanent=false) — counts toward
//     the configured failure threshold; sustained quota exhaustion across
//     attempts trips the breaker.
//   - ErrorClassTransient   → no-op (EC-2.3: flapping network MUST NOT
//     trip the breaker — would otherwise mask intermittent connectivity
//     blips as CLI death and waste the cooldown window).
//   - ErrorClassModelUnavailable → no-op. ModelUnavailable means *one* model
//     on the CLI is inaccessible; other models on the same CLI may still be
//     fine. Only the cooldown tracker (per-(cli,model)) marks the model.
//   - ErrorClassUnknown     → no-op. Conservative: an unrecognised exit
//     could be transient; don't penalise the CLI for ambiguous failures.
//
// Safe to call with a nil breaker or empty cli — both paths early-return as
// no-ops, so callers do not need to guard the call site.
func RecordResultToBreaker(breaker *BreakerRegistry, cli string, errClass ErrorClass) {
	if breaker == nil || cli == "" {
		return
	}
	cb := breaker.Get(cli)
	switch errClass {
	case ErrorClassNone:
		cb.RecordSuccess()
	case ErrorClassFatal:
		cb.RecordFailure(true) // permanent — trip immediately
	case ErrorClassQuota:
		cb.RecordFailure(false) // counts toward threshold
	default:
		// Transient / ModelUnavailable / Unknown: deliberate no-op.
		// See EC-2.3 in spec.md.
	}
}

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

// truncateStr truncates s to at most n bytes for error excerpts,
// respecting UTF-8 rune boundaries so no multi-byte sequence is split.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Walk back from byte n until we land on a valid rune boundary.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
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
	verbose := fallbackVerboseFlag.Load()
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
			cooldown.MarkCooledDown(cli, model, cooldownDuration, stderr)
			recordAttempt(attemptIdx, model, errClass, latencyMs, int64(cooldownDuration.Seconds()))
			lastErr = fmt.Errorf("%w for %s:%s", ErrQuotaExhausted, cli, model)
			continue

		case ErrorClassModelUnavailable:
			cooldown.MarkCooledDown(cli, model, cooldownDuration, stderr)
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
				cooldown.MarkCooledDown(cli, model, cooldownDuration, s2)
				recordAttempt(attemptIdx, model, retryClass, retryLatencyMs, int64(cooldownDuration.Seconds()))
				lastErr = fmt.Errorf("%w for %s:%s (on transient retry)", ErrQuotaExhausted, cli, model)
				continue
			case ErrorClassModelUnavailable:
				cooldown.MarkCooledDown(cli, model, cooldownDuration, s2)
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

		default: // ErrorClassUnknown (5) — non-zero exit with unrecognised message
			// Redact before truncation: truncating first can split a secret at the
			// boundary, preventing the regex from matching and leaking a key prefix.
			excerpt := truncateStr(redact.RedactSecrets(stderr), 200)
			if excerpt == "" && content != "" {
				excerpt = truncateStr(redact.RedactSecrets(content), 200)
			}
			lastErr = fmt.Errorf("unknown error on %s:%s (exit=%d): %s",
				cli, model, exitCode, excerpt)
			recordAttempt(attemptIdx, model, ErrorClassUnknown, latencyMs, 0)
			continue
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

// RunWithModelFallbackBreaker is the breaker-aware variant of
// RunWithModelFallback. It wraps the existing model-fallback state machine
// and records the dispatch outcome to the supplied BreakerRegistry per
// AIMUX-16 FR-2:
//
//   - Successful run (no error) → RecordSuccess for the CLI; this is the
//     transition that flips a HalfOpen probe back to Closed and zeroes the
//     failure counter.
//   - Fatal-classified terminal error → RecordFailure(permanent=true);
//     trips the breaker immediately so the next dispatch skips this CLI.
//   - Quota terminal error (all models exhausted on quota grounds) →
//     RecordFailure(permanent=false); counts toward the threshold so
//     sustained quota failure across calls eventually trips the breaker.
//   - ModelUnavailable terminal error → no breaker write. ModelUnavailable
//     is a per-(cli,model) concern — the cooldown tracker already marks
//     the offending model so the next dispatch skips it; tripping the
//     CLI-level breaker would mask other models on the same CLI that may
//     still be healthy.
//   - Transient terminal error → no breaker write (EC-2.3: flapping
//     network MUST NOT trip the breaker).
//   - Unknown terminal error → no breaker write (conservative; could be
//     transient under the hood).
//
// When breaker is nil this function is equivalent to RunWithModelFallback —
// no extra allocations, no extra side effects. Callers that have not yet
// adopted breaker tracking can still call this entry point safely.
//
// The caller is expected to have already filtered the dispatch CLI list
// with SelectAvailableCLIs, so the breaker is consulted on BOTH ends of
// the dispatch: pre-dispatch (admission control) and post-dispatch
// (state update).
func RunWithModelFallbackBreaker(
	ctx context.Context,
	exec types.Executor,
	baseArgs types.SpawnArgs,
	models []string,
	modelFlag string,
	cooldown types.ModelCooldownTracker,
	cooldownDuration time.Duration,
	logFn func(format string, args ...any),
	counter *metrics.FallbackCounter,
	breaker *BreakerRegistry,
) (*types.Result, error) {
	result, err := RunWithModelFallback(
		ctx,
		exec,
		baseArgs,
		models,
		modelFlag,
		cooldown,
		cooldownDuration,
		logFn,
		counter,
	)
	if breaker == nil {
		return result, err
	}

	cli := baseArgs.CLI
	terminalClass := classifyTerminalOutcome(result, err)
	RecordResultToBreaker(breaker, cli, terminalClass)

	return result, err
}

// classifyTerminalOutcome reduces the (result, err) pair returned by
// RunWithModelFallback to a single ErrorClass that drives the breaker
// state transition.
//
// Mapping rationale:
//   - err == nil  → ErrorClassNone (success).
//   - err wraps ErrQuotaExhausted → ErrorClassQuota. Quota exhaustion of the
//     entire CLI (all models cooled down on quota grounds) is a CLI-level
//     concern: sustained occurrences should count toward the breaker
//     threshold so the dispatcher eventually skips this CLI.
//   - err wraps ErrModelUnavailable → ErrorClassModelUnavailable (NOT Quota).
//     ModelUnavailable is a per-(cli,model) concern — wrong account, model
//     not enabled, plan-gated access. The cooldown tracker already marks
//     the offending model so the next dispatch skips it; tripping the
//     CLI-level breaker would mask other models on the same CLI that may
//     still be healthy. Per RecordResultToBreaker (EC-2.3), ModelUnavailable
//     terminal outcomes are intentionally a no-op for the breaker.
//   - else: re-classify via ClassifyError on the result/err pair using the
//     same patterns the per-attempt loop uses, so terminal Fatal/Transient
//     stay aligned with how the loop classified individual attempts.
func classifyTerminalOutcome(result *types.Result, err error) ErrorClass {
	if err == nil {
		return ErrorClassNone
	}
	if errors.Is(err, ErrQuotaExhausted) {
		return ErrorClassQuota
	}
	if errors.Is(err, ErrModelUnavailable) {
		return ErrorClassModelUnavailable
	}
	var content, stderr string
	exitCode := 1 // err != nil at this point, so exit code is non-zero by definition
	if result != nil {
		content = result.Content
		// Only adopt result.ExitCode if it is non-zero; an exitCode of 0 here would
		// make ClassifyError return ErrorClassNone despite err != nil, masking the
		// terminal failure as success and yielding a false RecordSuccess() on the breaker.
		if result.ExitCode != 0 {
			exitCode = result.ExitCode
		}
	}
	stderr = err.Error()
	return ClassifyError(content, stderr, exitCode)
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
// variants are appended after the explicit list, but ONLY when errClass indicates
// a quota or model-unavailability error — wasted retries on a suffix-stripped base
// model are pointless if the failure was a transient network blip or unknown error.
//
// Example (errClass=ErrorClassQuota):
//
//	currentModel="gpt-5.3-codex-spark", explicit=[], suffixes=["-spark"]
//	→ chain=["gpt-5.3-codex-spark", "gpt-5.3-codex"]
//
// Example (errClass=ErrorClassUnknown):
//
//	currentModel="gpt-5.3-codex-spark", explicit=[], suffixes=["-spark"]
//	→ chain=["gpt-5.3-codex-spark"]   (no suffix-stripped variant)
//
// Pass ErrorClassNone when building the initial chain before any error is known.
func BuildModelChain(currentModel string, explicitModels []string, suffixStrip []string, errClass ErrorClass) []string {
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

	// Generate suffix-stripped variants only when the prior error was quota or
	// model-unavailability. For transient, fatal, or unknown errors the base model
	// is unlikely to succeed either, so the extra attempt wastes a request slot.
	if errClass == ErrorClassQuota || errClass == ErrorClassModelUnavailable {
		for _, suffix := range suffixStrip {
			if strings.HasSuffix(currentModel, suffix) {
				stripped := strings.TrimSuffix(currentModel, suffix)
				if stripped != "" && !seen[stripped] {
					chain = append(chain, stripped)
					seen[stripped] = true
				}
			}
		}
	}

	return chain
}
