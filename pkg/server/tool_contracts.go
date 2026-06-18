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

type toolContract struct {
	Name           string
	Classification toolClassification
	AdapterKind    string
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
		if strings.TrimSpace(contract.AdapterKind) == "" {
			return fmt.Errorf("tool contract %q: async_mandatory requires adapter kind", contractName)
		}
		if tool.Execution == nil {
			tool.Execution = &mcp.ToolExecution{}
		}
		tool.Execution.TaskSupport = mcp.TaskSupportRequired
	case toolClassificationSyncOK, "":
		// No execution metadata required.
	default:
		return fmt.Errorf("tool contract %q: unsupported classification %q", contractName, contract.Classification)
	}
	return nil
}
