package fallback

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/aimux/pkg/executor/types"
)

// --- test helpers ---

func buildFallback(candidates []string) *Fallback {
	cfg := DefaultFallbackConfig()
	cfgp := &cfg
	store := NewInMemoryScoreStore()
	health := alwaysHealthyChecker(candidates)
	score := picker.NewCapabilityScore(&picker.PickerConfig{})
	orderer := NewOrderer(score, health, cfgp)
	classifier := NewFailureClassifier()
	translator := NewPassThroughTranslator()
	return NewFallback(classifier, orderer, translator, store, cfgp, candidates)
}

func rateLimitErr(msg string) *types.CLIError {
	return types.NewRateLimit(msg, nil)
}

func terminalErr(msg string) *types.CLIError {
	return types.NewUserInputError(msg, nil)
}

func testSpec() picker.TaskSpec {
	return picker.TaskSpec{TaskClass: "code", Prompt: "implement bubble sort"}
}

// --- success on N+1 attempt ---

func TestFallback_SuccessOnSecondAttempt(t *testing.T) {
	candidates := []string{"codex", "claude", "gemini"}
	fb := buildFallback(candidates)

	// Primary (codex) already failed — simulate via fctx
	callCount := 0
	dispatch := func(_ context.Context, cli string, _ picker.TaskSpec) (string, error) {
		callCount++
		if cli == "claude" {
			return "output from claude", nil
		}
		return "", rateLimitErr("rate limited")
	}

	fctx := FailureCtx{
		PriorAttempts: []FailedAttempt{{CLI: "codex", Code: "RateLimit", Message: "rate limited"}},
		LastError:     rateLimitErr("codex failed"),
	}

	result, err := fb.Retry(context.Background(), testSpec(), fctx, dispatch)
	if err != nil {
		t.Fatalf("Retry returned error: %v", err)
	}
	if result.SelectedCLI != "claude" {
		t.Errorf("SelectedCLI = %q, want claude", result.SelectedCLI)
	}
	if result.Content != "output from claude" {
		t.Errorf("Content = %q, want 'output from claude'", result.Content)
	}
	if len(result.FailedAttempts) < 1 {
		t.Errorf("FailedAttempts should include codex, got %v", result.FailedAttempts)
	}
}

// --- exhaustion path ---

func TestFallback_AllExhausted(t *testing.T) {
	candidates := []string{"codex", "claude", "gemini"}
	fb := buildFallback(candidates)

	dispatch := func(_ context.Context, cli string, _ picker.TaskSpec) (string, error) {
		return "", rateLimitErr(cli + " rate limited")
	}

	fctx := FailureCtx{
		PriorAttempts: []FailedAttempt{{CLI: "codex", Code: "RateLimit", Message: "rate limited"}},
		LastError:     rateLimitErr("codex failed"),
	}

	_, err := fb.Retry(context.Background(), testSpec(), fctx, dispatch)
	if err == nil {
		t.Fatal("expected ErrAllFallbackExhausted, got nil")
	}
	var exhausted *ErrAllFallbackExhausted
	if !errors.As(err, &exhausted) {
		t.Fatalf("expected *ErrAllFallbackExhausted, got %T: %v", err, err)
	}
	if len(exhausted.Attempts) == 0 {
		t.Errorf("ErrAllFallbackExhausted.Attempts is empty")
	}
	// codex was in PriorAttempts; Retry should have tried the remaining up to maxAttempts
	if !IsExhausted(err) {
		t.Errorf("IsExhausted() should return true for ErrAllFallbackExhausted")
	}
}

// --- terminal error stops immediately ---

