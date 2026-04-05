package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
)

func TestLoadStubRules_BuiltIn(t *testing.T) {
	configDir := findConfigDir(t)

	cfg, err := config.LoadStubRules(configDir, "")
	if err != nil {
		t.Fatalf("LoadStubRules: %v", err)
	}

	if len(cfg.Rules) != 8 {
		t.Errorf("expected 8 rules, got %d", len(cfg.Rules))
	}

	// Verify known rules exist
	for _, id := range []string{"STUB-DISCARD", "STUB-HARDCODED", "STUB-PASSTHROUGH", "STUB-INTERFACE-EMPTY"} {
		rule := cfg.GetRule(id)
		if rule == nil {
			t.Errorf("missing rule %s", id)
		}
	}
}

func TestLoadStubRules_EnabledFilter(t *testing.T) {
	configDir := findConfigDir(t)

	cfg, err := config.LoadStubRules(configDir, "")
	if err != nil {
		t.Fatalf("LoadStubRules: %v", err)
	}

	enabled := cfg.EnabledRules()
	if len(enabled) != 8 {
		t.Errorf("expected 8 enabled rules, got %d", len(enabled))
	}
}

func TestLoadStubRules_ProjectOverride_DisableRule(t *testing.T) {
	configDir := findConfigDir(t)

	// Create project override that disables STUB-TODO
	projectDir := t.TempDir()
	overrideDir := filepath.Join(projectDir, ".aimux", "audit-rules.d")
	os.MkdirAll(overrideDir, 0o755)

	override := `rules:
  - id: STUB-TODO
    description: "Disabled for this project"
    severity: MEDIUM
    enabled: false
`
	os.WriteFile(filepath.Join(overrideDir, "stub-detection.yaml"), []byte(override), 0o644)

	cfg, err := config.LoadStubRules(configDir, projectDir)
	if err != nil {
		t.Fatalf("LoadStubRules with override: %v", err)
	}

	// STUB-TODO should be disabled
	rule := cfg.GetRule("STUB-TODO")
	if rule == nil {
		t.Fatal("STUB-TODO rule missing after override")
	}
	if rule.Enabled {
		t.Error("STUB-TODO should be disabled by project override")
	}

	// Other rules should still be enabled
	enabled := cfg.EnabledRules()
	if len(enabled) != 7 {
		t.Errorf("expected 7 enabled rules (1 disabled), got %d", len(enabled))
	}
}

func TestLoadStubRules_ProjectOverride_CustomPattern(t *testing.T) {
	configDir := findConfigDir(t)

	// Create project override with custom pattern
	projectDir := t.TempDir()
	overrideDir := filepath.Join(projectDir, ".aimux", "audit-rules.d")
	os.MkdirAll(overrideDir, 0o755)

	override := `rules:
  - id: STUB-CUSTOM
    description: "Custom project-specific stub pattern"
    severity: HIGH
    pattern: 'return ErrNotImplemented'
    enabled: true
additional_patterns:
  - 'return ErrNotSupported'
`
	os.WriteFile(filepath.Join(overrideDir, "stub-detection.yaml"), []byte(override), 0o644)

	cfg, err := config.LoadStubRules(configDir, projectDir)
	if err != nil {
		t.Fatalf("LoadStubRules with custom: %v", err)
	}

	// Custom rule should be appended
	rule := cfg.GetRule("STUB-CUSTOM")
	if rule == nil {
		t.Fatal("STUB-CUSTOM rule not found after override")
	}
	if rule.Severity != "HIGH" {
		t.Errorf("STUB-CUSTOM severity = %q, want HIGH", rule.Severity)
	}

	// Additional patterns should be merged
	if len(cfg.AdditionalPatterns) != 1 {
		t.Errorf("expected 1 additional pattern, got %d", len(cfg.AdditionalPatterns))
	}

	// Total: 8 built-in + 1 custom = 9
	if len(cfg.Rules) != 9 {
		t.Errorf("expected 9 rules (8 + 1 custom), got %d", len(cfg.Rules))
	}
}
