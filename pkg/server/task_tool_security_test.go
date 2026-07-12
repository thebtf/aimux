package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/executor/code"
	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/mcp-mux/muxcore"
)

func TestSplitCommandLine_SingleQuoteInsideDoubleQuoteStaysLiteral(t *testing.T) {
	got, err := splitCommandLine(`-p "Hello' --debug --model gemini-2.0-flash-thinking '"`)
	if err != nil {
		t.Fatalf("splitCommandLine returned error: %v", err)
	}
	if len(got) != 2 || got[0] != "-p" || !strings.Contains(got[1], "--debug") || !strings.Contains(got[1], "--model") {
		t.Fatalf("tokens = %#v, want -p plus one literal prompt", got)
	}
}

func TestSplitCommandLine_SingleQuoteOutsideQuotesIsActiveQuote(t *testing.T) {
	got, err := splitCommandLine(`-p 'hello world'`)
	if err != nil {
		t.Fatalf("splitCommandLine returned error: %v", err)
	}
	if len(got) != 2 || got[1] != "hello world" {
		t.Fatalf("tokens = %#v, want quoted prompt", got)
	}
}

func TestCommandArgsTemplateArgs_RejectsModelWhitespace(t *testing.T) {
	profile := &config.CLIProfile{Command: config.CommandConfig{Base: "gemini", ArgsTemplate: "{{if .Model}}--model {{.Model}}{{end}} -p \"{{.Prompt}}\""}}
	if _, ok := commandArgsTemplateArgs(profile, picker.TaskSpec{Prompt: "ok", Model: "evil --extra-flag injected"}); ok {
		t.Fatal("commandArgsTemplateArgs accepted a whitespace-bearing model")
	}
}

func TestCommandArgsTemplateArgs_AcceptsCleanInput(t *testing.T) {
	profile := &config.CLIProfile{Command: config.CommandConfig{Base: "gemini", ArgsTemplate: "{{if .Model}}--model {{.Model}}{{end}} -p \"{{.Prompt}}\""}}
	args, ok := commandArgsTemplateArgs(profile, picker.TaskSpec{Prompt: "Tell me about the weather today", Model: "gemini-2.0-flash"})
	if !ok || len(args) != 4 || args[0] != "--model" || args[1] != "gemini-2.0-flash" || args[2] != "-p" || args[3] != "Tell me about the weather today" {
		t.Fatalf("args/ok = %#v/%t, want clean template argv", args, ok)
	}
}

func TestCommandArgsTemplateArgs_NewlineInValueIsRejected(t *testing.T) {
	profile := &config.CLIProfile{Command: config.CommandConfig{Base: "gemini", ArgsTemplate: "-p \"{{.Prompt}}\""}}
	if _, ok := commandArgsTemplateArgs(profile, picker.TaskSpec{Prompt: "hello\n--extra-flag"}); ok {
		t.Fatal("commandArgsTemplateArgs accepted a newline-bearing prompt")
	}
}

func TestCommandArgsTemplateArgs_SingleQuoteInValueIsRejected(t *testing.T) {
	profile := &config.CLIProfile{Command: config.CommandConfig{Base: "gemini", ArgsTemplate: "-p \"{{.Prompt}}\""}}
	if _, ok := commandArgsTemplateArgs(profile, picker.TaskSpec{Prompt: "hello' --extra-flag '"}); ok {
		t.Fatal("commandArgsTemplateArgs accepted a single-quote-bearing prompt")
	}
}

