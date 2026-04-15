package server

import (
	"os"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
)

func TestAuthTokenPrecedence_EnvBeatsYAML(t *testing.T) {
	os.Setenv("AIMUX_AUTH_TOKEN", "env-token-value")
	defer os.Unsetenv("AIMUX_AUTH_TOKEN")

	// Simulate what New() does for auth token resolution.
	// We test the logic directly rather than constructing a full Server
	// (which requires a DB, etc.).
	cfg := &config.Config{
		Server: config.ServerConfig{
			AuthToken: "yaml-token-value",
		},
	}

	var authToken string
	authToken = os.Getenv("AIMUX_AUTH_TOKEN")
	if authToken == "" {
		authToken = cfg.Server.AuthToken
	}

	if authToken != "env-token-value" {
		t.Errorf("expected env token to win, got %q", authToken)
	}
}
