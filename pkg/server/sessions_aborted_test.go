package server

// T012: sessions(action=list, status=aborted) filter test.
// Uses package server (whitebox) so it can call handleSessions directly.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/routing"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

// buildAbortedSessionsServer creates a minimal Server for sessions list testing.
func buildAbortedSessionsServer(t *testing.T) *Server {
	t.Helper()

	cfg := &config.Config{
		Server: config.ServerConfig{
			LogLevel:              "error",
			LogFile:               t.TempDir() + "/test.log",
			DefaultTimeoutSeconds: 10,
			Pair:                  config.PairConfig{MaxRounds: 2},
			Audit: config.AuditConfig{
				ScannerRole:      "codereview",
				ValidatorRole:    "analyze",
				ParallelScanners: 1,
			},
		},
		Roles: map[string]types.RolePreference{
			"default":    {CLI: "echo-cli"},
			"coding":     {CLI: "echo-cli"},
			"codereview": {CLI: "echo-cli"},
			"thinkdeep":  {CLI: "echo-cli"},
			"analyze":    {CLI: "echo-cli"},
		},
		CircuitBreaker: config.CircuitBreakerConfig{
			FailureThreshold: 3,
			CooldownSeconds:  5,
			HalfOpenMaxCalls: 1,
		},
		CLIProfiles: map[string]*config.CLIProfile{
			"echo-cli": {
				Name:           "echo-cli",
				Binary:         testBinary(),
				DisplayName:    "Echo CLI",
				Command:        config.CommandConfig{Base: testBinary()},
				PromptFlag:     "-p",
				TimeoutSeconds: 10,
				Features:       types.CLIFeatures{Headless: true},
			},
		},
		ConfigDir: t.TempDir(),
	}

	log, err := logger.New(cfg.Server.LogFile, logger.LevelError, logger.RotationOpts{})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	t.Cleanup(func() { log.Close() })

	reg := driver.NewRegistry(cfg.CLIProfiles)
	reg.SetAvailable("echo-cli", true)
	router := routing.NewRouter(cfg.Roles, []string{"echo-cli"})

	return New(cfg, log, reg, router)
}

// TestSessionsList_AbortedFilter seeds 5 aborted sessions and 3 active sessions,
// then verifies that sessions(action=list, status=aborted) returns exactly 5 rows.
func TestSessionsList_AbortedFilter(t *testing.T) {
	srv := buildAbortedSessionsServer(t)

	// Seed 5 aborted sessions.
	abortedIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		sess := srv.sessions.Create("echo-cli", types.SessionModeOnceStateless, "/tmp")
		srv.sessions.Update(sess.ID, func(s *session.Session) {
			s.Status = types.SessionStatusAborted
		})
		abortedIDs[i] = sess.ID
	}

	// Seed 3 active sessions (status remains "created").
	for i := 0; i < 3; i++ {
		srv.sessions.Create("echo-cli", types.SessionModeOnceStateless, "/tmp")
	}

	req := makeRequest("sessions", map[string]any{
		"action": "list",
		"status": "aborted",
	})

	result, err := srv.handleSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSessions: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Fatalf("handleSessions returned error result: %v", result.Content)
	}

	data := parseResult(t, result)

	// The dual-source list returns "sessions" array.
	sessionsRaw, ok := data["sessions"]
	if !ok {
		t.Fatalf("response missing 'sessions' field; keys: %v", mapKeys(data))
	}

	// Marshal/unmarshal to get the count.
	sessionsJSON, marshalErr := json.Marshal(sessionsRaw)
	if marshalErr != nil {
		t.Fatalf("marshal sessions: %v", marshalErr)
	}
	var sessions []map[string]any
	if unmarshalErr := json.Unmarshal(sessionsJSON, &sessions); unmarshalErr != nil {
		t.Fatalf("unmarshal sessions array: %v (raw: %s)", unmarshalErr, sessionsJSON)
	}

	if len(sessions) != 5 {
		t.Errorf("sessions count = %d, want 5 (status=aborted filter)", len(sessions))
	}

	// Verify each returned session has status=aborted.
	for i, s := range sessions {
		status, _ := s["status"].(string)
		if status != "aborted" {
			t.Errorf("sessions[%d].status = %q, want aborted", i, status)
		}
	}
}

// mapKeys returns the keys of a map for error diagnostics.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
