package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSanitizedAimuxE2EEnvStripsInheritedMuxcoreEnvAndPreservesOverrides(t *testing.T) {
	fixturePointer := filepath.Join(t.TempDir(), "fixture-active-engine.txt")
	inheritedPointer := filepath.Join(t.TempDir(), "external-active-engine.txt")
	env := sanitizedAimuxE2EEnv([]string{
		"PATH=/test/bin",
		"AIMUX_CONFIG_DIR=/repo/config",
		"MCPMUX_ACTIVE_ENGINE_FILE=" + inheritedPointer,
		"MCPMUX_SUCCESSOR_EXE=/external/aimux-stage-leak.exe",
		"MCP_MUX_SESSION_ID=operator-session",
		"MCP_MUX_EXTRA=value",
	},
		"MCPMUX_ACTIVE_ENGINE_FILE="+fixturePointer,
		"MCP_MUX_TEST_OVERRIDE=fixture-owned",
	)

	if got := testEnvValue(env, "MCPMUX_ACTIVE_ENGINE_FILE"); got != fixturePointer {
		t.Fatalf("MCPMUX_ACTIVE_ENGINE_FILE = %q, want explicit fixture override %q", got, fixturePointer)
	}
	for _, forbidden := range []string{"MCPMUX_SUCCESSOR_EXE", "MCP_MUX_SESSION_ID", "MCP_MUX_EXTRA"} {
		if got := testEnvValue(env, forbidden); got != "" {
			t.Fatalf("%s survived quarantine with value %q", forbidden, got)
		}
	}
	if got := testEnvValue(env, "MCP_MUX_TEST_OVERRIDE"); got != "fixture-owned" {
		t.Fatalf("MCP_MUX_TEST_OVERRIDE = %q, want explicit fixture-owned override", got)
	}
	if got := testEnvValue(env, "PATH"); got != "/test/bin" {
		t.Fatalf("PATH = %q, want non-muxcore env preserved", got)
	}
}

func testEnvValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}

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

