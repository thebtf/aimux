package types_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

type fiveMethodExecutor struct{}

var _ types.ExecutorV2 = (*fiveMethodExecutor)(nil)

func (*fiveMethodExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{Name: "five-method-fake"}
}

func (*fiveMethodExecutor) Send(context.Context, types.Message) (*types.Response, error) {
	return &types.Response{}, nil
}

func (*fiveMethodExecutor) SendStream(_ context.Context, _ types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	onChunk(types.Chunk{Done: true})
	return &types.Response{}, nil
}

func (*fiveMethodExecutor) IsAlive() types.HealthStatus {
	return 0
}

func (*fiveMethodExecutor) Close() error {
	return nil
}

type optionalCapabilityExecutor struct {
	fiveMethodExecutor
}

var (
	_ types.EventExecutor           = (*optionalCapabilityExecutor)(nil)
	_ types.ExecutionCanceller      = (*optionalCapabilityExecutor)(nil)
	_ types.ProcessEvidenceProvider = (*optionalCapabilityExecutor)(nil)
	_ types.SessionIdentityProvider = (*optionalCapabilityExecutor)(nil)
)

func (*optionalCapabilityExecutor) SendEvents(_ context.Context, _ types.ExecutionID, _ types.Message, emit func(types.ExecutorEvent)) (*types.Response, error) {
	emit(types.ExecutorEvent{})
	return &types.Response{}, nil
}

func (*optionalCapabilityExecutor) CancelExecution(context.Context, types.ExecutionID, string) (types.CancellationEvidence, error) {
	return types.CancellationEvidence{}, nil
}

func (*optionalCapabilityExecutor) ProcessTreeEvidence(context.Context, types.ExecutionID) (types.ProcessTreeEvidence, error) {
	return types.ProcessTreeEvidence{}, nil
}

func (*optionalCapabilityExecutor) SessionIdentity() types.SessionIdentity {
	return types.SessionIdentity{}
}

func TestExecutorEventJSONRoundTripPreservesContractKeys(t *testing.T) {
	want := types.ExecutorEvent{
		Channel:   "stderr",
		Type:      "output",
		Terminal:  true,
		Truncated: true,
	}

	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}

	for key, value := range map[string]any{
		"channel":   "stderr",
		"type":      "output",
		"terminal":  true,
		"truncated": true,
	} {
		if got[key] != value {
			t.Fatalf("event JSON key %q = %#v, want %#v; payload=%s", key, got[key], value, payload)
		}
	}

	payload, err = json.Marshal(types.ExecutorEvent{
		Channel: "stdout",
		Type:    "output",
	})
	if err != nil {
		t.Fatalf("marshal false event flags: %v", err)
	}

	var falseFlags map[string]any
	if err := json.Unmarshal(payload, &falseFlags); err != nil {
		t.Fatalf("unmarshal false event flags: %v", err)
	}

	for _, key := range []string{"terminal", "truncated"} {
		value, ok := falseFlags[key]
		if !ok {
			t.Fatalf("event JSON omitted false key %q; payload=%s", key, payload)
		}
		if value != false {
			t.Fatalf("event JSON key %q = %#v, want false; payload=%s", key, value, payload)
		}
	}
}

func TestExecutionIdentitiesAcceptValidValues(t *testing.T) {
	tests := []struct {
		name     string
		validate func() error
	}{
		{name: "execution identity", validate: func() error { return types.ExecutionID("exec-17").Validate() }},
		{name: "process identity", validate: func() error {
			return (types.ProcessIdentity{PID: 4242, StartFingerprint: "start-17", TreeID: "tree-17"}).Validate()
		}},
		{name: "session identity", validate: func() error {
			return (types.SessionIdentity{Provider: "neutral", ID: "session-17", Generation: 1}).Validate()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.validate(); err != nil {
				t.Fatalf("valid identity rejected: %v", err)
			}
		})
	}
}

func TestExecutionIdentitiesRejectInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		validate func() error
	}{
		{name: "empty execution identity", validate: func() error { return types.ExecutionID("").Validate() }},
		{name: "empty process identity", validate: func() error { return (types.ProcessIdentity{}).Validate() }},
		{name: "zero session generation", validate: func() error {
			return (types.SessionIdentity{Provider: "neutral", ID: "session-17", Generation: 0}).Validate()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !types.IsTypedError(err, types.ErrorTypeValidation) {
				t.Fatalf("validation error must be typed: %T %v", err, err)
			}
		})
	}
}

func TestEvidenceWrappersRejectInvalidAffirmativeEvidence(t *testing.T) {
	tests := []struct {
		name     string
		validate func() error
	}{
		{
			name: "cancellation_acknowledged_without_execution_id",
			validate: func() error {
				return (types.CancellationEvidence{NativeAcknowledged: true}).Validate()
			},
		},
		{
			name: "process_stopped_without_identity",
			validate: func() error {
				return (types.ProcessTreeEvidence{Stopped: true}).Validate()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !types.IsTypedError(err, types.ErrorTypeValidation) {
				t.Fatalf("validation error must be typed: %T %v", err, err)
			}
		})
	}
}
