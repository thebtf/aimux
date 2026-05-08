package picker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/types"
)

func TestPickPair_DefaultCycle(t *testing.T) {
	cases := []struct {
		taskClass     string
		wantDriver    types.CLIName
		wantNavigator types.CLIName
	}{
		{taskClass: "code", wantDriver: "codex", wantNavigator: "claude"},
		{taskClass: "task", wantDriver: "claude", wantNavigator: "gemini"},
		{taskClass: "research", wantDriver: "gemini", wantNavigator: "codex"},
	}

	for _, tc := range cases {
		t.Run(tc.taskClass, func(t *testing.T) {
			p := makePairPicker(DefaultPickerConfig(), map[string]bool{"codex": true, "claude": true, "gemini": true}, activeCLIs)
			driver, navigator, err := p.PickPair(context.Background(), tc.taskClass)
			if err != nil {
				t.Fatalf("PickPair: %v", err)
			}
			if driver != tc.wantDriver || navigator != tc.wantNavigator {
				t.Fatalf("PickPair(%s) = (%s,%s), want (%s,%s)", tc.taskClass, driver, navigator, tc.wantDriver, tc.wantNavigator)
			}
		})
	}
}

func TestPickPair_FamilyMap(t *testing.T) {
	cases := map[types.CLIName]string{
		"codex":  familyOpenAI,
		"claude": familyAnthropic,
		"gemini": familyGoogle,
	}
	for cli, want := range cases {
		got, ok := FamilyOf(cli)
		if !ok {
			t.Fatalf("FamilyOf(%s) not found", cli)
		}
		if got != want {
			t.Fatalf("FamilyOf(%s) = %s, want %s", cli, got, want)
		}
	}
}

func TestPickPair_OverrideHonoured(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.PairNavigator = map[string]string{"codex": "gemini"}
	p := makePairPicker(cfg, map[string]bool{"codex": true, "claude": true, "gemini": true}, activeCLIs)

	driver, navigator, err := p.PickPair(context.Background(), "code")
	if err != nil {
		t.Fatalf("PickPair: %v", err)
	}
	if driver != "codex" || navigator != "gemini" {
		t.Fatalf("PickPair override = (%s,%s), want (codex,gemini)", driver, navigator)
	}
}

func TestPickPair_SingleFamilyOnly(t *testing.T) {
	p := makePairPicker(DefaultPickerConfig(), map[string]bool{"codex": true}, []string{"codex"})

	_, _, err := p.PickPair(context.Background(), "code")
	assertCLIError(t, err, types.CLIErrorCodeCapabilityMismatch, "cross-family pairing required, only one family available")
}

func TestPickPair_ZeroHealthy(t *testing.T) {
	p := makePairPicker(DefaultPickerConfig(), map[string]bool{}, activeCLIs)

	_, _, err := p.PickPair(context.Background(), "code")
	assertCLIError(t, err, types.CLIErrorCodeCapabilityMismatch, "no healthy CLI available")
}

func TestPickPair_OverrideSameFamilyError(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.PairNavigator = map[string]string{"codex": "codex"}
	p := makePairPicker(cfg, map[string]bool{"codex": true, "claude": true, "gemini": true}, activeCLIs)

	_, _, err := p.PickPair(context.Background(), "code")
	assertCLIError(t, err, types.CLIErrorCodeCapabilityMismatch, "cross-family pairing required")
}

func TestPickPair_UnhealthyOverrideFallsBack(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.PairNavigator = map[string]string{"codex": "gemini"}
	p := makePairPicker(cfg, map[string]bool{"codex": true, "claude": true}, activeCLIs)

	driver, navigator, err := p.PickPair(context.Background(), "code")
	if err != nil {
		t.Fatalf("PickPair: %v", err)
	}
	if driver != "codex" || navigator != "claude" {
		t.Fatalf("PickPair unhealthy override fallback = (%s,%s), want (codex,claude)", driver, navigator)
	}
}

func TestPickPair_RejectsUnknownDriverFamily(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.DefaultCLI = "local"
	p := makePairPicker(cfg, map[string]bool{"local": true, "codex": true, "claude": true}, []string{"local", "codex", "claude"})

	_, _, err := p.PickPair(context.Background(), "code")
	assertCLIError(t, err, types.CLIErrorCodeCapabilityMismatch, "known CLI family")
}

func TestPickPair_RejectsUnknownNavigatorOverride(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.PairNavigator = map[string]string{"codex": "local"}
	p := makePairPicker(cfg, map[string]bool{"codex": true, "local": true, "claude": true}, []string{"codex", "local", "claude"})

	_, _, err := p.PickPair(context.Background(), "code")
	assertCLIError(t, err, types.CLIErrorCodeCapabilityMismatch, "same-family or unknown-family")
}

func makePairPicker(cfg PickerConfig, healthy map[string]bool, active []string) *Picker {
	cfg.HealthCacheTTL = time.Hour
	cs := NewCapabilityScore(&cfg)
	hc := NewHealthChecker(&cfg, identityBinaryFor, active, stubLookPath(healthy))
	hc.WarmAll(context.Background())
	return NewPicker(&cfg, cs, hc, active)
}

func assertCLIError(t *testing.T, err error, wantCode types.CLIErrorCode, wantMessage string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected CLIError, got nil")
	}
	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("expected *types.CLIError, got %T: %v", err, err)
	}
	if cliErr.Code != wantCode {
		t.Fatalf("Code = %v, want %v", cliErr.Code, wantCode)
	}
	if !strings.Contains(cliErr.Message, wantMessage) {
		t.Fatalf("Message = %q, want containing %q", cliErr.Message, wantMessage)
	}
}
