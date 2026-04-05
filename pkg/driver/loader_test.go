package driver_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/types"
)

func TestRegistry_Get(t *testing.T) {
	profiles := map[string]*config.CLIProfile{
		"codex": {
			Name:           "codex",
			Binary:         "codex",
			DisplayName:    "Codex",
			TimeoutSeconds: 3600,
			Features:       types.CLIFeatures{Streaming: true, JSONL: true},
		},
		"gemini": {
			Name:           "gemini",
			Binary:         "gemini",
			DisplayName:    "Gemini",
			TimeoutSeconds: 600,
			Features:       types.CLIFeatures{Streaming: true, JSON: true},
		},
	}

	reg := driver.NewRegistry(profiles)

	p, err := reg.Get("codex")
	if err != nil {
		t.Fatalf("Get(codex): %v", err)
	}
	if p.Name != "codex" {
		t.Errorf("Name = %q, want codex", p.Name)
	}

	_, err = reg.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent CLI")
	}
}

func TestRegistry_EnabledCLIs(t *testing.T) {
	profiles := map[string]*config.CLIProfile{
		"codex":  {Name: "codex", Binary: "codex"},
		"gemini": {Name: "gemini", Binary: "gemini"},
	}

	reg := driver.NewRegistry(profiles)
	// Before probe, nothing is available
	enabled := reg.EnabledCLIs()
	if len(enabled) != 0 {
		t.Errorf("expected 0 enabled before probe, got %d", len(enabled))
	}
}

func TestResolveCommand_Basic(t *testing.T) {
	profile := &config.CLIProfile{
		Name:       "codex",
		Command:    config.CommandConfig{Base: "codex"},
		ModelFlag:  "-m",
		PromptFlag: "-p",
		Features:   types.CLIFeatures{Headless: true},
	}

	cmd, args := driver.ResolveCommand(profile, driver.TemplateParams{
		Prompt:   "hello world",
		Model:    "gpt-5.3-codex",
		Headless: true,
	})

	if cmd != "codex" {
		t.Errorf("command = %q, want codex", cmd)
	}

	// Should contain --full-auto, -m, model, -p, prompt
	found := map[string]bool{}
	for _, arg := range args {
		found[arg] = true
	}

	if !found["--full-auto"] {
		t.Error("missing --full-auto")
	}
	if !found["-m"] {
		t.Error("missing -m flag")
	}
	if !found["gpt-5.3-codex"] {
		t.Error("missing model value")
	}
	if !found["-p"] {
		t.Error("missing -p flag")
	}
}

func TestResolveCommand_ReadOnly(t *testing.T) {
	profile := &config.CLIProfile{
		Name:          "codex",
		Command:       config.CommandConfig{Base: "codex"},
		PromptFlag:    "-p",
		ReadOnlyFlags: []string{"--sandbox", "read-only"},
	}

	_, args := driver.ResolveCommand(profile, driver.TemplateParams{
		Prompt:   "test",
		ReadOnly: true,
	})

	foundSandbox := false
	for _, arg := range args {
		if arg == "--sandbox" {
			foundSandbox = true
		}
	}
	if !foundSandbox {
		t.Error("missing --sandbox flag for read-only mode")
	}
}

func TestResolveCommand_ReasoningEffort(t *testing.T) {
	profile := &config.CLIProfile{
		Name:       "codex",
		Command:    config.CommandConfig{Base: "codex"},
		PromptFlag: "-p",
		Reasoning: &config.ReasoningConfig{
			Flag:              "-c",
			FlagValueTemplate: `model_reasoning_effort="{{.Level}}"`,
			Levels:            []string{"low", "medium", "high", "xhigh"},
		},
	}

	_, args := driver.ResolveCommand(profile, driver.TemplateParams{
		Prompt:          "test",
		ReasoningEffort: "high",
	})

	foundFlag := false
	for i, arg := range args {
		if arg == "-c" && i+1 < len(args) && args[i+1] == `model_reasoning_effort="high"` {
			foundFlag = true
		}
	}
	if !foundFlag {
		t.Errorf("missing reasoning effort flag, got args: %v", args)
	}
}

func TestShouldUseStdin(t *testing.T) {
	profile := &config.CLIProfile{
		Name:           "codex",
		StdinThreshold: 6000,
	}

	if driver.ShouldUseStdin(profile, "short") {
		t.Error("short prompt should not use stdin")
	}

	longPrompt := make([]byte, 7000)
	for i := range longPrompt {
		longPrompt[i] = 'a'
	}
	if !driver.ShouldUseStdin(profile, string(longPrompt)) {
		t.Error("long prompt should use stdin")
	}
}

func TestValidateReasoningEffort(t *testing.T) {
	profile := &config.CLIProfile{
		Name: "codex",
		Reasoning: &config.ReasoningConfig{
			Levels: []string{"low", "medium", "high"},
		},
	}

	if err := driver.ValidateReasoningEffort(profile, "medium"); err != nil {
		t.Errorf("expected valid effort: %v", err)
	}

	if err := driver.ValidateReasoningEffort(profile, "xhigh"); err == nil {
		t.Error("expected error for invalid effort")
	}

	noReasoning := &config.CLIProfile{Name: "aider"}
	if err := driver.ValidateReasoningEffort(noReasoning, "high"); err == nil {
		t.Error("expected error for CLI without reasoning support")
	}
}
