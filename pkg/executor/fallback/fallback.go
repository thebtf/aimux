package fallback

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/aimux/pkg/executor/types"
)

// timeNow is the clock function used by nowMSImpl.
// Replaced in tests to produce deterministic timing.
var timeNow = time.Now

// DispatchFn is the function signature for dispatching a task to a named CLI.
// Returns the CLI output content and any error. Errors must be *types.CLIError
// to be classified by FailureClassifier.
type DispatchFn func(ctx context.Context, cli string, spec picker.TaskSpec) (string, error)

// Result is the successful output of a fallback attempt chain.
type Result struct {
	// Content is the output returned by the winning CLI.
	Content string
	// SelectedCLI is the name of the CLI that ultimately succeeded.
	SelectedCLI string
	// FailedAttempts lists every CLI that was tried before the winner, in order.
	// Empty when the first attempt succeeded.
	FailedAttempts []FailedAttempt
}

// FailureCtx carries context about failures already incurred before Retry is called.
// Callers populate this before the first call to Retry so the attempt counter and
// metadata are accurate.
type FailureCtx struct {
	// PriorAttempts records CLIs that have already been tried in this chain.
	// Retry skips these CLIs when ordering candidates.
	PriorAttempts []FailedAttempt
	// LastError is the most recent CLIError that triggered this fallback call.
	LastError error
	// MaxAttemptsOverride caps fallback re-attempts for this call. Zero uses config.
	MaxAttemptsOverride int
}

// Fallback is the runtime re-rank engine (spec FR-1, architecture.md §3).
// It is called by FallbackPicker when the picker-selected primary CLI fails
// with an eligible error. It orchestrates:
//
//  1. FailureClassifier check (eligible vs terminal)
//  2. Orderer rank (re-rank remaining candidates)
//  3. Translator adapt (pass-through in v1)
//  4. Dispatch to next CLI
//  5. Repeat up to maxAttempts
//
// Fallback is goroutine-safe after construction.
type Fallback struct {
	classifier *FailureClassifier
	orderer    *Orderer
	translator Translator
	store      ScoreStore
	cfg        *FallbackConfig
	candidates []string // full ordered active CLI list
}

// NewFallback constructs a Fallback. All fields are required.
func NewFallback(
	classifier *FailureClassifier,
	orderer *Orderer,
	translator Translator,
	store ScoreStore,
	cfg *FallbackConfig,
	candidates []string,
) *Fallback {
	if classifier == nil || orderer == nil || translator == nil || store == nil || cfg == nil {
		panic("fallback: Fallback: all constructor arguments must be non-nil")
	}
	// Defensive copy of candidates.
	c := make([]string, len(candidates))
	copy(c, candidates)
	return &Fallback{
		classifier: classifier,
		orderer:    orderer,
		translator: translator,
		store:      store,
		cfg:        cfg,
		candidates: c,
	}
}