func TestTaskToolDoesNotPersistSensitiveSessionEnv(t *testing.T) {
	db, err := sql.Open("sqlite", nextTaskToolFixtureDSN())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	engine, err := loom.NewEngine(db, "task-env-security")
	if err != nil {
		t.Fatalf("loom.NewEngine: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = engine.Close(ctx)
	})

	worker := &recordingTaskWorker{workerType: code.WorkerTypeCode}
	engine.RegisterWorker(code.WorkerTypeCode, worker)
	const secret = "sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
	ctx := context.WithValue(context.Background(), projectContextKey{}, muxcore.ProjectContext{
		ID:  muxcore.ProjectContextID("task-env-security"),
		Cwd: t.TempDir(),
		Env: map[string]string{
			"OPENAI_API_KEY": secret,
			"AIMUX_REGION":   "eu-test-1",
		},
	})

	request, err := parseTaskToolRequest(ctx, makeRequest("task", map[string]any{
		"prompt":     "Implement the requested security change.",
		"task_class": "code",
	}))
	if err != nil {
		t.Fatalf("parseTaskToolRequest: %v", err)
	}
	router, err := NewTaskRouter(TaskRouterConfig{Loom: engine})
	if err != nil {
		t.Fatalf("NewTaskRouter: %v", err)
	}
	result, err := router.Dispatch(ctx, request)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	executed := worker.onlyTask(t)
	if executed.Env["OPENAI_API_KEY"] != secret || executed.Env["AIMUX_REGION"] != "eu-test-1" {
		t.Fatalf("worker env = %#v, want raw request env available only at execution", executed.Env)
	}
	waitForTaskTerminal(t, engine, result.TaskID)

	var envJSON string
	var resultText string
	var metadataJSON string
	if err := db.QueryRow("SELECT env, result, metadata FROM tasks WHERE id = ?", result.TaskID).Scan(&envJSON, &resultText, &metadataJSON); err != nil {
		t.Fatalf("read durable task row: %v", err)
	}
	for _, value := range []string{secret, "eu-test-1"} {
		if strings.Contains(envJSON, value) || strings.Contains(resultText, value) || strings.Contains(metadataJSON, value) {
			t.Errorf("durable task row leaked raw env value %q: env=%s result=%s metadata=%s", value, envJSON, resultText, metadataJSON)
		}
	}
	if envJSON != "{}" {
		t.Errorf("durable env = %s, want no persisted values", envJSON)
	}

	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode durable metadata: %v", err)
	}
	if metadata["task_env_source"] != "mux_project_context" {
		t.Errorf("task_env_source = %#v, want mux_project_context", metadata["task_env_source"])
	}
	keys, ok := metadata["task_env_keys"].([]any)
	if !ok || len(keys) != 2 {
		t.Errorf("task_env_keys = %#v, want two safe key names", metadata["task_env_keys"])
	}
	if fingerprint, _ := metadata["task_env_keyset_fingerprint"].(string); len(fingerprint) != 64 {
		t.Errorf("task_env_keyset_fingerprint = %#v, want SHA-256 hex", metadata["task_env_keyset_fingerprint"])
	}
	rows, err := db.Query("SELECT summary, payload_json FROM task_artifacts WHERE task_id = ?", result.TaskID)
	if err != nil {
		t.Fatalf("read durable task artifacts: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var summary, payload string
		if err := rows.Scan(&summary, &payload); err != nil {
			t.Fatalf("scan durable task artifact: %v", err)
		}
		for _, value := range []string{secret, "eu-test-1"} {
			if strings.Contains(summary, value) || strings.Contains(payload, value) {
				t.Errorf("durable artifact leaked raw env value %q: summary=%s payload=%s", value, summary, payload)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate durable task artifacts: %v", err)
	}
}

func TestEnvMetadataBoundsKeysAndIgnoresValues(t *testing.T) {
	const maxKeys = 32
	const maxKeyBytes = 1024
	first := make(map[string]string, maxKeys+10)
	second := make(map[string]string, maxKeys+10)
	for i := 0; i < maxKeys+10; i++ {
		key := strings.Repeat("K", 40) + string(rune('A'+i))
		first[key] = "first-value"
		second[key] = "second-value"
	}
	keys, fingerprint := EnvMetadata(first)
	otherKeys, otherFingerprint := EnvMetadata(second)
	if len(keys) > maxKeys || len(otherKeys) > maxKeys {
		t.Fatalf("metadata keys = %d/%d, want <= %d", len(keys), len(otherKeys), maxKeys)
	}
	keyBytes := 0
	for _, key := range keys {
		keyBytes += len(key)
	}
	if keyBytes > maxKeyBytes {
		t.Fatalf("metadata key bytes = %d, want <= %d", keyBytes, maxKeyBytes)
	}
	if fingerprint != otherFingerprint || strings.Join(keys, "\x00") != strings.Join(otherKeys, "\x00") {
		t.Fatalf("metadata changed with values: %q/%q vs %q/%q", keys, fingerprint, otherKeys, otherFingerprint)
	}
}

func waitForTaskTerminal(t *testing.T, engine *loom.LoomEngine, taskID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, err := engine.Get(taskID)
		if err == nil && task.Status.IsTerminal() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach a terminal state", taskID)
}
