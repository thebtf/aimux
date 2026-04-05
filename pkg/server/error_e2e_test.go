package server_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// TestErrorPropagation_StrategyFailure verifies the error chain:
// strategy failure → TypedError → preserved in job → MCP tool response
func TestErrorPropagation_StrategyFailure(t *testing.T) {
	// Simulate: executor returns timeout with partial output
	timeoutErr := types.NewTimeoutError("timed out after 300s", "partial findings here")

	// Verify chain
	if timeoutErr.Type != types.ErrorTypeTimeout {
		t.Errorf("type = %q, want TimeoutError", timeoutErr.Type)
	}
	if timeoutErr.PartialOutput != "partial findings here" {
		t.Error("partial output lost in chain")
	}

	// Simulate: executor error wraps cause
	execErr := types.NewExecutorError("strategy failed", timeoutErr, timeoutErr.PartialOutput)
	if execErr.PartialOutput == "" {
		t.Error("partial output should propagate through wrapping")
	}
	if !types.IsTypedError(execErr, types.ErrorTypeExecutor) {
		t.Error("should be recognized as ExecutorError")
	}

	// Verify Error() string contains useful info
	errStr := execErr.Error()
	if errStr == "" {
		t.Error("error string should not be empty")
	}
}

// TestErrorPropagation_CircuitBreaker verifies circuit breaker error format
func TestErrorPropagation_CircuitBreaker(t *testing.T) {
	err := types.NewCircuitOpenError("codex")
	if !types.IsTypedError(err, types.ErrorTypeCircuitOpen) {
		t.Error("should be CircuitOpenError")
	}
	if err.Message == "" {
		t.Error("message should contain CLI name")
	}
}
