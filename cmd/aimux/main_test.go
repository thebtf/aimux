package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetectMode exercises all 8 combinations of:
//   - daemon flag present/absent in args
//   - MCP_MUX_SESSION_ID set/unset
//   - AIMUX_NO_ENGINE set/unset
//
// Covers FR-1 (mode detection), NFR-4 (determinism), FR-4 (proxy rejection),
// FR-5 (AIMUX_NO_ENGINE deprecation), EC-3 (exact flag match).
func TestDetectMode(t *testing.T) {
	t.Parallel()

	const daemonFlag = "--muxcore-daemon"

	tests := []struct {
		name     string
		args     []string
		env      map[string]string
		wantMode Mode
		wantErr  bool
	}{
		// Row 1: daemon-flag present, no MCP_MUX_SESSION_ID, no AIMUX_NO_ENGINE → ModeDaemon
		{
			name:     "daemon_flag_no_session_no_no_engine",
			args:     []string{"aimux", daemonFlag},
			env:      map[string]string{},
			wantMode: ModeDaemon,
			wantErr:  false,
		},
		// Row 2: daemon-flag present, no MCP_MUX_SESSION_ID, AIMUX_NO_ENGINE=1 → ModeDaemon (deprecated env ignored)
		{
			name:     "daemon_flag_no_session_with_no_engine",
			args:     []string{"aimux", daemonFlag},
			env:      map[string]string{"AIMUX_NO_ENGINE": "1"},
			wantMode: ModeDaemon,
			wantErr:  false,
		},
		// Row 3: daemon-flag present, MCP_MUX_SESSION_ID set, no AIMUX_NO_ENGINE → error (FR-4)
		{
			name:    "daemon_flag_with_session_no_no_engine",
			args:    []string{"aimux", daemonFlag},
			env:     map[string]string{"MCP_MUX_SESSION_ID": "sess-x"},
			wantErr: true,
		},
		// Row 4: daemon-flag present, MCP_MUX_SESSION_ID set, AIMUX_NO_ENGINE=1 → error (FR-4, env order)
		{
			name:    "daemon_flag_with_session_with_no_engine",
			args:    []string{"aimux", daemonFlag},
			env:     map[string]string{"MCP_MUX_SESSION_ID": "sess-x", "AIMUX_NO_ENGINE": "1"},
			wantErr: true,
		},
		// Row 5: no daemon-flag, no MCP_MUX_SESSION_ID, no AIMUX_NO_ENGINE → ModeShim
		{
			name:     "no_flag_no_session_no_no_engine",
			args:     []string{"aimux"},
			env:      map[string]string{},
			wantMode: ModeShim,
			wantErr:  false,
		},
		// Row 6: no daemon-flag, no MCP_MUX_SESSION_ID, AIMUX_NO_ENGINE=1 → ModeShim (deprecated env ignored)
		{
			name:     "no_flag_no_session_with_no_engine",
			args:     []string{"aimux"},
			env:      map[string]string{"AIMUX_NO_ENGINE": "1"},
			wantMode: ModeShim,
			wantErr:  false,
		},
		// Row 7: no daemon-flag, MCP_MUX_SESSION_ID set, no AIMUX_NO_ENGINE → error (FR-4)
		{
			name:    "no_flag_with_session_no_no_engine",
			args:    []string{"aimux"},
			env:     map[string]string{"MCP_MUX_SESSION_ID": "sess-x"},
			wantErr: true,
		},
		// Row 8: no daemon-flag, MCP_MUX_SESSION_ID set, AIMUX_NO_ENGINE=1 → error (FR-4)
		{
			name:    "no_flag_with_session_with_no_engine",
			args:    []string{"aimux"},
			env:     map[string]string{"MCP_MUX_SESSION_ID": "sess-x", "AIMUX_NO_ENGINE": "1"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			envFn := func(k string) string { return tt.env[k] }

			got, err := detectMode(tt.args, envFn)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
					return
				}
				// FR-4: error message must contain "proxy mode" and "MCP_MUX_SESSION_ID".
				if !strings.Contains(err.Error(), "proxy mode") {
					t.Errorf("error %q does not contain \"proxy mode\"", err.Error())
				}
				if !strings.Contains(err.Error(), "MCP_MUX_SESSION_ID") {
					t.Errorf("error %q does not contain \"MCP_MUX_SESSION_ID\"", err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.wantMode {
				t.Errorf("mode = %d, want %d", got, tt.wantMode)
			}
		})
	}
}

// TestDetectMode_AllowLegacyProxyEscape verifies that AIMUX_ALLOW_LEGACY_PROXY=1
// suppresses the FR-4 rejection and returns ModeShim when MCP_MUX_SESSION_ID is set.
func TestDetectMode_AllowLegacyProxyEscape(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"MCP_MUX_SESSION_ID":       "sess-x",
		"AIMUX_ALLOW_LEGACY_PROXY": "1",
	}
	envFn := func(k string) string { return env[k] }

	got, err := detectMode([]string{"aimux"}, envFn)
	if err != nil {
		t.Errorf("unexpected error with AIMUX_ALLOW_LEGACY_PROXY=1: %v", err)
	}
	if got != ModeShim {
		t.Errorf("mode = %d, want ModeShim (%d)", got, ModeShim)
	}
}

