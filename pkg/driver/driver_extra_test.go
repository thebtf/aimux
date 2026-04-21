package driver_test

import (
	"runtime"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/types"
)

func TestProbe_FindsEcho(t *testing.T) {
	// echo/cmd.exe exists on all platforms — probe should find it
	binary := "echo"
	if runtime.GOOS == "windows" {
		binary = "cmd"
	}

	profiles := map[string]*config.CLIProfile{
		"test-cli": {Name: "test-cli", Binary: binary},
	}

	reg := driver.NewRegistry(profiles)
	reg.Probe()

	enabled := reg.EnabledCLIs()
	if len(enabled) != 1 {
		t.Errorf("expected 1 enabled CLI after probe, got %d", len(enabled))
	}
}

func TestProbe_MissingBinary(t *testing.T) {
	profiles := map[string]*config.CLIProfile{
		"fake": {Name: "fake", Binary: "nonexistent_binary_xyz_12345"},
	}

	reg := driver.NewRegistry(profiles)
	reg.Probe()

	enabled := reg.EnabledCLIs()
	if len(enabled) != 0 {
		t.Errorf("expected 0 enabled for missing binary, got %d", len(enabled))
	}
}

func TestProbe_MixedAvailability(t *testing.T) {
	binary := "echo"
	if runtime.GOOS == "windows" {
		binary = "cmd"
	}

	profiles := map[string]*config.CLIProfile{
		"real":  {Name: "real", Binary: binary},
		"fake":  {Name: "fake", Binary: "nonexistent_xyz"},
		"real2": {Name: "real2", Binary: binary},
	}

	reg := driver.NewRegistry(profiles)
	reg.Probe()

	enabled := reg.EnabledCLIs()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled (real + real2), got %d: %v", len(enabled), enabled)
	}
}

func TestResolveCommand_SessionResume_Codex(t *testing.T) {
	profile := &config.CLIProfile{
		Name:       "codex",
		Command:    config.CommandConfig{Base: "codex"},
		PromptFlag: "-p",
		Features:   types.CLIFeatures{SessionResume: true},
	}

	_, args := driver.ResolveCommand(profile, driver.TemplateParams{
		SessionResume: true,
		SessionID:     "sess-123",
	})

	found := map[string]bool{}
	for _, arg := range args {
		found[arg] = true
	}
	if !found["exec"] || !found["resume"] || !found["sess-123"] {
		t.Errorf("expected exec resume sess-123, got %v", args)
	}
}

func TestResolveCommand_SessionResume_Claude(t *testing.T) {
	profile := &config.CLIProfile{
		Name:       "claude",
		Command:    config.CommandConfig{Base: "claude"},
		PromptFlag: "-p",
		Features:   types.CLIFeatures{SessionResume: true},
	}

	_, args := driver.ResolveCommand(profile, driver.TemplateParams{
		SessionResume: true,
		SessionID:     "sess-456",
	})

	found := map[string]bool{}
	for _, arg := range args {
		found[arg] = true
	}
	if !found["--continue"] || !found["sess-456"] {
		t.Errorf("expected --continue sess-456, got %v", args)
	}
}

func TestResolveCommand_JSONOutputFormat(t *testing.T) {
	profile := &config.CLIProfile{
		Name:       "gemini",
		Command:    config.CommandConfig{Base: "gemini"},
		PromptFlag: "-p",
		Features:   types.CLIFeatures{JSON: true},
	}

	_, args := driver.ResolveCommand(profile, driver.TemplateParams{
		Prompt: "test",
		JSON:   true,
	})

	found := map[string]bool{}
	for _, arg := range args {
		found[arg] = true
	}
	if !found["--output-format"] || !found["json"] {
		t.Errorf("expected --output-format json, got %v", args)
	}
}

func TestResolveCommand_DefaultModel(t *testing.T) {
	profile := &config.CLIProfile{
		Name:         "codex",
		Command:      config.CommandConfig{Base: "codex"},
		ModelFlag:    "-m",
		DefaultModel: "gpt-5.4",
		PromptFlag:   "-p",
	}

	_, args := driver.ResolveCommand(profile, driver.TemplateParams{
		Prompt: "test",
		// Model is empty → should use DefaultModel
	})

	found := map[string]bool{}
	for _, arg := range args {
		found[arg] = true
	}
	if !found["-m"] || !found["gpt-5.4"] {
		t.Errorf("expected default model, got %v", args)
	}
}

func TestResolveCommand_ReasoningWithoutTemplate(t *testing.T) {
	profile := &config.CLIProfile{
		Name:       "test",
		Command:    config.CommandConfig{Base: "test"},
		PromptFlag: "-p",
		Reasoning: &config.ReasoningConfig{
			Flag:   "--effort",
			Levels: []string{"low", "high"},
			// No FlagValueTemplate — effort passed directly
		},
	}

	_, args := driver.ResolveCommand(profile, driver.TemplateParams{
		Prompt:          "test",
		ReasoningEffort: "high",
	})

	found := false
	for i, arg := range args {
		if arg == "--effort" && i+1 < len(args) && args[i+1] == "high" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --effort high, got %v", args)
	}
}

func TestResolveCommand_NoPrompt(t *testing.T) {
	profile := &config.CLIProfile{
		Name:       "test",
		Command:    config.CommandConfig{Base: "test"},
		PromptFlag: "-p",
	}

	_, args := driver.ResolveCommand(profile, driver.TemplateParams{
		// Empty prompt — no -p flag should be added
	})

	for _, arg := range args {
		if arg == "-p" {
			t.Error("should not add -p flag for empty prompt")
		}
	}
}

func TestShouldUseStdin_ZeroThreshold(t *testing.T) {
	profile := &config.CLIProfile{
		Name:           "test",
		StdinThreshold: 0, // disabled
	}

	if driver.ShouldUseStdin(profile, "any prompt") {
		t.Error("should not use stdin when threshold is 0")
	}
}
