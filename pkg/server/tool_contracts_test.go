package server

import (
	"context"
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
	// F3: the adapter kind must be surfaced on tool _meta, not silently discarded.
	if tool.Meta == nil || tool.Meta.AdditionalFields == nil {
		t.Fatal("tool.Meta.AdditionalFields = nil, want adapter kind surfaced")
	}
	if got := tool.Meta.AdditionalFields[adapterKindMetaKey]; got != "loom" {
		t.Fatalf("meta[%q] = %v, want %q", adapterKindMetaKey, got, "loom")
	}
}

func TestNewServerInitializeAdvertisesToolCallTaskSupport(t *testing.T) {
	t.Parallel()

	srv := testServer(t)
	response := srv.mcp.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`))

	resp, ok := response.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("initialize response = %T, want JSONRPCResponse", response)
	}
	result, ok := resp.Result.(mcp.InitializeResult)
	if !ok {
		t.Fatalf("initialize result = %T, want InitializeResult", resp.Result)
	}
	if result.Capabilities.Tasks == nil {
		t.Fatal("initialize tasks capability = nil, want tool-call task support advertised")
	}
	if result.Capabilities.Tasks.List != nil {
		t.Fatalf("tasks.list = %#v, want nil for minimal task capability surface", result.Capabilities.Tasks.List)
	}
	if result.Capabilities.Tasks.Cancel != nil {
		t.Fatalf("tasks.cancel = %#v, want nil for minimal task capability surface", result.Capabilities.Tasks.Cancel)
	}
	if result.Capabilities.Tasks.Requests == nil || result.Capabilities.Tasks.Requests.Tools == nil || result.Capabilities.Tasks.Requests.Tools.Call == nil {
		t.Fatalf("tasks.requests.tools.call missing from initialize response: %#v", result.Capabilities.Tasks)
	}
	if result.Capabilities.Tools == nil {
		t.Fatal("initialize tools capability = nil, want tool listing still advertised")
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
