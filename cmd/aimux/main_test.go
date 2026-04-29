package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/logger"
	muxdaemon "github.com/thebtf/mcp-mux/muxcore/daemon"
	mcpsnapshot "github.com/thebtf/mcp-mux/muxcore/snapshot"
)

// TestDetectMode exercises the 4 real combinations of:
//   - daemon flag present/absent in args
//   - MCP_MUX_SESSION_ID set/unset
//
// Covers FR-1 (mode detection), NFR-4 (determinism), FR-4 (proxy rejection),
// and EC-3 (exact flag match).
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
		// Row 1: daemon-flag present, no MCP_MUX_SESSION_ID → ModeDaemon
		{
			name:     "daemon_flag_no_session",
			args:     []string{"aimux", daemonFlag},
			env:      map[string]string{},
			wantMode: ModeDaemon,
			wantErr:  false,
		},
		// Row 2: daemon-flag present, MCP_MUX_SESSION_ID set → error (FR-4)
		{
			name:    "daemon_flag_with_session",
			args:    []string{"aimux", daemonFlag},
			env:     map[string]string{"MCP_MUX_SESSION_ID": "sess-x"},
			wantErr: true,
		},
		// Row 3: no daemon-flag, no MCP_MUX_SESSION_ID → ModeShim
		{
			name:     "no_flag_no_session",
			args:     []string{"aimux"},
			env:      map[string]string{},
			wantMode: ModeShim,
			wantErr:  false,
		},
		// Row 4: no daemon-flag, MCP_MUX_SESSION_ID set → error (FR-4)
		{
			name:    "no_flag_with_session",
			args:    []string{"aimux"},
			env:     map[string]string{"MCP_MUX_SESSION_ID": "sess-x"},
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

	// Muxcore graceful-restart successor currently re-execs with "--daemon".
	gotLegacy, errLegacy := detectMode([]string{"aimux", "--daemon"}, envFn)
	if errLegacy != nil {
		t.Errorf("unexpected error for legacy daemon flag: %v", errLegacy)
	}
	if gotLegacy != ModeDaemon {
		t.Errorf("legacy daemon flag: mode = %d, want ModeDaemon (%d)", gotLegacy, ModeDaemon)
	}

}

// TestDetectMode_DirectUpstreamRejected verifies that AIMUX_DIRECT_UPSTREAM=1
// is rejected with an explicit error after ModeDirect was removed in v5.1.
// The error message must name the env var and state it is no longer supported.
func TestDetectMode_DirectUpstreamRejected(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"AIMUX_DIRECT_UPSTREAM": "1",
	}
	envFn := func(k string) string { return env[k] }

	_, err := detectMode([]string{"aimux"}, envFn)
	if err == nil {
		t.Fatal("expected error when AIMUX_DIRECT_UPSTREAM=1, got nil")
	}
	if !strings.Contains(err.Error(), "AIMUX_DIRECT_UPSTREAM") {
		t.Errorf("error %q does not contain \"AIMUX_DIRECT_UPSTREAM\"", err.Error())
	}
	if !strings.Contains(err.Error(), "no longer supported") {
		t.Errorf("error %q does not contain \"no longer supported\"", err.Error())
	}
}

// TestDetectMode_DirectUpstreamDaemonFlagWins verifies ADR-001 priority rule:
// when AIMUX_DIRECT_UPSTREAM=1 AND a daemon flag is present, daemon flag wins
// (the env var check is after the daemon flag check, so no error is returned).
func TestDetectMode_DirectUpstreamDaemonFlagWins(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"AIMUX_DIRECT_UPSTREAM": "1",
	}
	envFn := func(k string) string { return env[k] }

	got, err := detectMode([]string{"aimux", "--muxcore-daemon"}, envFn)
	if err != nil {
		t.Fatalf("unexpected error when daemon flag present: %v", err)
	}
	if got != ModeDaemon {
		t.Errorf("mode = %d, want ModeDaemon (%d)", got, ModeDaemon)
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

func TestReceivePredecessorHandoff_TokenMismatchReturnsExit2Error(t *testing.T) {
	t.Parallel()

	goodToken := strings.Repeat("ab", 32)
	badToken := strings.Repeat("cd", 32)

	listener, socketPath, err := listenPlatformHandoffRelay()
	if err != nil {
		t.Fatalf("listen predecessor socket: %v", err)
	}
	defer func() {
		_ = listener.Close()
		_ = removePlatformHandoffRelay(socketPath)
	}()

	done := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer conn.Close()
		_, performErr := muxdaemon.PerformHandoff(context.Background(), conn, goodToken, nil)
		done <- performErr
	}()

	_, err = bootstrapSuccessorHandoff(context.Background(), mustTestLogger(t), handoffFlags{From: socketPath, Token: badToken})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var exitErr *exitCodeError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exitCodeError, got %T (%v)", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("exit code = %d, want 2", exitErr.Code)
	}
	if !strings.Contains(exitErr.Error(), "handoff token mismatch") {
		t.Fatalf("error = %q, want token mismatch", exitErr.Error())
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("predecessor handoff goroutine did not finish")
	}
}