func TestE2E_Upgrade_ActivePointerSuccessorRuntimeAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("active-pointer upgrade smoke requires real daemon/shim processes")
	}

	const currentVersion = "debug-302-current"
	const nextVersion = "debug-302-next"
	const thirdVersion = "debug-302-third"

	currentBin := buildBinaryVersion(t, currentVersion)
	nextBin := buildBinaryVersion(t, nextVersion)
	thirdBin := buildBinaryVersion(t, thirdVersion)
	nextInstallBin := filepath.Join(filepath.Dir(currentBin), filepath.Base(nextBin))
	copyFileForTest(t, nextBin, nextInstallBin)
	thirdInstallBin := filepath.Join(filepath.Dir(currentBin), filepath.Base(thirdBin))
	copyFileForTest(t, thirdBin, thirdInstallBin)
	testcliBin := buildTestCLI(t)
	tmpDir := t.TempDir()
	configDir, _, _ := shimTestWriteConfig(t, tmpDir)
	testcliDir := filepath.Dir(testcliBin)

	activeEngineFile := filepath.Join(tmpDir, "active-engine.txt")
	if err := os.WriteFile(activeEngineFile, []byte(currentBin+"\n"), 0o600); err != nil {
		t.Fatalf("write initial active engine pointer: %v", err)
	}

	engineName := fmt.Sprintf("ap-%08x", uint32(time.Now().UnixNano()))
	// The daemon binds its control socket under this TMPDIR as
	// <isolatedTmp>/<engineName>-<hash>-muxd.ctl.sock. Unix-domain socket limits
	// are short (Linux ~108 bytes, macOS ~104 bytes), so use the shared short temp
	// root plus a short engine label rather than t.TempDir() or macOS /var/folders.
	isolatedTmp := newMuxcoreIsolatedTemp(t, "ax")
	pathEnv := testcliDir + string(os.PathListSeparator) + os.Getenv("PATH")
	baseEnv := sanitizedAimuxE2EEnv(os.Environ(),
		"AIMUX_CONFIG_DIR="+configDir,
		"AIMUX_ENGINE_NAME="+engineName,
		"AIMUX_WARMUP=false",
		"AIMUX_SESSION_STORE=sqlite",
		"PATH="+pathEnv,
		"TMPDIR="+isolatedTmp,
		"TEMP="+isolatedTmp,
		"TMP="+isolatedTmp,
		"MCPMUX_ACTIVE_ENGINE_FILE="+activeEngineFile,
	)

	var ctlSock string
	daemonCmd := exec.Command(currentBin, "--muxcore-daemon")
	daemonCmd.Env = baseEnv
	daemonCmd.Stderr = os.Stderr
	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("start active-pointer daemon: %v", err)
	}
	t.Cleanup(func() {
		cleanupDaemon(t, ctlSock, daemonCmd, "TestE2E_Upgrade_ActivePointerSuccessorRuntimeAcceptance")
	})
	rec := waitForHealthyRegistryDescriptor(t, isolatedTmp, engineName, 60*time.Second)
	ctlSock = rec.Descriptor.DaemonControlPath

	oldStdin, oldReader := startShimWithEnv(t, currentBin, baseEnv)
	oldInit := initializeMCPWithResponse(t, oldStdin, oldReader)
	requireInitializeVersion(t, oldInit, currentVersion)
	requireToolNamed(t, readToolsList(t, oldStdin, oldReader, 2), "upgrade")
	oldHealth := readHealthResource(t, oldStdin, oldReader, 3)
	requireNativeHealthFields(t, oldHealth)
	oldDaemonGeneration := requireStringHealthField(t, oldHealth, "daemon_generation")

	firstSuccessor := applyAndAssertActivePointerUpgrade(t, oldStdin, oldReader, 4, activeEngineFile, nextInstallBin)
	requireExecutableVersion(t, firstSuccessor, nextVersion)

	oldPostHealth := readHealthResource(t, oldStdin, oldReader, 5)
	requireNativeHealthFields(t, oldPostHealth)
	if oldPostHealth["version"] != nextVersion {
		t.Fatalf("old session health.version after successor restart = %v, want %s; health=%v", oldPostHealth["version"], nextVersion, oldPostHealth)
	}
	firstDaemonGeneration := requireStringHealthField(t, oldPostHealth, "daemon_generation")
	if firstDaemonGeneration == oldDaemonGeneration {
		t.Fatalf("daemon_generation did not change after first successor restart: old=%q fresh=%q; health=%v", oldDaemonGeneration, firstDaemonGeneration, oldPostHealth)
	}
	requireHandoffNumericFieldAtLeast(t, oldPostHealth, "restored_owner_count", 1)
	requireNumericHealthFieldAtLeast(t, oldPostHealth, "shim_reconnect_refreshed", 1)
	requireNumericHealthField(t, oldPostHealth, "shim_reconnect_fallback_spawned", 0)
	requireNumericHealthField(t, oldPostHealth, "shim_reconnect_gave_up", 0)
	requireToolNamed(t, readToolsList(t, oldStdin, oldReader, 6), "upgrade")

	freshStdin, freshReader := startShimWithEnv(t, currentBin, baseEnv)
	freshInit := initializeMCPWithResponse(t, freshStdin, freshReader)
	requireInitializeVersion(t, freshInit, nextVersion)
	requireToolNamed(t, readToolsList(t, freshStdin, freshReader, 2), "upgrade")
	freshHealth := readHealthResource(t, freshStdin, freshReader, 3)
	requireNativeHealthFields(t, freshHealth)
	if freshHealth["version"] != nextVersion {
		t.Fatalf("fresh shim health.version = %v, want %s; health=%v", freshHealth["version"], nextVersion, freshHealth)
	}

	secondSuccessor := applyAndAssertActivePointerUpgrade(t, freshStdin, freshReader, 4, activeEngineFile, thirdInstallBin)
	if secondSuccessor == firstSuccessor {
		t.Fatalf("active pointer did not advance on second successor restart: %q", secondSuccessor)
	}
	requireExecutableVersion(t, secondSuccessor, thirdVersion)

	secondHealth := readHealthResource(t, freshStdin, freshReader, 5)
	requireNativeHealthFields(t, secondHealth)
	if secondHealth["version"] != thirdVersion {
		t.Fatalf("second-cycle health.version = %v, want %s; health=%v", secondHealth["version"], thirdVersion, secondHealth)
	}
	secondDaemonGeneration := requireStringHealthField(t, secondHealth, "daemon_generation")
	if secondDaemonGeneration == firstDaemonGeneration {
		t.Fatalf("daemon_generation did not change after second successor restart: first=%q second=%q; health=%v", firstDaemonGeneration, secondDaemonGeneration, secondHealth)
	}
	requireHandoffNumericFieldAtLeast(t, secondHealth, "restored_owner_count", 1)
	requireNumericHealthFieldAtLeast(t, secondHealth, "shim_reconnect_refreshed", 1)
	requireNumericHealthField(t, secondHealth, "shim_reconnect_fallback_spawned", 0)
	requireNumericHealthField(t, secondHealth, "shim_reconnect_gave_up", 0)
	requireToolNamed(t, readToolsList(t, freshStdin, freshReader, 6), "upgrade")

	freshThirdStdin, freshThirdReader := startShimWithEnv(t, currentBin, baseEnv)
	freshThirdInit := initializeMCPWithResponse(t, freshThirdStdin, freshThirdReader)
	requireInitializeVersion(t, freshThirdInit, thirdVersion)
	requireToolNamed(t, readToolsList(t, freshThirdStdin, freshThirdReader, 2), "upgrade")
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