func TestFallback_TerminalError_StopsImmediately(t *testing.T) {
	candidates := []string{"codex", "claude", "gemini"}
	fb := buildFallback(candidates)

	callCount := 0
	dispatch := func(_ context.Context, cli string, _ picker.TaskSpec) (string, error) {
		callCount++
		return "", terminalErr("invalid prompt syntax")
	}

	fctx := FailureCtx{
		PriorAttempts: []FailedAttempt{{CLI: "codex", Code: "UserInputError", Message: "invalid"}},
		LastError:     terminalErr("primary terminal"),
	}

	// If LastError is Terminal, Retry returns immediately without calling dispatch
	_, err := fb.Retry(context.Background(), testSpec(), fctx, dispatch)
	if err == nil {
		t.Fatal("expected error from terminal classification")
	}
	if callCount > 0 {
		t.Errorf("Terminal error from fctx.LastError: dispatch called %d times, want 0", callCount)
	}
}

func TestFallback_TerminalError_DuringRetry(t *testing.T) {
	candidates := []string{"codex", "claude", "gemini"}
	fb := buildFallback(candidates)

	callCount := 0
	dispatch := func(_ context.Context, cli string, _ picker.TaskSpec) (string, error) {
		callCount++
		// First fallback CLI returns a terminal error
		return "", terminalErr("input invalid")
	}

	fctx := FailureCtx{
		PriorAttempts: []FailedAttempt{{CLI: "codex", Code: "RateLimit", Message: "rate limit"}},
		LastError:     rateLimitErr("eligible — start retry"), // eligible triggers retry
	}

	_, err := fb.Retry(context.Background(), testSpec(), fctx, dispatch)
	if err == nil {
		t.Fatal("expected error")
	}
	// Terminal error from dispatch should stop immediately — only 1 dispatch call
	if callCount != 1 {
		t.Errorf("dispatch called %d times for terminal mid-retry, want 1", callCount)
	}
	// Should NOT be ErrAllFallbackExhausted — it's the raw terminal error
	var exhausted *ErrAllFallbackExhausted
	if errors.As(err, &exhausted) {
		t.Errorf("terminal error should surface raw, not wrapped as ErrAllFallbackExhausted")
	}
}

// --- context cancellation ---

func TestFallback_CanceledContext(t *testing.T) {
	candidates := []string{"codex", "claude", "gemini"}
	fb := buildFallback(candidates)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	dispatch := func(ctx context.Context, cli string, _ picker.TaskSpec) (string, error) {
		select {
		case <-ctx.Done():
			return "", types.NewCanceled("canceled", ctx.Err())
		default:
			return "", rateLimitErr("rate limited")
		}
	}

	fctx := FailureCtx{
		PriorAttempts: []FailedAttempt{{CLI: "codex", Code: "RateLimit", Message: "rl"}},
		LastError:     rateLimitErr("primary failed"),
	}

	_, err := fb.Retry(ctx, testSpec(), fctx, dispatch)
	// Either context error or terminal cancel error
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
}

func TestFallbackPicker_RunPrimaryStartsWithCallerCLI(t *testing.T) {
	fp := buildFallbackPickerForTest([]string{"codex", "claude", "gemini"}, nil)
	var calls []string
	dispatch := func(_ context.Context, cli string, _ picker.TaskSpec) (string, error) {
		calls = append(calls, cli)
		if cli == "claude" {
			return "output from claude", nil
		}
		return "", rateLimitErr(cli + " rate limited")
	}

	result, err := fp.RunPrimary(context.Background(), "codex", testSpec(), RunOptions{}, dispatch)
	if err != nil {
		t.Fatalf("RunPrimary returned error: %v", err)
	}
	if result.SelectedCLI != "claude" {
		t.Fatalf("SelectedCLI = %q, want claude", result.SelectedCLI)
	}
	if strings.Join(calls, ",") != "codex,claude" {
		t.Fatalf("dispatch calls = %#v, want codex then claude", calls)
	}
}

