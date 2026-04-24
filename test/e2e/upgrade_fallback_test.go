package e2e

import (
	"path/filepath"
	"testing"
	"time"
)

func TestE2E_Upgrade_Fallback_InvalidModeRejectedButDaemonLives(t *testing.T) {
	v1Bin := buildBinaryVersion(t, "1.0.0")
	testcliBin := buildTestCLI(t)
	tmpDir := t.TempDir()
	configDir, _, _ := shimTestWriteConfig(t, tmpDir)

	_, stdin, reader := startDaemonAndShimWithEnv(t, v1Bin, filepath.Dir(testcliBin), configDir, []string{
		"AIMUX_SESSION_STORE=sqlite",
	})
	initializeMCP(t, stdin, reader)

	if _, err := stdin.Write([]byte(jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "upgrade",
		"arguments": map[string]any{
			"action": "apply",
			"mode":   "bogus",
		},
	}))); err != nil {
		t.Fatalf("write invalid mode request: %v", err)
	}

	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("upgrade invalid mode response: %v", err)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result envelope for invalid mode: %+v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected text content for invalid mode: %+v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first text content item: %+v", content[0])
	}
	text, _ := first["text"].(string)
	if text == "" {
		t.Fatalf("expected non-empty invalid mode message: %+v", first)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true for invalid mode: %+v", result)
	}

	// The daemon/shim pair must remain usable after the rejected request.
	if _, err := stdin.Write([]byte(jsonRPCRequest(3, "resources/read", map[string]any{
		"uri": "aimux://health",
	}))); err != nil {
		t.Fatalf("write health read: %v", err)
	}
	healthResp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("health after invalid mode rejection: %v", err)
	}
	if _, ok := healthResp["result"].(map[string]any); !ok {
		t.Fatalf("expected health result after invalid mode rejection: %+v", healthResp)
	}
}
