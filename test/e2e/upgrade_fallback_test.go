package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func TestE2E_Upgrade_OldSessionRequestThenFreshSessionNewVersion(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("post-exit installed-daemon smoke is Windows-specific")
	}

	const currentVersion = "debug-283-current"
	const nextVersion = "debug-283-next"

	currentBin := buildBinaryVersion(t, currentVersion)
	nextBin := buildBinaryVersion(t, nextVersion)
	nextInstallBin := filepath.Join(filepath.Dir(currentBin), filepath.Base(nextBin))
	copyFileForTest(t, nextBin, nextInstallBin)
	testcliBin := buildTestCLI(t)
	tmpDir := t.TempDir()
	configDir, _, _ := shimTestWriteConfig(t, tmpDir)
	testcliDir := filepath.Dir(testcliBin)

	_, oldStdin, oldReader := startDaemonAndShimWithEnv(t, currentBin, testcliDir, configDir, []string{
		"AIMUX_SESSION_STORE=sqlite",
	})
	oldInit := initializeMCPWithResponse(t, oldStdin, oldReader)
	requireInitializeVersion(t, oldInit, currentVersion)

	if _, err := fmt.Fprint(oldStdin, jsonRPCRequest(2, "tools/call", map[string]any{
		"name": "upgrade",
		"arguments": map[string]any{
			"action": "apply",
			"source": nextInstallBin,
			"force":  true,
		},
	})); err != nil {
		t.Fatalf("write upgrade request: %v", err)
	}
	upgradeResp, err := readResponse(oldReader, 20*time.Second)
	if err != nil {
		t.Fatalf("upgrade response: %v", err)
	}
	upgradePayload := toolJSONPayload(t, upgradeResp)
	if upgradePayload["status"] != "updated_deferred" {
		t.Fatalf("status = %v, want updated_deferred; payload=%v", upgradePayload["status"], upgradePayload)
	}
	if upgradePayload["update_method"] != "deferred" {
		t.Fatalf("update_method = %v, want deferred; payload=%v", upgradePayload["update_method"], upgradePayload)
	}
	topology, ok := upgradePayload["update_topology"].(map[string]any)
	if !ok {
		t.Fatalf("update_topology = %#v, want object; payload=%v", upgradePayload["update_topology"], upgradePayload)
	}
	if topology["restart_topology"] != "post_exit" {
		t.Fatalf("restart_topology = %v, want post_exit; topology=%v", topology["restart_topology"], topology)
	}
	if topology["replacement_started"] != true {
		t.Fatalf("replacement_started = %v, want true; topology=%v", topology["replacement_started"], topology)
	}

	oldHealth := readHealthResource(t, oldStdin, oldReader, 3)
	requireNativeHealthFields(t, oldHealth)
	oldDaemonGeneration := requireStringHealthField(t, oldHealth, "daemon_generation")

	_ = oldStdin.Close()
	waitForBinaryVersion(t, currentBin, nextVersion, 20*time.Second)

	_, freshStdin, freshReader := startDaemonAndShimWithEnv(t, currentBin, testcliDir, configDir, []string{
		"AIMUX_SESSION_STORE=sqlite",
	})
	freshInit := initializeMCPWithResponse(t, freshStdin, freshReader)
	requireInitializeVersion(t, freshInit, nextVersion)
	freshHealth := readHealthResource(t, freshStdin, freshReader, 2)
	requireNativeHealthFields(t, freshHealth)
	freshDaemonGeneration := requireStringHealthField(t, freshHealth, "daemon_generation")
	if freshDaemonGeneration == oldDaemonGeneration {
		t.Fatalf("daemon_generation did not change across update: old=%q fresh=%q; fresh health=%v", oldDaemonGeneration, freshDaemonGeneration, freshHealth)
	}
	requireNumericHealthField(t, freshHealth, "shim_reconnect_fallback_spawned", 0)
	requireNumericHealthField(t, freshHealth, "shim_reconnect_gave_up", 0)
	if freshHealth["version"] != nextVersion {
		t.Fatalf("fresh aimux://health.version = %v, want %s; health=%v", freshHealth["version"], nextVersion, freshHealth)
	}
}

