package server

import (
	"context"
	"encoding/json"
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
	applyStallGuidance(result, TierNone)
	if _, ok := result["stall_warning"]; ok {
		t.Error("TierNone should not add stall_warning")
	}
}

func TestApplyStallGuidance_AddsWarningForSoft(t *testing.T) {
	result := map[string]any{"status": "running"}
	applyStallGuidance(result, TierSoftWarning)
	if _, ok := result["stall_warning"]; !ok {
		t.Error("TierSoftWarning should add stall_warning")
	}
}

func TestApplyStallGuidance_RecommendsCancelForHard(t *testing.T) {
	result := map[string]any{"status": "running"}
	applyStallGuidance(result, TierHardStall)
	if action, ok := result["recommended_action"]; !ok || action != "cancel" {
		t.Errorf("TierHardStall should recommend cancel, got %v", result)
	}
}

func TestApplyStallGuidance_AutoCancelForMax(t *testing.T) {
	result := map[string]any{"status": "running"}
	applyStallGuidance(result, TierAutoCancel)
	if v, ok := result["auto_cancel_recommended"]; !ok || v != true {
		t.Errorf("TierAutoCancel should set auto_cancel_recommended=true, got %v", result)
	}
}
