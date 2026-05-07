package codex

import "fmt"

// SandboxConfig maps a task class to a codex sandbox policy.
// Implements the ForClass strategy table from FR-8 / ADR-006.
//
// Safe defaults: review and task classes use read-only sandbox with
// approvalPolicy:"never" so automated execution never pauses for human input.
// write-task enables writes but retains approvalPolicy:"never".
// danger requires explicit per-task user confirmation (not a config toggle).
type SandboxConfig struct {
	Mode           SandboxMode
	AskForApproval AskForApproval
}

// JobClass enumerates the valid task class values for AIMUX-18.
// These map 1:1 to sandbox policies per ADR-006.
const (
	JobClassReview    = "review"
	JobClassTask      = "task"
	JobClassWriteTask = "write-task"
	JobClassDanger    = "danger"
)

// ForClass returns the sandbox policy for the given job class.
//
// Table per FR-8:
//   review     → read-only, never
//   task       → read-only, never
//   write-task → workspace-write, never
//   danger     → danger-full-access, on-request
//
// Returns an error for unknown classes. The caller is responsible for rejecting
// "danger" class without explicit confirmation (danger_confirmed param).
func ForClass(jobClass string) (SandboxConfig, error) {
	switch jobClass {
	case JobClassReview:
		return SandboxConfig{
			Mode:           SandboxModeReadOnly,
			AskForApproval: AskForApprovalNever,
		}, nil
	case JobClassTask:
		return SandboxConfig{
			Mode:           SandboxModeReadOnly,
			AskForApproval: AskForApprovalNever,
		}, nil
	case JobClassWriteTask:
		return SandboxConfig{
			Mode:           SandboxModeWorkspaceWrite,
			AskForApproval: AskForApprovalNever,
		}, nil
	case JobClassDanger:
		return SandboxConfig{
			Mode:           SandboxModeDangerFullAccess,
			AskForApproval: AskForApprovalOnRequest,
		}, nil
	default:
		return SandboxConfig{}, fmt.Errorf("codex: unknown job class %q; valid: review, task, write-task, danger", jobClass)
	}
}