func initializeMCPWithResponse(t *testing.T, stdin io.Writer, reader *bufio.Reader) map[string]any {
	t.Helper()
	fmt.Fprint(stdin, jsonRPCRequest(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-upgrade-smoke", "version": "1.0"},
	}))
	resp, err := readResponse(reader, 5*time.Second)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return resp
}

func requireInitializeVersion(t *testing.T, resp map[string]any, want string) {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result missing: %+v", resp)
	}
	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("initialize serverInfo missing: %+v", result)
	}
	if got, _ := serverInfo["version"].(string); got != want {
		t.Fatalf("initialize serverInfo.version = %q, want %q; serverInfo=%v", got, want, serverInfo)
	}
}

func toolJSONPayload(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tool result missing: %+v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("tool content missing: %+v", result)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("tool content item = %T, want object", content[0])
	}
	text, _ := first["text"].(string)
	if text == "" {
		t.Fatalf("tool content text empty: %+v", first)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tool content is not JSON: %v text=%s", err, text)
	}
	return payload
}

func readHealthResource(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int) map[string]any {
	t.Helper()
	if _, err := fmt.Fprint(stdin, jsonRPCRequest(id, "resources/read", map[string]any{
		"uri": "aimux://health",
	})); err != nil {
		t.Fatalf("write health read: %v", err)
	}
	resp, err := readResponse(reader, 10*time.Second)
	if err != nil {
		t.Fatalf("health resource response: %v", err)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("health result missing: %+v", resp)
	}
	contents, ok := result["contents"].([]any)
	if !ok || len(contents) == 0 {
		t.Fatalf("health contents missing: %+v", result)
	}
	first, ok := contents[0].(map[string]any)
	if !ok {
		t.Fatalf("health content item = %T, want object", contents[0])
	}
	text, _ := first["text"].(string)
	if text == "" {
		t.Fatalf("health content text empty: %+v", first)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("health content is not JSON: %v text=%s", err, text)
	}
	return payload
}

func requireNativeHealthFields(t *testing.T, health map[string]any) {
	t.Helper()
	for _, key := range []string{
		"engine_name",
		"daemon_generation",
		"owner_count",
		"owners",
		"handoff",
		"shim_reconnect_refreshed",
		"shim_reconnect_fallback_spawned",
		"shim_reconnect_gave_up",
	} {
		if _, ok := health[key]; !ok {
			t.Fatalf("health missing %s: %v", key, health)
		}
	}
	owners, ok := health["owners"].([]any)
	if !ok || len(owners) == 0 {
		t.Fatalf("health owners = %#v, want non-empty array", health["owners"])
	}
	owner, ok := owners[0].(map[string]any)
	if !ok {
		t.Fatalf("health owner item = %T, want object", owners[0])
	}
	if got, _ := owner["owner_generation"].(string); got == "" {
		t.Fatalf("owner_generation missing: %v", owner)
	}
	if got, _ := owner["restore_source"].(string); got == "" {
		t.Fatalf("restore_source missing: %v", owner)
	}
}

func requireStringHealthField(t *testing.T, health map[string]any, key string) string {
	t.Helper()
	got, ok := health[key].(string)
	if !ok || got == "" {
		t.Fatalf("health[%s] = %#v, want non-empty string; health=%v", key, health[key], health)
	}
	return got
}

func requireNumericHealthField(t *testing.T, health map[string]any, key string, want float64) {
	t.Helper()
	got, ok := health[key].(float64)
	if !ok {
		t.Fatalf("health[%s] = %#v, want numeric; health=%v", key, health[key], health)
	}
	if got != want {
		t.Fatalf("health[%s] = %v, want %v; health=%v", key, got, want, health)
	}
}

func waitForBinaryVersion(t *testing.T, bin string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	var lastErr error
	for time.Now().Before(deadline) {
		out, err := exec.Command(bin, "--version").CombinedOutput()
		last = strings.TrimSpace(string(out))
		lastErr = err
		if err == nil && strings.Contains(last, want) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("binary %s did not report version %q within %v; last=%q err=%v", bin, want, timeout, last, lastErr)
}