func readToolsList(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int) []any {
	t.Helper()
	if _, err := fmt.Fprint(stdin, jsonRPCRequest(id, "tools/list", map[string]any{})); err != nil {
		t.Fatalf("write tools/list: %v", err)
	}
	resp := readResponseForID(t, reader, id, 10*time.Second)
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result missing: %+v", resp)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools/list tools missing: %+v", result)
	}
	return tools
}

func requireToolNamed(t *testing.T, tools []any, want string) {
	t.Helper()
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if got, _ := tool["name"].(string); got == want {
			return
		}
	}
	t.Fatalf("tools/list missing %q: %v", want, tools)
}

func readHealthResource(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int) map[string]any {
	t.Helper()
	if _, err := fmt.Fprint(stdin, jsonRPCRequest(id, "resources/read", map[string]any{
		"uri": "aimux://health",
	})); err != nil {
		t.Fatalf("write health read: %v", err)
	}
	resp := readResponseForID(t, reader, id, 10*time.Second)
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

func requireHandoffNumericFieldAtLeast(t *testing.T, health map[string]any, key string, min float64) {
	t.Helper()
	handoff, ok := health["handoff"].(map[string]any)
	if !ok {
		t.Fatalf("health[handoff] = %#v, want object; health=%v", health["handoff"], health)
	}
	got, ok := handoff[key].(float64)
	if !ok {
		t.Fatalf("health[handoff][%s] = %#v, want numeric; health=%v", key, handoff[key], health)
	}
	if got < min {
		t.Fatalf("health[handoff][%s] = %v, want >= %v; health=%v", key, got, min, health)
	}
}

func requireNumericHealthFieldAtLeast(t *testing.T, health map[string]any, key string, min float64) {
	t.Helper()
	got, ok := health[key].(float64)
	if !ok {
		t.Fatalf("health[%s] = %#v, want numeric; health=%v", key, health[key], health)
	}
	if got < min {
		t.Fatalf("health[%s] = %v, want >= %v; health=%v", key, got, min, health)
	}
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

func applyAndAssertActivePointerUpgrade(t *testing.T, stdin io.Writer, reader *bufio.Reader, id int, activeEngineFile, source string) string {
	t.Helper()
	if _, err := fmt.Fprint(stdin, jsonRPCRequest(id, "tools/call", map[string]any{
		"name": "upgrade",
		"arguments": map[string]any{
			"action": "apply",
			"source": source,
			"force":  true,
		},
	})); err != nil {
		t.Fatalf("write active-pointer upgrade request: %v", err)
	}
	upgradeResp := readResponseForID(t, reader, id, 30*time.Second)
	if rpcErr, ok := upgradeResp["error"].(map[string]any); ok {
		msg, _ := rpcErr["message"].(string)
		if !strings.Contains(msg, "upstream restarted, request lost during reconnect") {
			t.Fatalf("active-pointer upgrade returned unexpected JSON-RPC error: %+v", upgradeResp)
		}
		t.Logf("active-pointer upgrade request was lost during reconnect; continuing to verify post-restart runtime state")
	} else {
		upgradePayload := toolJSONPayload(t, upgradeResp)
		if upgradePayload["status"] != "updated_hot_swap" {
			t.Fatalf("status = %v, want updated_hot_swap; payload=%v", upgradePayload["status"], upgradePayload)
		}
		if upgradePayload["update_method"] != "hot_swap" {
			t.Fatalf("update_method = %v, want hot_swap; payload=%v", upgradePayload["update_method"], upgradePayload)
		}
		topology, ok := upgradePayload["update_topology"].(map[string]any)
		if !ok {
			t.Fatalf("update_topology = %#v, want object; payload=%v", upgradePayload["update_topology"], upgradePayload)
		}
		for key, want := range map[string]any{
			"restart_topology":    "graceful_restart",
			"daemon_was_running":  true,
			"graceful_restarted":  true,
			"fallback_shutdown":   false,
			"replacement_started": true,
			"replacement_ready":   true,
		} {
			if topology[key] != want {
				t.Fatalf("update_topology[%s] = %v, want %v; topology=%v payload=%v", key, topology[key], want, topology, upgradePayload)
			}
		}
	}

	pointerPayload, err := os.ReadFile(activeEngineFile)
	if err != nil {
		t.Fatalf("read active engine pointer: %v", err)
	}
	successor := strings.TrimSpace(string(pointerPayload))
	if successor == "" {
		t.Fatalf("active engine pointer is empty")
	}
	if _, err := os.Stat(successor); err != nil {
		t.Fatalf("active engine successor does not exist: %s: %v", successor, err)
	}
	return successor
}

func readResponseForID(t *testing.T, reader *bufio.Reader, id int, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timeout waiting for JSON-RPC response id=%d after %v", id, timeout)
		}
		resp, err := readResponse(reader, remaining)
		if err != nil {
			t.Fatalf("response id=%d: %v", id, err)
		}
		if jsonRPCIDMatches(resp["id"], id) {
			return resp
		}
		t.Logf("skipping response id=%v while waiting for id=%d", resp["id"], id)
	}
}

