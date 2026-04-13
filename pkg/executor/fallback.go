package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

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
func RunWithModelFallback(
	ctx context.Context,
	exec types.Executor,
	baseArgs types.SpawnArgs,
	models []string,
	modelFlag string,
	cooldown types.ModelCooldownTracker,
	cooldownDuration time.Duration,
	logFn func(format string, args ...any),
) (*types.Result, error) {
	cli := baseArgs.CLI

	if cooldownDuration == 0 {
		cooldownDuration = 5 * time.Minute
	}

	available := cooldown.FilterAvailable(cli, models)
	if len(available) == 0 {
		// Include "rate limit" so callers can treat this as a retriable condition.
		return nil, fmt.Errorf("all models on cooldown (rate limit) for CLI %s", cli)
	}

	var lastErr error
	for _, model := range available {
		args := baseArgs
		args.Args = ReplaceModelFlag(baseArgs.Args, modelFlag, model)

		result, err := exec.Run(ctx, args)

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
			return result, err

		case ErrorClassQuota:
			cooldown.MarkCooledDown(cli, model, cooldownDuration)
			if logFn != nil {
				logFn("model fallback: cli=%s model=%s → next (reason: quota, cooldown: %ds)",
					cli, model, int(cooldownDuration.Seconds()))
			}
			lastErr = fmt.Errorf("quota exceeded for %s:%s", cli, model)
			continue

		case ErrorClassTransient:
			// Retry same model once, then apply full classification to the retry result.
			if logFn != nil {
				logFn("model fallback: cli=%s model=%s transient error, retrying once", cli, model)
			}
			result2, err2 := exec.Run(ctx, args)
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
				return result2, err2
			case ErrorClassQuota:
				cooldown.MarkCooledDown(cli, model, cooldownDuration)
				lastErr = fmt.Errorf("quota exceeded for %s:%s (on transient retry)", cli, model)
				continue
			case ErrorClassFatal:
				if err2 == nil {
					err2 = fmt.Errorf("fatal error detected in output")
				}
				return result2, fmt.Errorf("fatal error on %s:%s: %w", cli, model, err2)
			default:
				lastErr = fmt.Errorf("transient error on %s:%s after retry", cli, model)
				continue
			}

		case ErrorClassFatal:
			if err == nil {
				err = fmt.Errorf("fatal error detected in output")
			}
			return result, fmt.Errorf("fatal error on %s:%s: %w", cli, model, err)
		}
	}

	return nil, fmt.Errorf("all models exhausted for CLI %s: %w", cli, lastErr)
}