func TestBootstrapSuccessorHandoff_RelaysIntoMuxcoreRestorePath(t *testing.T) {
	t.Parallel()

	oldTokenPath := os.Getenv("MCPMUX_HANDOFF_TOKEN_PATH")
	oldSocketPath := os.Getenv("MCPMUX_HANDOFF_SOCKET")
	defer restoreEnvVar(t, "MCPMUX_HANDOFF_TOKEN_PATH", oldTokenPath)
	defer restoreEnvVar(t, "MCPMUX_HANDOFF_SOCKET", oldSocketPath)

	snapshotPath := mcpsnapshot.SnapshotPath("")
	_ = os.Remove(snapshotPath)
	defer os.Remove(snapshotPath)

	sid := "handoff-successor-bootstrap-test"
	snapshot := mcpsnapshot.DaemonSnapshot{
		Version:   mcpsnapshot.SnapshotVersion,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Owners: []mcpsnapshot.OwnerSnapshot{{
			ServerID:       sid,
			Command:        "echo",
			Args:           []string{"relay"},
			Cwd:            t.TempDir(),
			Mode:           "global",
			CachedInit:     "e30=",
			CachedTools:    "e30=",
		}},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, data, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinW.Close()
	defer stdinR.Close()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	defer stdoutR.Close()
	defer stdoutW.Close()

	predecessorListener, predecessorSocket, err := listenPlatformHandoffRelay()
	if err != nil {
		t.Fatalf("listen predecessor socket: %v", err)
	}
	defer func() {
		_ = predecessorListener.Close()
		_ = removePlatformHandoffRelay(predecessorSocket)
	}()

	token := strings.Repeat("ef", 32)
	senderDone := make(chan error, 1)
	go func() {
		conn, acceptErr := predecessorListener.Accept()
		if acceptErr != nil {
			senderDone <- acceptErr
			return
		}
		defer conn.Close()
		_, performErr := muxdaemon.PerformHandoff(context.Background(), conn, token, []muxdaemon.HandoffUpstream{{
			ServerID: sid,
			Command:  "echo",
			PID:      os.Getpid(),
			StdinFD:  stdinR.Fd(),
			StdoutFD: stdoutW.Fd(),
		}})
		senderDone <- performErr
	}()

	cleanup, err := bootstrapSuccessorHandoff(context.Background(), mustTestLogger(t), handoffFlags{From: predecessorSocket, Token: token})
	if err != nil {
		t.Fatalf("bootstrapSuccessorHandoff() error: %v", err)
	}
	defer cleanup()

	tokenPath := os.Getenv("MCPMUX_HANDOFF_TOKEN_PATH")
	if tokenPath == "" {
		t.Fatal("MCPMUX_HANDOFF_TOKEN_PATH not set")
	}
	relaySocket := os.Getenv("MCPMUX_HANDOFF_SOCKET")
	if relaySocket == "" {
		t.Fatal("MCPMUX_HANDOFF_SOCKET not set")
	}

	restoredToken, err := muxdaemon.ReadHandoffToken(tokenPath)
	if err != nil {
		t.Fatalf("ReadHandoffToken() error: %v", err)
	}
	if restoredToken != token {
		t.Fatalf("restored token = %q, want %q", restoredToken, token)
	}

	relayConn, err := dialPlatformHandoffConn(context.Background(), relaySocket)
	if err != nil {
		t.Fatalf("dial relay socket: %v", err)
	}
	defer relayConn.Close()

	received, err := muxdaemon.ReceiveHandoff(context.Background(), relayConn, token)
	if err != nil {
		t.Fatalf("ReceiveHandoff() via relay error: %v", err)
	}
	if len(received) != 1 {
		t.Fatalf("received %d upstreams, want 1", len(received))
	}
	if received[0].ServerID != sid {
		t.Fatalf("server id = %q, want %q", received[0].ServerID, sid)
	}
	if received[0].StdinFD == 0 || received[0].StdoutFD == 0 {
		t.Fatalf("received invalid FDs: stdin=%d stdout=%d", received[0].StdinFD, received[0].StdoutFD)
	}
	_ = os.NewFile(received[0].StdinFD, "").Close()
	_ = os.NewFile(received[0].StdoutFD, "").Close()

	select {
	case err := <-senderDone:
		if err != nil {
			t.Fatalf("predecessor PerformHandoff() error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("predecessor PerformHandoff() did not finish")
	}
}

func mustTestLogger(t *testing.T) *logger.Logger {
	t.Helper()
	l, err := logger.New("", logger.LevelDebug, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("logger.New(): %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func restoreEnvVar(t *testing.T, key, value string) {
	t.Helper()
	if value == "" {
		_ = os.Unsetenv(key)
		return
	}
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("restore %s: %v", key, err)
	}
}

func listenPlatformHandoffRelayAtPath(socketPath string) (net.Listener, error) {
	return listenPlatformHandoffRelayForTest(socketPath)
}
