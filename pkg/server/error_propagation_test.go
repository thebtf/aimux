package server_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

func TestTypedErrorPropagation(t *testing.T) {
	// Verify that executor errors propagate through the chain:
	// executor crash → TypedError → job.Error → MCP tool response

	// Create a timeout error with partial output
	err := types.NewTimeoutError("timed out after 300s", "partial audit findings here")
	if err.Type != types.ErrorTypeTimeout {
		t.Errorf("type = %q, want TimeoutError", err.Type)
	}
	if err.PartialOutput != "partial audit findings here" {
		t.Error("partial output not preserved")
	}

	// Verify errors.Is works through wrapping
	if !types.IsTypedError(err, types.ErrorTypeTimeout) {
		t.Error("IsTypedError should match")
	}
	if types.IsTypedError(err, types.ErrorTypeExecutor) {
		t.Error("IsTypedError should not match wrong type")
	}

	// Create executor error with cause chain
	cause := types.NewConfigError("bad config", nil)
	execErr := types.NewExecutorError("spawn failed", cause, "")
	if execErr.Cause == nil {
		t.Error("cause should be preserved")
	}

	// Verify circuit open error has CLI name
	circErr := types.NewCircuitOpenError("codex")
	if circErr.Type != types.ErrorTypeCircuitOpen {
		t.Errorf("type = %q, want CircuitOpenError", circErr.Type)
	}
}
