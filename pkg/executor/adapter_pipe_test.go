package executor_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/types"
)

// TestCLIPipeAdapter_CompileCheck verifies that CLIPipeAdapter satisfies
// ExecutorV2 at compile time (redundant with the package-level var _ check,
// but acts as an explicit, searchable test assertion).
func TestCLIPipeAdapter_CompileCheck(t *testing.T) {
	t.Parallel()

	var _ types.ExecutorV2 = executor.NewCLIPipeAdapter(pipe.New())
}

// TestCLIPipeAdapter_Info verifies that Info() returns the correct ExecutorInfo.
func TestCLIPipeAdapter_Info(t *testing.T) {
	t.Parallel()

	adapter := executor.NewCLIPipeAdapter(pipe.New())
	info := adapter.Info()

	if info.Name != "pipe" {
		t.Errorf("Info().Name = %q; want %q", info.Name, "pipe")
	}
	if info.Type != types.ExecutorTypeCLI {
		t.Errorf("Info().Type = %v; want ExecutorTypeCLI", info.Type)
	}
	if !info.Capabilities.PersistentSessions {
		t.Error("Info().Capabilities.PersistentSessions should be true")
	}
	if info.Capabilities.Streaming {
		t.Error("Info().Capabilities.Streaming should be false for pipe executor")
	}
}
