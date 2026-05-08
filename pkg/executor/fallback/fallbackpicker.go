package fallback

import (
	"context"
	"errors"
	"fmt"

	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/aimux/pkg/executor/types"
)

// FallbackPicker composes Picker + Fallback into a single entry point (spec FR-10).
//
// Run flow:
//  1. Picker.Pick → primary CLI
//  2. Dispatch primary (via dispatch callback)
//  3. On failure → FailureClassifier → if Eligible → Fallback.Retry
//  4. Return Result with selected_cli + failed_attempts, or error
//
// FallbackPicker is goroutine-safe after construction.
type FallbackPicker struct {
	p     *picker.Picker
	fb    *Fallback
	store ScoreStore
	cfg   *FallbackConfig
}

// NewFallbackPicker constructs a FallbackPicker.
// All arguments must be non-nil.
func NewFallbackPicker(p *picker.Picker, fb *Fallback, store ScoreStore, cfg *FallbackConfig) *FallbackPicker {
	if p == nil || fb == nil || store == nil || cfg == nil {
		panic("fallback: FallbackPicker: all constructor arguments must be non-nil")
	}
	return &FallbackPicker{p: p, fb: fb, store: store, cfg: cfg}
}

// RunOptions controls per-call behavior overrides.
type RunOptions struct {
	// FallbackEnabled overrides the global FallbackConfig.IsEnabled() for this call.
	// nil = use config default (true).
	FallbackEnabled *bool
	// MaxAttempts overrides FallbackConfig.MaxAttempts for this call.
	// 0 = use config default.
	MaxAttempts int
}

// FallbackEnabled reports whether fallback should run for these options.
func (o RunOptions) fallbackEnabled(cfg *FallbackConfig) bool {
	if o.FallbackEnabled != nil {
		return *o.FallbackEnabled
	}
	return cfg.IsEnabled()
}

// Run selects the best CLI via Picker, dispatches the task, and — if the primary
// CLI fails with an eligible error — invokes Fallback.Retry with remaining candidates.
//
// dispatch is called with the selected CLI name and the (possibly translated) TaskSpec.
// It must return *types.CLIError on failure for the classifier to work correctly.
//
// opts may be a zero-value RunOptions to use all defaults.
func (fp *FallbackPicker) Run(
	ctx context.Context,
	spec picker.TaskSpec,
	opts RunOptions,
	dispatch DispatchFn,
) (Result, error) {
	// Step 1: Pick primary CLI.
	primaryCLI, err := fp.p.Pick(ctx, spec)
	if err != nil {
		// Picker failed (e.g., ErrNoHealthyCLI) — no fallback possible.
		return Result{}, fmt.Errorf("fallback picker: no CLI available: %w", err)
	}

	return fp.RunPrimary(ctx, primaryCLI, spec, opts, dispatch)
}

// PickPair exposes the underlying healthy cross-family pair selection for
// orchestrators that dispatch driver and navigator subtasks themselves.
func (fp *FallbackPicker) PickPair(ctx context.Context, taskClass string) (types.CLIName, types.CLIName, error) {
	if fp == nil || fp.p == nil {
		return "", "", types.NewCapabilityMismatch("fallback picker requires an initialized picker", nil)
	}
	return fp.p.PickPair(ctx, taskClass)
}

// RunPrimary dispatches a caller-selected primary CLI, then uses the fallback
// chain for eligible failures. Use this when a higher-level role already picked
// the primary CLI but still wants fallback behavior on transient failures.
func (fp *FallbackPicker) RunPrimary(
	ctx context.Context,
	primaryCLI string,
	spec picker.TaskSpec,
	opts RunOptions,
	dispatch DispatchFn,
) (Result, error) {
	if primaryCLI == "" {
		return fp.Run(ctx, spec, opts, dispatch)
	}

	// Step 2: Dispatch primary CLI.
	start := nowMS()
	content, dispatchErr := dispatch(ctx, primaryCLI, spec)
	elapsed := nowMS() - start

	if dispatchErr == nil {
		// Happy path: primary succeeded.
		fp.store.RecordSuccess(primaryCLI, elapsed)
		return Result{
			Content:        content,
			SelectedCLI:    primaryCLI,
			FailedAttempts: nil,
		}, nil
	}

	return fp.retryAfterPrimaryFailure(ctx, primaryCLI, spec, opts, dispatchErr, dispatch)
}

func (fp *FallbackPicker) retryAfterPrimaryFailure(
	ctx context.Context,
	primaryCLI string,
	spec picker.TaskSpec,
	opts RunOptions,
	dispatchErr error,
	dispatch DispatchFn,
) (Result, error) {
	// Step 3: Primary failed — record the failure and check eligibility.
	errCode := errorCode(dispatchErr)
	fp.store.RecordFailure(primaryCLI, errCode)

	primaryAttempt := FailedAttempt{
		CLI:     primaryCLI,
		Code:    errCode.String(),
		Message: dispatchErr.Error(),
	}

	// Step 3a: Check for explicit opt-out.
	if !opts.fallbackEnabled(fp.cfg) {
		// Per spec FR-6: surface raw error without retry.
		return Result{}, dispatchErr
	}

	// Step 3b: Check global enable flag.
	if !fp.cfg.IsEnabled() {
		return Result{}, dispatchErr
	}

	// Step 3c: Check max_attempts: 0 means no fallback at all (spec edge case table).
	effectiveMax := opts.MaxAttempts
	if effectiveMax <= 0 {
		effectiveMax = fp.cfg.maxAttempts()
	}
	if effectiveMax == 0 {
		return Result{}, dispatchErr
	}

	// Step 3d: Check classifier — terminal errors surface immediately.
	classifier := fp.fb.classifier
	if classifier.Classify(dispatchErr) == Terminal {
		return Result{}, dispatchErr
	}

	// Check for ErrNoHealthyCLI — not a CLIError, always terminal.
	var noHealthy *picker.ErrNoHealthyCLI
	if errors.As(dispatchErr, &noHealthy) {
		return Result{}, dispatchErr
	}

	// Step 4: Invoke Fallback.Retry.
	fctx := FailureCtx{
		PriorAttempts:       []FailedAttempt{primaryAttempt},
		LastError:           dispatchErr,
		MaxAttemptsOverride: effectiveMax,
	}

	result, retryErr := fp.fb.Retry(ctx, spec, fctx, func(ctx context.Context, cli string, s picker.TaskSpec) (string, error) {
		retStart := nowMS()
		c, e := dispatch(ctx, cli, s)
		retElapsed := nowMS() - retStart
		if e == nil {
			fp.store.RecordSuccess(cli, retElapsed)
		} else {
			fp.store.RecordFailure(cli, errorCode(e))
		}
		return c, e
	})

	if retryErr != nil {
		// Fallback also failed — wrap with exhaustion context if not already.
		var exhausted *ErrAllFallbackExhausted
		if errors.As(retryErr, &exhausted) {
			return Result{}, retryErr
		}
		return Result{}, retryErr
	}

	// Note: Retry's dispatch wrapper already recorded success into store above,
	// but the result.SelectedCLI needs the primary attempt prepended.
	// Retry builds its own failed-attempts list starting from fctx.PriorAttempts,
	// so the returned result already includes the primary attempt.
	return result, nil
}