func jsonRPCIDMatches(got any, want int) bool {
	switch v := got.(type) {
	case float64:
		return int(v) == want
	case int:
		return v == want
	case json.Number:
		n, err := v.Int64()
		return err == nil && int(n) == want
	case string:
		return v == fmt.Sprintf("%d", want)
	default:
		return false
	}
}

func requireExecutableVersion(t *testing.T, bin string, want string) {
	t.Helper()
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version: %v\n%s", bin, err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, want) {
		t.Fatalf("%s --version = %q, want substring %q", bin, got, want)
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

func startShimWithEnv(t *testing.T, aimuxBin string, env []string) (io.WriteCloser, *bufio.Reader) {
	t.Helper()

	shimStdinR, shimStdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("shim stdin pipe: %v", err)
	}
	shimStdoutR, shimStdoutW, err := os.Pipe()
	if err != nil {
		shimStdinR.Close()
		shimStdinW.Close()
		t.Fatalf("shim stdout pipe: %v", err)
	}

	shimCmd := exec.Command(aimuxBin)
	shimCmd.Env = env
	shimCmd.Stdin = shimStdinR
	shimCmd.Stdout = shimStdoutW
	shimCmd.Stderr = os.Stderr
	if err := shimCmd.Start(); err != nil {
		shimStdinR.Close()
		shimStdinW.Close()
		shimStdoutR.Close()
		shimStdoutW.Close()
		t.Fatalf("start shim: %v", err)
	}
	shimStdinR.Close()
	shimStdoutW.Close()

	t.Cleanup(func() {
		shimStdinW.Close()
		if shimCmd.Process == nil {
			shimStdoutR.Close()
			return
		}
		done := make(chan struct{})
		go func() {
			_ = shimCmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = shimCmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(1 * time.Second):
				t.Logf("startShimWithEnv cleanup: shim Wait() did not return within 1s after Kill")
			}
		}
		shimStdoutR.Close()
	})

	return shimStdinW, bufio.NewReader(shimStdoutR)
}
