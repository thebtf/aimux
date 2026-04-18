package e2e

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// TestE2E_Status_BriefOmitsContent verifies that status on a completed job omits content by default.
func TestE2E_Status_BriefOmitsContent(t *testing.T) {
	stdin, reader := initTestCLIServer(t)

	// Run exec asynchronously so we can poll status on the resulting job.
	fmt.Fprint(stdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "exec",
		"arguments": map[string]any{
			"prompt": "hello budget test",
			"cli":    "codex",
			"async":  true,
		},
	}))

	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}

	data := extractToolJSON(t, resp)
	jobID, _ := data["job_id"].(string)
	if jobID == "" {
		t.Fatalf("no job_id: %v", data)
	}

	// Poll status until terminal.
	var completedData map[string]any
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		fmt.Fprint(stdin, jsonRPCRequest(10+i, "tools/call", map[string]any{
			"name":      "status",
			"arguments": map[string]any{"job_id": jobID},
		}))
		pr, perr := readResponse(reader, 5*time.Second)
		if perr != nil {
			t.Fatalf("status poll: %v", perr)
		}
		pd := extractToolJSON(t, pr)
		status, _ := pd["status"].(string)
		if status == "completed" || status == "failed" {
			completedData = pd
			break
		}
	}

	if completedData == nil {
		t.Fatal("job did not complete within timeout")
	}

	if completedData["content"] != nil {
		t.Errorf("brief status should not include content, got: %v", completedData["content"])
	}
	if completedData["content_length"] == nil {
		t.Error("brief status should include content_length field")
	}

	jsonBytes, _ := json.Marshal(completedData)
	if len(jsonBytes) > 4096 {
		t.Errorf("brief status response %d bytes exceeds 4096 byte budget", len(jsonBytes))
	}

	fmt.Fprint(stdin, jsonRPCRequest(50, "tools/call", map[string]any{
		"name": "status",
		"arguments": map[string]any{
			"job_id":          jobID,
			"include_content": true,
		},
	}))
	fullResp, ferr := readResponse(reader, 5*time.Second)
	if ferr != nil {
		t.Fatalf("status include_content: %v", ferr)
	}
	fullData := extractToolJSON(t, fullResp)
	if fullData["content"] == nil {
		t.Error("include_content=true should return content field")
	}
}
