package executor_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/pty"
	"github.com/thebtf/aimux/pkg/types"
)

// TestCLIPTYAdapter_CompileCheck verifies that CLIPTYAdapter satisfies ExecutorV2
// at the type level. The package-level var in adapter_pty.go already enforces this
// at compile time; this test makes the intent explicit and visible in test output.
func TestCLIPTYAdapter_CompileCheck(t *testing.T) {
	t.Parallel()
	// pty.New() returns *pty.Executor which satisfies types.LegacyExecutor.
	var _ types.ExecutorV2 = executor.NewCLIPTYAdapter(pty.New())
}

// TestCLIPTYAdapter_Info verifies the static ExecutorInfo returned by the adapter.
func TestCLIPTYAdapter_Info(t *testing.T) {
	t.Parallel()

	// pty.New() satisfies types.LegacyExecutor — passed directly.
	adapter := executor.NewCLIPTYAdapter(pty.New())

	info := adapter.Info()

	if info.Name != "pty" {
		t.Errorf("Info().Name = %q, want %q", info.Name, "pty")
	}
	if info.Type != types.ExecutorTypeCLI {
		t.Errorf("Info().Type = %v, want ExecutorTypeCLI", info.Type)
	}
	if info.Capabilities.Streaming {
		t.Error("Info().Capabilities.Streaming = true, want false")
	}
	if info.Capabilities.PersistentSessions {
		t.Error("Info().Capabilities.PersistentSessions = true, want false")
	}
}
