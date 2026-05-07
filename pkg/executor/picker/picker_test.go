package picker

import (
	"context"
	"errors"
	"testing"
	"time"
)

// activeCLIs is the standard ordered active CLI set for tests.
// Order matters for tie-break: codex wins ties.
var activeCLIs = []string{"codex", "claude", "gemini"}

// makeHealthChecker builds a HealthChecker with the given CLIs marked as healthy/unhealthy.
func makeHealthChecker(healthyCLIs map[string]bool) *HealthChecker {
	cfg := DefaultPickerConfig()
	cfg.HealthCacheTTL = 1 * time.Hour // long TTL: no re-probe during tests

	lp := stubLookPath(healthyCLIs)
	hc := NewHealthChecker(&cfg, identityBinaryFor, activeCLIs, lp)
	// Pre-warm the cache so IsHealthy never calls LookPath during Pick.
	hc.WarmAll(context.Background())
	return hc
}

// allHealthy returns a HealthChecker where all standard CLIs are healthy.
func allHealthy() *HealthChecker {
	return makeHealthChecker(map[string]bool{"codex": true, "claude": true, "gemini": true})
}

// noneHealthy returns a HealthChecker where all standard CLIs are absent.
func noneHealthy() *HealthChecker {
	return makeHealthChecker(map[string]bool{})
}

func TestPicker_ByTaskClass_Code(t *testing.T) {
	cfg := DefaultPickerConfig()
	cs := NewCapabilityScore(&cfg)
	hc := allHealthy()
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	got, err := p.Pick(context.Background(), TaskSpec{TaskClass: "code"})
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if got != "codex" {
		t.Errorf("Pick(code) = %q, want codex (score 95 > claude 80 > gemini 60)", got)
	}
}

func TestPicker_ByTaskClass_Review(t *testing.T) {
	cfg := DefaultPickerConfig()
	cs := NewCapabilityScore(&cfg)
	hc := allHealthy()
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	got, err := p.Pick(context.Background(), TaskSpec{TaskClass: "review"})
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if got != "codex" {
		t.Errorf("Pick(review) = %q, want codex (score 90 > claude 70 > gemini 50)", got)
	}
}

func TestPicker_ByTaskClass_Research(t *testing.T) {
	cfg := DefaultPickerConfig()
	cs := NewCapabilityScore(&cfg)
	hc := allHealthy()
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	got, err := p.Pick(context.Background(), TaskSpec{TaskClass: "research"})
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if got != "gemini" {
		t.Errorf("Pick(research) = %q, want gemini (score 90 > claude 80 > codex 40)", got)
	}
}

func TestPicker_ByTaskClass_Task(t *testing.T) {
	cfg := DefaultPickerConfig()
	cs := NewCapabilityScore(&cfg)
	hc := allHealthy()
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	got, err := p.Pick(context.Background(), TaskSpec{TaskClass: "task"})
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if got != "claude" {
		t.Errorf("Pick(task) = %q, want claude (score 85 > codex 80 > gemini 60)", got)
	}
}

func TestPicker_ConfigOverride_DefaultCLI(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.DefaultCLI = "claude"
	cs := NewCapabilityScore(&cfg)
	hc := allHealthy()
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	// default_cli=claude should win regardless of task class score.
	got, err := p.Pick(context.Background(), TaskSpec{TaskClass: "code"})
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if got != "claude" {
		t.Errorf("Pick with default_cli=claude = %q, want claude", got)
	}
}

func TestPicker_ConfigOverride_PreferCLI(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.PreferCLI = map[string]string{"research": "claude"}
	cs := NewCapabilityScore(&cfg)
	hc := allHealthy()
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	// prefer_cli["research"]=claude should override gemini's higher score.
	got, err := p.Pick(context.Background(), TaskSpec{TaskClass: "research"})
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if got != "claude" {
		t.Errorf("Pick(research) with prefer_cli[research]=claude = %q, want claude", got)
	}
}

