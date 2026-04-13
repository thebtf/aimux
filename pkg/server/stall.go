package server

import (
	"time"

	"github.com/thebtf/aimux/pkg/config"
)

// InactivityTier classifies how long a running job has been silent.
type InactivityTier int

const (
	// TierNone means the job is within the grace period — no guidance needed.
	TierNone InactivityTier = iota
	// TierSoftWarning means output has been absent for ≥ soft-warning threshold.
	TierSoftWarning
	// TierHardStall means output has been absent for ≥ hard-stall threshold.
	TierHardStall
	// TierAutoCancel means output has been absent for ≥ auto-cancel threshold.
	TierAutoCancel
)

// evaluateInactivityTier returns the tier for a running job based on how long
// it has been since its last output. lastOutputAt being zero means the job has
// never produced output; in that case we measure from the job's creation time,
// which the caller must substitute before calling this function (or pass a
// known-good baseline). The function itself only needs a timestamp and config.
//
// Tier boundaries (defaults):
//   - < grace (60s)   → TierNone  (startup allowance; StreamingGraceSeconds)
//   - ≥ grace, < soft (120s) → TierNone  (grace is a startup allowance, not a tier)
//   - ≥ soft (120s)   → TierSoftWarning
//   - ≥ hard (600s)   → TierHardStall
//   - ≥ cancel (900s) → TierAutoCancel
func evaluateInactivityTier(lastOutputAt time.Time, cfg *config.ServerConfig) InactivityTier {
	if lastOutputAt.IsZero() {
		return TierNone
	}

	silent := time.Since(lastOutputAt)

	graceDur := time.Duration(cfg.StreamingGraceSeconds) * time.Second
	if graceDur > 0 && silent < graceDur {
		return TierNone
	}

	autoCancelDur := time.Duration(cfg.StreamingAutoCancelSeconds) * time.Second
	hardStallDur := time.Duration(cfg.StreamingHardStallSeconds) * time.Second
	softWarnDur := time.Duration(cfg.StreamingSoftWarningSeconds) * time.Second

	switch {
	case silent >= autoCancelDur:
		return TierAutoCancel
	case silent >= hardStallDur:
		return TierHardStall
	case silent >= softWarnDur:
		return TierSoftWarning
	default:
		return TierNone
	}
}

// applyStallGuidance adds stall-related keys to a status result map for a
// running job. It is a no-op when the tier is TierNone.
func applyStallGuidance(result map[string]any, tier InactivityTier) {
	switch tier {
	case TierSoftWarning:
		result["stall_warning"] = "No output for 120s. Job may be stalled."
	case TierHardStall:
		result["stall_alert"] = "No output for 600s. Consider cancelling."
		result["recommended_action"] = "cancel"
	case TierAutoCancel:
		result["stall_alert"] = "No output for 900s. Auto-cancel recommended."
		result["recommended_action"] = "cancel"
		result["auto_cancel_recommended"] = true
	}
}
