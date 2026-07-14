package types_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

func TestExecutorEventJSONPreservesContentBytes(t *testing.T) {
	cases := [][]byte{{0xce}, {0xb2}, {0xff}, {0x00, 0x1b}, {}}
	for _, content := range cases {
		want := types.ExecutorEvent{Channel: "stdout", Type: "output", Content: content}
		encoded, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var got types.ExecutorEvent
		if err := json.Unmarshal(encoded, &got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Content, want.Content) {
			t.Fatalf("content = %v, want %v", got.Content, want.Content)
		}
	}
	var old types.ExecutorEvent
	if err := json.Unmarshal([]byte(`{"channel":"stderr","type":"output","terminal":false,"truncated":false}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Content != nil {
		t.Fatalf("old JSON content = %v, want nil", old.Content)
	}
}

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

func (*optionalCapabilityExecutor) SendEvents(_ context.Context, _ types.ExecutionID, _ types.Message, sink types.ExecutorEventSink) (*types.Response, error) {
	sink.TryAdmit(types.ExecutorEvent{})
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

func TestProcessTreeEvidenceRequiresExplicitOwnershipBoundary(t *testing.T) {
	evidence := types.ProcessTreeEvidence{
		Process: types.ProcessIdentity{PID: 4242, StartFingerprint: "start-17", TreeID: "tree-17"},
		Stopped: true,
	}
	if err := evidence.Validate(); err == nil {
		t.Fatal("confirmed process evidence without an ownership boundary validated")
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["ownership_boundary"]; !ok {
		t.Fatalf("process evidence JSON omitted ownership_boundary: %s", payload)
	}
	for _, boundary := range []types.ProcessOwnershipBoundary{
		types.ProcessOwnershipBoundaryProcessGroup,
		types.ProcessOwnershipBoundaryJobObject,
	} {
		evidence.OwnershipBoundary = boundary
		if err := evidence.Validate(); err != nil {
			t.Fatalf("valid ownership boundary %q rejected: %v", boundary, err)
		}
	}
	evidence.OwnershipBoundary = "ancestry"
	if err := evidence.Validate(); err == nil {
		t.Fatal("unknown ownership boundary validated")
	}
}

func TestMessageSpawnArgsCarrierPreservesExactTypes(t *testing.T) {
	want := types.SpawnArgs{
		CLI:               "generic",
		Command:           "worker.exe",
		Args:              []string{"--mode", "framing", "--literal=01"},
		CWD:               `D:\\work tree`,
		Env:               map[string]string{"A": "01", "B": "x=y"},
		EnvList:           []string{"ONLY=exact", "EMPTY="},
		Stdin:             "stdin\x00payload",
		TimeoutSeconds:    17,
		InactivitySeconds: 9,
		CompletionPattern: `(?m)^done$`,
	}
	msg := types.Message{Content: "fallback", Spawn: &want}
	got := types.SpawnArgsFromMessage(msg)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("spawn args = %#v, want %#v", got, want)
	}

	got.Args[0] = "mutated"
	got.Env["A"] = "mutated"
	got.EnvList[0] = "mutated"
	if want.Args[0] != "--mode" || want.Env["A"] != "01" || want.EnvList[0] != "ONLY=exact" {
		t.Fatalf("typed carrier aliased caller-owned input: %#v", want)
	}
}
