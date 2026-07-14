package workerruntime

import "github.com/thebtf/aimux/pkg/types"

// ExecutionEnvelope composes provider-neutral execution activity and optional
// evidence while retaining one explicit execution correlation.
type ExecutionEnvelope struct {
	ExecutionID  types.ExecutionID          `json:"execution_id"`
	Event        types.ExecutorEvent        `json:"event"`
	Cancellation types.CancellationEvidence `json:"cancellation"`
	ProcessTree  types.ProcessTreeEvidence  `json:"process_tree"`
	Session      types.SessionIdentity      `json:"session"`
}

// Validate verifies required execution identity and any optional evidence that
// is present. Affirmative evidence without its identity is invalid.
func (envelope ExecutionEnvelope) Validate() error {
	if err := envelope.ExecutionID.Validate(); err != nil {
		return err
	}

	if envelope.Cancellation.ExecutionID != "" || envelope.Cancellation.NativeAcknowledged {
		if err := envelope.Cancellation.Validate(); err != nil {
			return err
		}
		if envelope.Cancellation.ExecutionID != envelope.ExecutionID {
			return types.NewValidationError("cancellation execution ID must match envelope execution ID")
		}
	}

	if envelope.ProcessTree.Process != (types.ProcessIdentity{}) || envelope.ProcessTree.OwnershipBoundary != "" || envelope.ProcessTree.Stopped {
		if err := envelope.ProcessTree.Validate(); err != nil {
			return err
		}
	}

	if envelope.Session != (types.SessionIdentity{}) {
		if err := envelope.Session.Validate(); err != nil {
			return err
		}
	}

	return nil
}
