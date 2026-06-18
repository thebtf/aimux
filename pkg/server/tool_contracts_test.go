package server

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestApplyToolContractAsyncMandatorySetsTaskSupportRequired(t *testing.T) {
	t.Parallel()

	tool := mcp.NewTool("task")
	err := applyToolContract(&tool, toolContract{
		Name:           "task",
		Classification: toolClassificationAsyncMandatory,
		AdapterKind:    "loom",
	})
	if err != nil {
		t.Fatalf("applyToolContract() error = %v", err)
	}
	if tool.Execution == nil {
		t.Fatal("tool.Execution = nil, want execution metadata")
	}
	if tool.Execution.TaskSupport != mcp.TaskSupportRequired {
		t.Fatalf("TaskSupport = %v, want %v", tool.Execution.TaskSupport, mcp.TaskSupportRequired)
	}
}

func TestApplyToolContractAsyncMandatoryRequiresAdapterKind(t *testing.T) {
	t.Parallel()

	tool := mcp.NewTool("task")
	err := applyToolContract(&tool, toolContract{
		Name:           "task",
		Classification: toolClassificationAsyncMandatory,
	})
	if err == nil {
		t.Fatal("applyToolContract() error = nil, want adapter kind error")
	}
	if !strings.Contains(err.Error(), "async_mandatory requires adapter kind") {
		t.Fatalf("error = %q, want adapter kind message", err.Error())
	}
}

func TestApplyToolContractRejectsToolNameMismatch(t *testing.T) {
	t.Parallel()

	tool := mcp.NewTool("task")
	err := applyToolContract(&tool, toolContract{
		Name:           "deepresearch",
		Classification: toolClassificationAsyncMandatory,
		AdapterKind:    "loom",
	})
	if err == nil {
		t.Fatal("applyToolContract() error = nil, want name mismatch")
	}
	if !strings.Contains(err.Error(), "tool name mismatch") {
		t.Fatalf("error = %q, want mismatch message", err.Error())
	}
}

func TestApplyToolContractSyncOKDoesNotRequireExecutionMetadata(t *testing.T) {
	t.Parallel()

	tool := mcp.NewTool("status")
	err := applyToolContract(&tool, toolContract{
		Name:           "status",
		Classification: toolClassificationSyncOK,
	})
	if err != nil {
		t.Fatalf("applyToolContract() error = %v", err)
	}
	if tool.Execution != nil {
		t.Fatalf("tool.Execution = %#v, want nil for sync_ok", tool.Execution)
	}
}
