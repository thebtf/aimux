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
// hadOutput reports whether the task has produced at least one live output line
// (issue #359 C2, artifact-aware window). When true, the startup grace no longer
// applies — the task is already streaming, so silence is judged against the
// stricter StreamingActiveSoftWarningSeconds instead of the startup
// StreamingSoftWarningSeconds. This catches a CLI that started responding and
// then wedged mid-stream faster than the startup budget, without falsely
// flagging a slow cold start. When hadOutput is false the original startup
// behavior is preserved byte-for-byte.
//
// Tier boundaries (defaults):
//
//	Startup (hadOutput=false):
//	  - < grace (60s)          → TierNone  (startup allowance; StreamingGraceSeconds)
//	  - ≥ grace, < soft (120s) → TierNone  (grace is a startup allowance, not a tier)
//	  - ≥ soft (120s)          → TierSoftWarning
//	Active (hadOutput=true):
//	  - < active-soft (60s)    → TierNone   (no startup grace; the task is streaming)
//	  - ≥ active-soft (60s)    → TierSoftWarning
//	Both:
//	  - ≥ hard (600s)          → TierHardStall
//	  - ≥ cancel (900s)        → TierAutoCancel
func evaluateInactivityTier(lastOutputAt time.Time, cfg *config.ServerConfig, hadOutput bool) InactivityTier {
	if lastOutputAt.IsZero() {
		return TierNone
	}

	silent := time.Since(lastOutputAt)

	softWarnDur := time.Duration(cfg.StreamingSoftWarningSeconds) * time.Second
	if hadOutput {
		// Artifact-aware: the startup grace is spent; a task that has already
		// streamed output is held to the stricter active soft-warning threshold.
		if active := time.Duration(cfg.StreamingActiveSoftWarningSeconds) * time.Second; active > 0 {
			softWarnDur = active
		}
	} else {
		graceDur := time.Duration(cfg.StreamingGraceSeconds) * time.Second
		if graceDur > 0 && silent < graceDur {
			return TierNone
		}
	}

	autoCancelDur := time.Duration(cfg.StreamingAutoCancelSeconds) * time.Second
	hardStallDur := time.Duration(cfg.StreamingHardStallSeconds) * time.Second

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
// running job. jobID is the job being polled — it is pre-filled into
// cancel_command so the caller can copy-paste the command without substitution.
// It is a no-op when the tier is TierNone.
func applyStallGuidance(result map[string]any, tier InactivityTier, jobID string) {
	switch tier {
	case TierSoftWarning:
		result["stall_warning"] = "No output for 120s. CLI may be waiting for input or stalled. " +
			`If still stalled at 600s, cancel with: sessions(action="cancel", job_id="` + jobID + `")`
	case TierHardStall:
		result["stall_alert"] = "No output for 600s. Consider cancelling."
		result["recommended_action"] = "cancel"
		result["cancel_command"] = `sessions(action="cancel", job_id="` + jobID + `")`
	case TierAutoCancel:
		result["stall_alert"] = "No output for 900s. Auto-cancel recommended."
		result["recommended_action"] = "cancel"
		result["auto_cancel_recommended"] = true
		result["cancel_command"] = `sessions(action="cancel", job_id="` + jobID + `")`
	}
}
