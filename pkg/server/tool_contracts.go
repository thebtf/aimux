package server

import (
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

type toolClassification string

const (
	toolClassificationSyncOK         toolClassification = "sync_ok"
	toolClassificationAsyncMandatory toolClassification = "async_mandatory"
)

// adapterKindMetaKey is the tool _meta field under which an async_mandatory
// tool's adapter kind is surfaced, so the registration-time contract is
// observable to clients and introspection instead of a write-only guard.
const adapterKindMetaKey = "aimux/adapter_kind"

type toolContract struct {
	Name           string
	Classification toolClassification
	// AdapterKind names the async execution adapter backing an
	// async_mandatory tool (currently always "loom"). It is a
	// registration-time contract annotation, not a runtime router key: it
	// asserts at registration that an async adapter exists for the tool, and
	// it is surfaced onto tool _meta (adapterKindMetaKey) for introspection.
	// Required (non-empty) when Classification is async_mandatory.
	AdapterKind string
}

func (s *Server) registerContractedTool(contract toolContract, tool mcp.Tool, handler mcpserver.ToolHandlerFunc) {
	if err := applyToolContract(&tool, contract); err != nil {
		panic(err)
	}
	s.mcp.AddTool(tool, handler)
}

func applyToolContract(tool *mcp.Tool, contract toolContract) error {
	if tool == nil {
		return fmt.Errorf("tool contract %q: tool is nil", contract.Name)
	}
	toolName := strings.TrimSpace(tool.Name)
	contractName := strings.TrimSpace(contract.Name)
	if contractName == "" {
		return fmt.Errorf("tool contract: name is required")
	}
	if toolName != contractName {
		return fmt.Errorf("tool contract %q: tool name mismatch: %q", contractName, toolName)
	}

	switch contract.Classification {
	case toolClassificationAsyncMandatory:
		adapterKind := strings.TrimSpace(contract.AdapterKind)
		if adapterKind == "" {
			return fmt.Errorf("tool contract %q: async_mandatory requires adapter kind", contractName)
		}
		if tool.Execution == nil {
			tool.Execution = &mcp.ToolExecution{}
		}
		tool.Execution.TaskSupport = mcp.TaskSupportRequired
		// Surface the adapter kind on tool _meta so the contract is
		// observable to clients/introspection rather than a write-only guard.
		if tool.Meta == nil {
			tool.Meta = &mcp.Meta{}
		}
		if tool.Meta.AdditionalFields == nil {
			tool.Meta.AdditionalFields = map[string]any{}
		}
		tool.Meta.AdditionalFields[adapterKindMetaKey] = adapterKind
	case toolClassificationSyncOK, "":
		// No execution metadata required.
	default:
		return fmt.Errorf("tool contract %q: unsupported classification %q", contractName, contract.Classification)
	}
	return nil
}
