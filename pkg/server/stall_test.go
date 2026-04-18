package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/types"
)

func TestEvaluateInactivityTier_Boundaries(t *testing.T) {
	cfg := &config.ServerConfig{
		StreamingGraceSeconds:       60,
		StreamingSoftWarningSeconds: 120,
		StreamingHardStallSeconds:   600,
		StreamingAutoCancelSeconds:  900,
	}

	tests := []struct {
		name    string
		elapsed time.Duration
		want    InactivityTier
	}{
		{"within_grace_30s", 30 * time.Second, TierNone},
		{"at_grace_59s", 59 * time.Second, TierNone},
		{"past_grace_61s", 61 * time.Second, TierNone},
		{"at_soft_warning_121s", 121 * time.Second, TierSoftWarning},
		{"between_soft_and_hard_300s", 300 * time.Second, TierSoftWarning},
		{"past_hard_stall_601s", 601 * time.Second, TierHardStall},
		{"past_auto_cancel_901s", 901 * time.Second, TierAutoCancel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastOutput := time.Now().Add(-tt.elapsed)
			got := evaluateInactivityTier(lastOutput, cfg)
			if got != tt.want {
				t.Errorf("evaluateInactivityTier(-%v) = %d, want %d", tt.elapsed, got, tt.want)
			}
		})
	}
}

func TestEvaluateInactivityTier_ZeroValueIsNone(t *testing.T) {
	cfg := &config.ServerConfig{
		StreamingGraceSeconds:       60,
		StreamingSoftWarningSeconds: 120,
		StreamingHardStallSeconds:   600,
		StreamingAutoCancelSeconds:  900,
	}
	// Zero time = job hasn't produced output yet. Should not trigger stall.
	got := evaluateInactivityTier(time.Time{}, cfg)
	if got != TierNone {
		t.Errorf("evaluateInactivityTier(zero) = %d, want TierNone", got)
	}
}

func TestEvaluateInactivityTier_ConfigOverride(t *testing.T) {
	cfg := &config.ServerConfig{
		StreamingGraceSeconds:       10,
		StreamingSoftWarningSeconds: 20,
		StreamingHardStallSeconds:   30,
		StreamingAutoCancelSeconds:  40,
	}
	// 25s with 20s soft warning → should be SoftWarning
	lastOutput := time.Now().Add(-25 * time.Second)
	got := evaluateInactivityTier(lastOutput, cfg)
	if got != TierSoftWarning {
		t.Errorf("evaluateInactivityTier(-25s, custom config) = %d, want TierSoftWarning", got)
	}

	// 35s with 30s hard stall → should be HardStall
	lastOutput = time.Now().Add(-35 * time.Second)
	got = evaluateInactivityTier(lastOutput, cfg)
	if got != TierHardStall {
		t.Errorf("evaluateInactivityTier(-35s, custom config) = %d, want TierHardStall", got)
	}
}

func TestApplyStallGuidance_NoGuidanceForNone(t *testing.T) {
	result := map[string]any{"status": "running"}
	applyStallGuidance(result, TierNone, "job-test-1")
	if _, ok := result["stall_warning"]; ok {
		t.Error("TierNone should not add stall_warning")
	}
}

func TestApplyStallGuidance_AddsWarningForSoft(t *testing.T) {
	result := map[string]any{"status": "running"}
	applyStallGuidance(result, TierSoftWarning, "job-test-2")
	if _, ok := result["stall_warning"]; !ok {
		t.Error("TierSoftWarning should add stall_warning")
	}
}

func TestApplyStallGuidance_SoftWarningContainsCancelInstruction(t *testing.T) {
	result := map[string]any{"status": "running"}
	applyStallGuidance(result, TierSoftWarning, "job-abc-123")
	msg, ok := result["stall_warning"].(string)
	if !ok {
		t.Fatal("stall_warning should be a string")
	}
	wantCancel := `sessions(action="cancel", job_id="job-abc-123")`
	if !strings.Contains(msg, wantCancel) {
		t.Errorf("stall_warning should contain canonical cancel command %q, got: %s", wantCancel, msg)
	}
}

