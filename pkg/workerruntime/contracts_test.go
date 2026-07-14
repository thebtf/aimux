package workerruntime

import (
	"encoding/json"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

func TestNeutralExecutionEvidenceComposesAndRoundTrips(t *testing.T) {
	executionID := types.ExecutionID("exec-17")
	process := types.ProcessIdentity{
		PID:              4242,
		StartFingerprint: "start-17",
		TreeID:           "tree-17",
	}
	session := types.SessionIdentity{Provider: "neutral", ID: "session-17", Generation: 1}
	envelope := ExecutionEnvelope{
		ExecutionID: executionID,
		Event: types.ExecutorEvent{
			Channel:  "stdout",
			Type:     "output",
			Terminal: true,
		},
		Cancellation: types.CancellationEvidence{
			ExecutionID:        executionID,
			NativeAcknowledged: true,
		},
		ProcessTree: types.ProcessTreeEvidence{
			Process:           process,
			OwnershipBoundary: types.ProcessOwnershipBoundaryProcessGroup,
			Stopped:           true,
		},
		Session: session,
	}

	validations := []struct {
		name     string
		validate func() error
	}{
		{name: "envelope", validate: envelope.Validate},
		{name: "execution", validate: envelope.ExecutionID.Validate},
		{name: "process", validate: envelope.ProcessTree.Process.Validate},
		{name: "session", validate: envelope.Session.Validate},
		{name: "cancellation", validate: envelope.Cancellation.Validate},
		{name: "process tree", validate: envelope.ProcessTree.Validate},
	}
	for _, validation := range validations {
		if err := validation.validate(); err != nil {
			t.Fatalf("valid %s evidence rejected: %v", validation.name, err)
		}
	}

	mismatched := envelope
	mismatched.Cancellation.ExecutionID = types.ExecutionID("exec-other")
	err := mismatched.Validate()
	if err == nil {
		t.Fatal("expected mismatched execution correlation to fail validation")
	}
	if !types.IsTypedError(err, types.ErrorTypeValidation) {
		t.Fatalf("correlation error must be typed: %T %v", err, err)
	}

	unconfirmed := envelope
	unconfirmed.Cancellation.NativeAcknowledged = false
	unconfirmed.ProcessTree.Stopped = false
	if err := unconfirmed.Validate(); err != nil {
		t.Fatalf("valid unconfirmed evidence rejected: %v", err)
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal neutral evidence: %v", err)
	}

	var got ExecutionEnvelope
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal neutral evidence: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("round-tripped neutral evidence rejected: %v", err)
	}

	if got.ExecutionID != executionID || got.Cancellation.ExecutionID != executionID {
		t.Fatalf("execution correlation lost: got %#v", got)
	}
	if !got.Cancellation.NativeAcknowledged {
		t.Fatal("cancellation acknowledgement lost")
	}
	if got.ProcessTree.Process != process || !got.ProcessTree.Stopped {
		t.Fatalf("process-tree stop evidence lost: got %#v", got.ProcessTree)
	}
	if got.Session != session {
		t.Fatalf("session identity lost: got %#v, want %#v", got.Session, session)
	}
	if !got.Event.Terminal {
		t.Fatal("terminal event fact lost")
	}
}

func TestExecutionEnvelopeAllowsAbsentOptionalEvidence(t *testing.T) {
	envelope := ExecutionEnvelope{
		ExecutionID: types.ExecutionID("exec-sparse"),
		Event: types.ExecutorEvent{
			Channel: "stdout",
			Type:    "output",
		},
	}

	if err := envelope.Validate(); err != nil {
		t.Fatalf("sparse envelope rejected: %v", err)
	}
}

func TestExecutionEnvelopeRejectsPartialOptionalEvidence(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*ExecutionEnvelope)
	}{
		{
			name: "cancellation_acknowledged_without_execution_id",
			apply: func(envelope *ExecutionEnvelope) {
				envelope.Cancellation.NativeAcknowledged = true
			},
		},
		{
			name: "cancellation_id_mismatches_envelope_without_acknowledgement",
			apply: func(envelope *ExecutionEnvelope) {
				envelope.Cancellation.ExecutionID = types.ExecutionID("exec-other")
			},
		},
		{
			name: "process_stopped_without_identity",
			apply: func(envelope *ExecutionEnvelope) {
				envelope.ProcessTree.Stopped = true
			},
		},
		{
			name: "process_partial_identity_without_stop_confirmation",
			apply: func(envelope *ExecutionEnvelope) {
				envelope.ProcessTree.Process = types.ProcessIdentity{
					PID:              4242,
					StartFingerprint: "start-17",
				}
			},
		},
		{
			name: "process_boundary_without_identity",
			apply: func(envelope *ExecutionEnvelope) {
				envelope.ProcessTree.OwnershipBoundary = types.ProcessOwnershipBoundaryProcessGroup
			},
		},
		{
			name: "unknown_process_boundary_without_identity",
			apply: func(envelope *ExecutionEnvelope) {
				envelope.ProcessTree.OwnershipBoundary = types.ProcessOwnershipBoundary("unknown")
			},
		},
		{
			name: "session_zero_generation",
			apply: func(envelope *ExecutionEnvelope) {
				envelope.Session = types.SessionIdentity{
					Provider: "neutral",
					ID:       "session-17",
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope := ExecutionEnvelope{
				ExecutionID: types.ExecutionID("exec-partial"),
			}
			tt.apply(&envelope)

			err := envelope.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !types.IsTypedError(err, types.ErrorTypeValidation) {
				t.Fatalf("validation error must be typed: %T %v", err, err)
			}
		})
	}
}
