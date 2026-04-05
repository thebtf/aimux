package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// StubRule defines a single stub detection rule from audit-rules.d/.
type StubRule struct {
	ID              string   `yaml:"id"`
	Description     string   `yaml:"description"`
	Severity        string   `yaml:"severity"`
	Pattern         string   `yaml:"pattern,omitempty"`
	Keywords        []string `yaml:"keywords,omitempty"`
	ExcludeFiles    []string `yaml:"exclude_files,omitempty"`
	ExcludePatterns []string `yaml:"exclude_patterns,omitempty"`
	IncludeFiles    []string `yaml:"include_files,omitempty"`
	Enabled         bool     `yaml:"enabled"`
	Example         string   `yaml:"example,omitempty"`
}

// StubDetectionConfig holds all stub detection rules.
type StubDetectionConfig struct {
	Rules              []StubRule `yaml:"rules"`
	AdditionalPatterns []string   `yaml:"additional_patterns,omitempty"`
}

// LoadStubRules loads stub detection rules from audit-rules.d/ directories.
// Built-in rules loaded first, then project overrides shadow them.
func LoadStubRules(configDir string, projectDir string) (*StubDetectionConfig, error) {
	// Load built-in rules
	builtinPath := filepath.Join(configDir, "audit-rules.d", "stub-detection.yaml")
	cfg, err := loadStubRulesFile(builtinPath)
	if err != nil {
		return nil, fmt.Errorf("load built-in stub rules: %w", err)
	}

	// Load project overrides (shadow built-in)
	if projectDir != "" {
		projectPath := filepath.Join(projectDir, ".aimux", "audit-rules.d", "stub-detection.yaml")
		projectCfg, projectErr := loadStubRulesFile(projectPath)
		if projectErr == nil {
			cfg = mergeStubRules(cfg, projectCfg)
		}
		// Missing project config is not an error — use built-in only
	}

	return cfg, nil
}

// EnabledRules returns only rules where Enabled=true.
func (c *StubDetectionConfig) EnabledRules() []StubRule {
	var result []StubRule
	for _, r := range c.Rules {
		if r.Enabled {
			result = append(result, r)
		}
	}
	return result
}

// GetRule returns a rule by ID, or nil if not found.
func (c *StubDetectionConfig) GetRule(id string) *StubRule {
	for i := range c.Rules {
		if c.Rules[i].ID == id {
			return &c.Rules[i]
		}
	}
	return nil
}

func loadStubRulesFile(path string) (*StubDetectionConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg StubDetectionConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &cfg, nil
}

// mergeStubRules merges project overrides into built-in config.
// Project rules with matching ID override the built-in rule.
// New project rules are appended.
func mergeStubRules(builtin, project *StubDetectionConfig) *StubDetectionConfig {
	ruleMap := make(map[string]int)
	for i, r := range builtin.Rules {
		ruleMap[r.ID] = i
	}

	for _, pr := range project.Rules {
		if idx, ok := ruleMap[pr.ID]; ok {
			builtin.Rules[idx] = pr // override
		} else {
			builtin.Rules = append(builtin.Rules, pr) // append new
		}
	}

	if len(project.AdditionalPatterns) > 0 {
		builtin.AdditionalPatterns = append(builtin.AdditionalPatterns, project.AdditionalPatterns...)
	}

	return builtin
}
