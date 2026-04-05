package driver_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
)

func TestHotReload_NewProfile(t *testing.T) {
	// Create a temp config directory with cli.d/
	dir := t.TempDir()
	cliDir := filepath.Join(dir, "cli.d", "test-cli")
	os.MkdirAll(cliDir, 0o755)

	// Write a profile
	profileContent := `
name: test-cli
binary: echo
display_name: "Test CLI"
features:
  streaming: false
  headless: true
output_format: text
command:
  base: echo
prompt_flag: ""
timeout_seconds: 60
`
	os.WriteFile(filepath.Join(cliDir, "profile.yaml"), []byte(profileContent), 0o644)

	// Load config (simulating reload)
	cfg, err := config.Load(dir)
	if err != nil {
		// Expected if default.yaml is missing — create minimal one
		os.WriteFile(filepath.Join(dir, "default.yaml"), []byte("server:\n  log_level: info\n"), 0o644)
		cfg, err = config.Load(dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
	}

	reg := driver.NewRegistry(cfg.CLIProfiles)

	profile, err := reg.Get("test-cli")
	if err != nil {
		t.Fatalf("Get test-cli: %v", err)
	}
	if profile.Binary != "echo" {
		t.Errorf("Binary = %q, want echo", profile.Binary)
	}
	if profile.TimeoutSeconds != 60 {
		t.Errorf("Timeout = %d, want 60", profile.TimeoutSeconds)
	}
}