func TestApplyStallGuidance_RecommendsCancelForHard(t *testing.T) {
	result := map[string]any{"status": "running"}
	applyStallGuidance(result, TierHardStall, "job-test-3")
	if action, ok := result["recommended_action"]; !ok || action != "cancel" {
		t.Errorf("TierHardStall should recommend cancel, got %v", result)
	}
}

func TestApplyStallGuidance_HardStallCancelCommandPreFilled(t *testing.T) {
	result := map[string]any{"status": "running"}
	applyStallGuidance(result, TierHardStall, "job-hard-456")
	cmd, ok := result["cancel_command"].(string)
	if !ok {
		t.Fatal("TierHardStall should add cancel_command key")
	}
	want := `sessions(action="cancel", job_id="job-hard-456")`
	if cmd != want {
		t.Errorf("cancel_command = %q, want %q", cmd, want)
	}
}

func TestApplyStallGuidance_AutoCancelForMax(t *testing.T) {
	result := map[string]any{"status": "running"}
	applyStallGuidance(result, TierAutoCancel, "job-test-4")
	if v, ok := result["auto_cancel_recommended"]; !ok || v != true {
		t.Errorf("TierAutoCancel should set auto_cancel_recommended=true, got %v", result)
	}
}

func TestApplyStallGuidance_AutoCancelCancelCommandPreFilled(t *testing.T) {
	result := map[string]any{"status": "running"}
	applyStallGuidance(result, TierAutoCancel, "job-auto-789")
	cmd, ok := result["cancel_command"].(string)
	if !ok {
		t.Fatal("TierAutoCancel should add cancel_command key")
	}
	want := `sessions(action="cancel", job_id="job-auto-789")`
	if cmd != want {
		t.Errorf("cancel_command = %q, want %q", cmd, want)
	}
}

// --- handleStatus stall guidance integration tests ---

// testServerWithStallCfg returns a test server with canonical stall thresholds populated.
// testServer leaves all streaming threshold fields at zero, which collapses every tier
// to TierAutoCancel. Stall-specific tests must use this helper instead.
func testServerWithStallCfg(t *testing.T) *Server {
	t.Helper()
	srv := testServer(t)
	srv.cfg.Server.StreamingGraceSeconds = 60
	srv.cfg.Server.StreamingSoftWarningSeconds = 120
	srv.cfg.Server.StreamingHardStallSeconds = 600
	srv.cfg.Server.StreamingAutoCancelSeconds = 900
	return srv
}

// stallStatus creates a running job, injects lastOutputAt, and returns the
// parsed handleStatus response map.
func stallStatus(t *testing.T, srv *Server, lastOutputAt time.Time) map[string]any {
	t.Helper()

	sess := srv.sessions.Create("codex", types.SessionModeLive, t.TempDir())
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 1)

	// Inject the desired lastOutputAt directly into the live job to avoid sleeping.
	liveJob := srv.jobs.Get(job.ID)
	if liveJob == nil {
		t.Fatal("live job not found after create/start")
	}
	liveJob.LastOutputAt = lastOutputAt

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "status",
			Arguments: map[string]any{"job_id": job.ID},
		},
	}

	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatal("handleStatus returned nil or empty result")
	}

	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text.Text), &data); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	return data
}

func TestHandleStatus_RunningJob_WithinGrace_NoStallGuidance(t *testing.T) {
	srv := testServerWithStallCfg(t)
	data := stallStatus(t, srv, time.Now().Add(-30*time.Second))

	if _, ok := data["stall_warning"]; ok {
		t.Error("unexpected stall_warning within grace period")
	}
	if _, ok := data["stall_alert"]; ok {
		t.Error("unexpected stall_alert within grace period")
	}
	if _, ok := data["auto_cancel_recommended"]; ok {
		t.Error("unexpected auto_cancel_recommended within grace period")
	}
}