// Retry attempts to complete spec by trying fallback CLIs after the primary CLI failed.
//
// fctx carries the prior failure(s) already in the chain. dispatch is called with each
// candidate CLI; callers are responsible for wiring it to the appropriate worker.
//
// Returns (Result, nil) on success, or (zero, *ErrAllFallbackExhausted) when the attempt
// cap is reached with all candidates failing.
//
// If the initial error in fctx is Terminal, Retry returns it immediately without
// attempting any fallback (spec FR-2).
//
// NFR-3: no goroutines launched — all dispatch calls are synchronous within the
// caller's goroutine.
func (f *Fallback) Retry(ctx context.Context, spec picker.TaskSpec, fctx FailureCtx, dispatch DispatchFn) (Result, error) {
	// Build the attempted set from prior attempts so Orderer excludes them.
	attempted := make(map[string]struct{}, len(fctx.PriorAttempts))
	for _, a := range fctx.PriorAttempts {
		attempted[a.CLI] = struct{}{}
	}

	// Accumulate all failed attempts including those from before this call.
	allFailed := make([]FailedAttempt, len(fctx.PriorAttempts))
	copy(allFailed, fctx.PriorAttempts)

	// Check the initial error: if it is terminal, surface immediately.
	if fctx.LastError != nil {
		if f.classifier.Classify(fctx.LastError) == Terminal {
			return Result{}, fctx.LastError
		}
	}

	maxAttempts := f.cfg.maxAttempts()
	if fctx.MaxAttemptsOverride > 0 {
		maxAttempts = fctx.MaxAttemptsOverride
	}
	// Hard cap: do not exceed len(candidates) - 1 even if config says more.
	cap := len(f.candidates) - 1
	if maxAttempts > cap {
		maxAttempts = cap
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Re-rank after each prior attempt so the Orderer sees updated health state.
		ranked := f.orderer.Rank(ctx, f.candidates, spec.TaskClass, attempted, f.store)
		if len(ranked) == 0 {
			break // No healthy, un-attempted candidates remain.
		}

		nextCLI := ranked[0]
		adapted := f.translator.Adapt(spec, lastAttemptedCLI(allFailed), nextCLI)

		start := nowMS()
		content, dispatchErr := dispatch(ctx, nextCLI, adapted)
		elapsed := nowMS() - start

		if dispatchErr == nil {
			// Success — record latency and return.
			f.store.RecordSuccess(nextCLI, elapsed)
			return Result{
				Content:        content,
				SelectedCLI:    nextCLI,
				FailedAttempts: allFailed,
			}, nil
		}

		// Record failure.
		code := errorCode(dispatchErr)
		f.store.RecordFailure(nextCLI, code)

		allFailed = append(allFailed, FailedAttempt{
			CLI:     nextCLI,
			Code:    code.String(),
			Message: dispatchErr.Error(),
		})
		attempted[nextCLI] = struct{}{}

		// If this error is terminal, stop immediately — no further candidates will help.
		if f.classifier.Classify(dispatchErr) == Terminal {
			return Result{}, dispatchErr
		}
	}

	// Build skipped note: any healthy candidates that were not attempted.
	skippedNote := buildSkippedNote(f.candidates, attempted, f.orderer)

	return Result{}, &ErrAllFallbackExhausted{
		Attempts:    allFailed,
		SkippedNote: skippedNote,
	}
}

// --- helpers ---

// lastAttemptedCLI returns the most recently attempted CLI name, or "" if none.
func lastAttemptedCLI(attempts []FailedAttempt) string {
	if len(attempts) == 0 {
		return ""
	}
	return attempts[len(attempts)-1].CLI
}

// errorCode extracts the CLIErrorCode from err, defaulting to Unknown.
func errorCode(err error) types.CLIErrorCode {
	var cliErr *types.CLIError
	if errors.As(err, &cliErr) {
		return cliErr.Code
	}
	return types.CLIErrorCodeUnknown
}

// buildSkippedNote constructs a human-readable explanation of any CLIs that were
// excluded from the fallback chain (for ErrAllFallbackExhausted.SkippedNote).
func buildSkippedNote(candidates []string, attempted map[string]struct{}, orderer *Orderer) string {
	var skipped []string
	for _, cli := range candidates {
		if _, tried := attempted[cli]; tried {
			continue
		}
		if !orderer.health.IsHealthy(cli) {
			skipped = append(skipped, fmt.Sprintf("%s (health check failed)", cli))
		}
	}
	if len(skipped) == 0 {
		return ""
	}
	result := skipped[0]
	for _, s := range skipped[1:] {
		result += ", " + s
	}
	return result
}

// nowMS returns the current wall-clock time in milliseconds.
// Extracted for testability.
var nowMS = func() int64 {
	// We use a closure so tests can substitute a monotonic counter.
	return nowMSImpl()
}

func nowMSImpl() int64 {
	// time.Now().UnixMilli() requires Go 1.17+; use UnixNano/1e6 for compatibility.
	return timeNow().UnixNano() / 1e6
}
