package executor_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/conpty"
	"github.com/thebtf/aimux/pkg/types"
)

// TestCLIConPTYAdapter_Info verifies that Info() returns the expected metadata.
func TestCLIConPTYAdapter_Info(t *testing.T) {
	legacy := conpty.New()
	adapter := executor.NewCLIConPTYAdapter(legacy)

	info := adapter.Info()

	if info.Name != "conpty" {
		t.Errorf("Info().Name = %q, want %q", info.Name, "conpty")
	}
	if info.Type != types.ExecutorTypeCLI {
		t.Errorf("Info().Type = %v, want ExecutorTypeCLI", info.Type)
	}
	if info.Capabilities.PersistentSessions {
		t.Error("Info().Capabilities.PersistentSessions = true, want false (M6 not yet implemented)")
	}
	if info.Capabilities.Streaming {
		t.Error("Info().Capabilities.Streaming = true, want false")
	}
}

// TestCLIConPTYAdapter_CompileCheck verifies that CLIConPTYAdapter implements ExecutorV2.
// The compile-time assertion in adapter_conpty.go already enforces this, but this
// test makes the requirement explicit and visible in test output.
func TestCLIConPTYAdapter_CompileCheck(t *testing.T) {
	legacy := conpty.New()
	adapter := executor.NewCLIConPTYAdapter(legacy)

	// Interface assignment — if this compiles, the contract is satisfied.
	var _ types.ExecutorV2 = adapter
}