func TestHandleStatus_RunningJob_SoftWarning(t *testing.T) {
	srv := testServerWithStallCfg(t)
	// 130s ago — exceeds soft-warning threshold (120s default)
	data := stallStatus(t, srv, time.Now().Add(-130*time.Second))

	if data["stall_warning"] == nil {
		t.Error("expected stall_warning for job silent 130s")
	}
	if _, ok := data["stall_alert"]; ok {
		t.Error("stall_alert should not appear at soft-warning tier")
	}
	if _, ok := data["auto_cancel_recommended"]; ok {
		t.Error("auto_cancel_recommended should not appear at soft-warning tier")
	}
}

func TestHandleStatus_RunningJob_HardStall(t *testing.T) {
	srv := testServerWithStallCfg(t)
	// 650s ago — exceeds hard-stall threshold (600s default)
	data := stallStatus(t, srv, time.Now().Add(-650*time.Second))

	if data["stall_alert"] == nil {
		t.Error("expected stall_alert for job silent 650s")
	}
	if data["recommended_action"] != "cancel" {
		t.Errorf("recommended_action = %v, want cancel", data["recommended_action"])
	}
	if _, ok := data["auto_cancel_recommended"]; ok {
		t.Error("auto_cancel_recommended should not appear at hard-stall tier")
	}
	cmd, ok := data["cancel_command"].(string)
	if !ok {
		t.Fatal("TierHardStall integration: cancel_command should be present as a string")
	}
	if jobID, _ := data["job_id"].(string); jobID != "" {
		want := fmt.Sprintf(`sessions(action="cancel", job_id="%s")`, jobID)
		if cmd != want {
			t.Errorf("cancel_command = %q, want %q", cmd, want)
		}
	} else if !strings.Contains(cmd, `sessions(action="cancel", job_id="`) {
		t.Errorf("cancel_command %q does not match expected format", cmd)
	}
}

func TestHandleStatus_RunningJob_AutoCancel(t *testing.T) {
	srv := testServerWithStallCfg(t)
	// 950s ago — exceeds auto-cancel threshold (900s default)
	data := stallStatus(t, srv, time.Now().Add(-950*time.Second))

	if data["stall_alert"] == nil {
		t.Error("expected stall_alert for job silent 950s")
	}
	if data["recommended_action"] != "cancel" {
		t.Errorf("recommended_action = %v, want cancel", data["recommended_action"])
	}
	autoCancelRec, _ := data["auto_cancel_recommended"].(bool)
	if !autoCancelRec {
		t.Error("expected auto_cancel_recommended=true for job silent 950s")
	}
	cmd, ok := data["cancel_command"].(string)
	if !ok {
		t.Fatal("TierAutoCancel integration: cancel_command should be present as a string")
	}
	if jobID, _ := data["job_id"].(string); jobID != "" {
		want := fmt.Sprintf(`sessions(action="cancel", job_id="%s")`, jobID)
		if cmd != want {
			t.Errorf("cancel_command = %q, want %q", cmd, want)
		}
	} else if !strings.Contains(cmd, `sessions(action="cancel", job_id="`) {
		t.Errorf("cancel_command %q does not match expected format", cmd)
	}
}

func TestHandleStatus_CompletedJob_NoStallGuidance(t *testing.T) {
	srv := testServerWithStallCfg(t)

	sess := srv.sessions.Create("codex", types.SessionModeLive, t.TempDir())
	job := srv.jobs.Create(sess.ID, "codex")
	srv.jobs.StartJob(job.ID, 1)
	srv.jobs.CompleteJob(job.ID, "done", 0)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "status",
			Arguments: map[string]any{"job_id": job.ID},
		},
	}

	result, err := srv.handleStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}

	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text.Text), &data); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	if _, ok := data["stall_warning"]; ok {
		t.Error("completed job should not carry stall_warning")
	}
	if _, ok := data["stall_alert"]; ok {
		t.Error("completed job should not carry stall_alert")
	}
}