func TestPicker_ConfigOverrideUnhealthy_FallsThrough(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.DefaultCLI = "codex"
	cs := NewCapabilityScore(&cfg)
	// codex is absent (unhealthy), claude and gemini are present.
	hc := makeHealthChecker(map[string]bool{"claude": true, "gemini": true})
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	// default_cli=codex but codex unhealthy → fall through to score (claude wins "code" after codex absent).
	got, err := p.Pick(context.Background(), TaskSpec{TaskClass: "code"})
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if got != "claude" {
		t.Errorf("Pick(code) with codex unhealthy = %q, want claude (highest score among healthy)", got)
	}
}

func TestPicker_AllUnhealthy_ErrNoHealthyCLI(t *testing.T) {
	cfg := DefaultPickerConfig()
	cs := NewCapabilityScore(&cfg)
	hc := noneHealthy()
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	_, err := p.Pick(context.Background(), TaskSpec{TaskClass: "code"})
	if err == nil {
		t.Fatal("expected ErrNoHealthyCLI, got nil")
	}

	var noHealth *ErrNoHealthyCLI
	if !errors.As(err, &noHealth) {
		t.Fatalf("expected *ErrNoHealthyCLI, got %T: %v", err, err)
	}
	if len(noHealth.Reasons) == 0 {
		t.Error("expected at least one failure reason in ErrNoHealthyCLI")
	}
}

func TestPicker_DisabledCLIs_ErrNoHealthyCLI(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.DisabledCLIs = []string{"codex", "claude", "gemini"}
	cs := NewCapabilityScore(&cfg)
	hc := allHealthy()
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	_, err := p.Pick(context.Background(), TaskSpec{TaskClass: "code"})
	if err == nil {
		t.Fatal("expected ErrNoHealthyCLI when all CLIs disabled")
	}
	var noHealth *ErrNoHealthyCLI
	if !errors.As(err, &noHealth) {
		t.Fatalf("expected *ErrNoHealthyCLI, got %T", err)
	}
}

func TestPicker_TieBreak_PrefersCodex(t *testing.T) {
	// Set equal scores for all CLIs on "equal-task".
	cfg := DefaultPickerConfig()
	cfg.Scores = map[string]map[string]int{
		"codex":  {"equal-task": 75},
		"claude": {"equal-task": 75},
		"gemini": {"equal-task": 75},
	}
	cs := NewCapabilityScore(&cfg)
	hc := allHealthy()
	// activeCLIs order: codex first → codex wins ties.
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	got, err := p.Pick(context.Background(), TaskSpec{TaskClass: "equal-task"})
	if err != nil {
		t.Fatalf("Pick returned error: %v", err)
	}
	if got != "codex" {
		t.Errorf("tie-break: got %q, want codex (first in activeCLIs)", got)
	}
}

func TestPicker_DisabledCLI_NotSelectedEvenIfOverridden(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.DefaultCLI = "codex"
	cfg.DisabledCLIs = []string{"codex"} // codex disabled overrides default_cli
	cs := NewCapabilityScore(&cfg)
	hc := allHealthy()
	p := NewPicker(&cfg, cs, hc, activeCLIs)

	// default_cli=codex but codex disabled; should fall through to scored (claude wins "task").
	got, err := p.Pick(context.Background(), TaskSpec{TaskClass: "task"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "codex" {
		t.Error("disabled CLI codex was selected despite being in DisabledCLIs")
	}
}

func BenchmarkPicker_WarmCache(b *testing.B) {
	cfg := DefaultPickerConfig()
	cfg.HealthCacheTTL = 1 * time.Hour
	cs := NewCapabilityScore(&cfg)
	hc := makeHealthChecker(map[string]bool{"codex": true, "claude": true, "gemini": true})
	p := NewPicker(&cfg, cs, hc, activeCLIs)
	ctx := context.Background()
	spec := TaskSpec{TaskClass: "code"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = p.Pick(ctx, spec)
	}
}