func TestFallbackPicker_RunPrimaryHonorsMaxAttemptsOverride(t *testing.T) {
	fp := buildFallbackPickerForTest([]string{"codex", "claude", "gemini"}, nil)
	var calls []string
	dispatch := func(_ context.Context, cli string, _ picker.TaskSpec) (string, error) {
		calls = append(calls, cli)
		return "", rateLimitErr(cli + " rate limited")
	}

	_, err := fp.RunPrimary(context.Background(), "codex", testSpec(), RunOptions{MaxAttempts: 1}, dispatch)
	if err == nil {
		t.Fatal("RunPrimary returned nil, want exhaustion")
	}
	if strings.Join(calls, ",") != "codex,claude" {
		t.Fatalf("dispatch calls = %#v, want primary plus one fallback", calls)
	}
}

func TestFallbackPicker_RunPrimaryRejectsUnknownPrimary(t *testing.T) {
	fp := buildFallbackPickerForTest([]string{"codex", "claude", "gemini"}, nil)
	dispatchCalled := false
	dispatch := func(context.Context, string, picker.TaskSpec) (string, error) {
		dispatchCalled = true
		return "", nil
	}

	_, err := fp.RunPrimary(context.Background(), "unknown-cli", testSpec(), RunOptions{}, dispatch)
	if err == nil {
		t.Fatal("RunPrimary returned nil, want unknown primary error")
	}
	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != types.CLIErrorCodeCapabilityMismatch {
		t.Fatalf("CLIError code = %s, want %s", cliErr.Code, types.CLIErrorCodeCapabilityMismatch)
	}
	if dispatchCalled {
		t.Fatal("dispatch was called for an unknown primary CLI")
	}
}

// --- candidates list ---

func TestFallback_NilCandidatesNotPanics(t *testing.T) {
	candidates := []string{"codex"} // single CLI
	fb := buildFallback(candidates)

	// With only 1 candidate already in PriorAttempts, no fallbacks possible
	fctx := FailureCtx{
		PriorAttempts: []FailedAttempt{{CLI: "codex", Code: "RateLimit", Message: "rl"}},
		LastError:     rateLimitErr("primary failed"),
	}
	_, err := fb.Retry(context.Background(), testSpec(), fctx, func(_ context.Context, _ string, _ picker.TaskSpec) (string, error) {
		return "ok", nil
	})
	// Should fail with exhausted (no candidates) not panic
	if err == nil {
		t.Fatal("expected error (no fallback candidates)")
	}
}

// --- errors.go ---

func TestErrAllFallbackExhausted_ErrorMessage(t *testing.T) {
	e := &ErrAllFallbackExhausted{
		Attempts: []FailedAttempt{
			{CLI: "codex", Code: "RateLimit", Message: "rate limit"},
			{CLI: "claude", Code: "Timeout", Message: "timed out"},
		},
	}
	msg := e.Error()
	if !strings.Contains(msg, "codex") {
		t.Errorf("error message missing codex: %q", msg)
	}
	if !strings.Contains(msg, "claude") {
		t.Errorf("error message missing claude: %q", msg)
	}
}

func TestIsExhausted_NilErr(t *testing.T) {
	if IsExhausted(nil) {
		t.Error("IsExhausted(nil) should be false")
	}
}

func TestIsExhausted_WrongType(t *testing.T) {
	if IsExhausted(errors.New("unrelated")) {
		t.Error("IsExhausted(unrelated) should be false")
	}
}

func buildFallbackPickerForTest(candidates []string, cfg *FallbackConfig) *FallbackPicker {
	if cfg == nil {
		defaultCfg := DefaultFallbackConfig()
		cfg = &defaultCfg
	}
	store := NewInMemoryScoreStore()
	health := alwaysHealthyChecker(candidates)
	score := picker.NewCapabilityScore(&picker.PickerConfig{})
	p := picker.NewPicker(&picker.PickerConfig{}, score, health, candidates)
	orderer := NewOrderer(score, health, cfg)
	classifier := NewFailureClassifier()
	translator := NewPassThroughTranslator()
	fb := NewFallback(classifier, orderer, translator, store, cfg, candidates)
	return NewFallbackPicker(p, fb, store, cfg)
}