// TestDetectMode_DaemonFlagExactMatch verifies EC-3: only the exact flag
// "--muxcore-daemon" triggers daemon mode; prefix matches like
// "--muxcore-daemon-debug" do NOT.
func TestDetectMode_DaemonFlagExactMatch(t *testing.T) {
	t.Parallel()

	envFn := func(string) string { return "" }

	// Prefix "--muxcore-daemon-debug" must NOT trigger daemon mode.
	gotPrefix, errPrefix := detectMode([]string{"aimux", "--muxcore-daemon-debug"}, envFn)
	if errPrefix != nil {
		t.Errorf("unexpected error for prefix flag: %v", errPrefix)
	}
	if gotPrefix != ModeShim {
		t.Errorf("prefix flag: mode = %d, want ModeShim (%d)", gotPrefix, ModeShim)
	}

	// Exact "--muxcore-daemon" MUST trigger daemon mode.
	gotExact, errExact := detectMode([]string{"aimux", "--muxcore-daemon"}, envFn)
	if errExact != nil {
		t.Errorf("unexpected error for exact flag: %v", errExact)
	}
	if gotExact != ModeDaemon {
		t.Errorf("exact flag: mode = %d, want ModeDaemon (%d)", gotExact, ModeDaemon)
	}
}

func TestParseHandoffFlags(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	if err := os.WriteFile(socketPath, []byte("socket"), 0o600); err != nil {
		t.Fatalf("write socket path fixture: %v", err)
	}

	token := strings.Repeat("ab", 32)
	got, err := parseHandoffFlags([]string{"--handoff-from", socketPath, "--handoff-token", token, "--muxcore-daemon"})
	if err != nil {
		t.Fatalf("parseHandoffFlags() unexpected error: %v", err)
	}
	if got.From != socketPath {
		t.Fatalf("From = %q, want %q", got.From, socketPath)
	}
	if got.Token != token {
		t.Fatalf("Token = %q, want %q", got.Token, token)
	}
}

func TestParseHandoffFlags_ValidationErrors(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	if err := os.WriteFile(socketPath, []byte("socket"), 0o600); err != nil {
		t.Fatalf("write socket path fixture: %v", err)
	}

	validToken := strings.Repeat("cd", 32)
	missingPath := filepath.Join(t.TempDir(), "missing.sock")

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "requires_both_flags_when_only_from_is_set",
			args:    []string{"--handoff-from", socketPath},
			wantErr: "--handoff-from and --handoff-token must both be set",
		},
		{
			name:    "requires_both_flags_when_only_token_is_set",
			args:    []string{"--handoff-token", validToken},
			wantErr: "--handoff-from and --handoff-token must both be set",
		},
		{
			name:    "rejects_short_token",
			args:    []string{"--handoff-from", socketPath, "--handoff-token", strings.Repeat("a", 63)},
			wantErr: "--handoff-token must be 64 hex characters",
		},
		{
			name:    "rejects_non_hex_token",
			args:    []string{"--handoff-from", socketPath, "--handoff-token", strings.Repeat("z1", 32)},
			wantErr: "--handoff-token must be 64 hex characters",
		},
		{
			name:    "rejects_missing_socket_path",
			args:    []string{"--handoff-from", missingPath, "--handoff-token", validToken},
			wantErr: "--handoff-from path must exist",
		},
		{
			name:    "rejects_directory_path",
			args:    []string{"--handoff-from", t.TempDir(), "--handoff-token", validToken},
			wantErr: "--handoff-from path must not be a directory",
		},
		{
			name:    "rejects_missing_token_value",
			args:    []string{"--handoff-from", socketPath, "--handoff-token"},
			wantErr: "parse handoff flags",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseHandoffFlags(tt.args)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateHandoffFlags_AcceptsUppercaseHex(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	if err := os.WriteFile(socketPath, []byte("socket"), 0o600); err != nil {
		t.Fatalf("write socket path fixture: %v", err)
	}

	tokenBytes := make([]byte, 32)
	for i := range tokenBytes {
		tokenBytes[i] = 0xAB
	}
	uppercaseToken := strings.ToUpper(hex.EncodeToString(tokenBytes))

	if err := validateHandoffFlags(handoffFlags{From: socketPath, Token: uppercaseToken}); err != nil {
		t.Fatalf("validateHandoffFlags() unexpected error for uppercase hex: %v", err)
	}
}
