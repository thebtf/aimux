package resolve_test

import (
	"os"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/resolve"
)

func makeProfile(passthrough []string) *config.CLIProfile {
	return &config.CLIProfile{
		Name:           "test-cli",
		EnvPassthrough: passthrough,
	}
}

func TestBuildEnv_FiltersOutsideAllowlist(t *testing.T) {
	// Set a secret-like env var that should NOT appear in output.
	os.Setenv("SHOULD_NOT_LEAK_XYZ", "secret")
	defer os.Unsetenv("SHOULD_NOT_LEAK_XYZ")

	profile := makeProfile(nil)
	env := resolve.BuildEnv(profile, nil)

	for _, kv := range env {
		if strings.HasPrefix(kv, "SHOULD_NOT_LEAK_XYZ=") {
			t.Errorf("BuildEnv leaked env var outside allowlist: %s", kv)
		}
	}
}

func TestBuildEnv_PreservesBaseline(t *testing.T) {
	profile := makeProfile(nil)
	env := resolve.BuildEnv(profile, nil)

	// PATH must always be present if set in the parent.
	parentPath := os.Getenv("PATH")
	if parentPath == "" {
		t.Skip("PATH not set in test environment")
	}

	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			found = true
			break
		}
	}
	if !found {
		t.Error("BuildEnv dropped PATH from baseline")
	}
}

func TestBuildEnv_AllowsProfileOverride(t *testing.T) {
	// Set an API key that is in the profile allowlist.
	os.Setenv("TEST_CLI_API_KEY", "myapikey123")
	defer os.Unsetenv("TEST_CLI_API_KEY")

	profile := makeProfile([]string{"TEST_CLI_API_KEY"})
	env := resolve.BuildEnv(profile, nil)

	found := false
	for _, kv := range env {
		if kv == "TEST_CLI_API_KEY=myapikey123" {
			found = true
			break
		}
	}
	if !found {
		t.Error("BuildEnv did not pass through profile-allowlisted key TEST_CLI_API_KEY")
	}
}

func TestBuildEnv_ExtraOverridesParent(t *testing.T) {
	os.Setenv("OVERRIDE_KEY", "original")
	defer os.Unsetenv("OVERRIDE_KEY")

	profile := makeProfile([]string{"OVERRIDE_KEY"})
	extra := map[string]string{"OVERRIDE_KEY": "injected"}
	env := resolve.BuildEnv(profile, extra)

	var found string
	for _, kv := range env {
		if strings.HasPrefix(kv, "OVERRIDE_KEY=") {
			found = kv
		}
	}
	if found != "OVERRIDE_KEY=injected" {
		t.Errorf("BuildEnv extra did not override parent: got %q", found)
	}
}
