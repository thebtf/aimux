package loom

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const terminalSinksSecret = "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const terminalSinksPrivate = "PRIVATE_REASONING_SENTINEL"

func TestTaskStore_FailActiveSanitizesDurableAndLog(t *testing.T) {
	store := newTestStore(t)
	logger := &recordingLogger{}
	engine := New(store, WithLogger(logger))
	task := makeTask("admin-terminal-sink", "terminal-sinks", TaskStatusRunning)
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if ok, err := engine.FailActive(task.ID, "admin failure: "+terminalSinksSecret+" "+terminalSinksPrivate); err != nil || !ok {
		t.Fatalf("FailActive = (%v, %v), want (true, nil)", ok, err)
	}
	stored, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var durable string
	if err := store.db.QueryRow(`SELECT error FROM tasks WHERE id = ?`, task.ID).Scan(&durable); err != nil {
		t.Fatalf("query durable error: %v", err)
	}
	assertTerminalSinksSafe(t, "returned task", stored.Error)
	assertTerminalSinksSafe(t, "durable task", durable)
	if got := terminalSinksLogField(logger, "task failed", "error"); got != stored.Error {
		t.Fatalf("admin failure log error = %q, want canonical durable error %q", got, stored.Error)
	}
	assertTerminalSinksSafe(t, "admin failure log", terminalSinksLogField(logger, "task failed", "error"))
}

func TestSanitizeTerminalResultAmbiguousJSON(t *testing.T) {
	const invalidStructured = "[REDACTED:invalid-structured-terminal-content]"
	for _, tt := range []struct {
		name  string
		input string
		want  string
	}{
		{"duplicate_sensitive_key", `{"credential":"short-secret","credential":"other"}`, invalidStructured},
		{"malformed_sensitive_object", `{"credential":"short-secret",`, invalidStructured},
		{"valid_sensitive_object", `{"credential":"short-secret","provider":"codex"}`, `{"credential":"[REDACTED]","provider":"codex"}`},
		{"valid_safe_object", `{"provider":"codex","exit_code":0}`, `{"provider":"codex","exit_code":0}`},
		{"valid_scalar", `"safe scalar"`, `"safe scalar"`},
		{"safe_plain_text", "safe plain text", "safe plain text"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeTerminalResult(tt.input); got != tt.want {
				t.Fatalf("sanitizeTerminalResult(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoomEngine_SubmitSanitizesGenericWorkerMetadata(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	engine.RegisterWorker(WorkerTypeCLI, &testWorkerWithMetadata{
		wtype:  WorkerTypeCLI,
		result: "safe result",
		metadata: map[string]any{
			"provider":   "codex",
			"exit_code":  json.Number("0"),
			"credential": "short-secret",
			"nested":     map[string]any{"reasoning": terminalSinksPrivate},
		},
	})
	taskID, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "metadata-sinks", Prompt: "test"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	task := waitForTaskStatus(t, store, taskID, TaskStatusCompleted)
	var durable string
	if err := store.db.QueryRow(`SELECT metadata FROM tasks WHERE id = ?`, taskID).Scan(&durable); err != nil {
		t.Fatalf("query durable metadata: %v", err)
	}
	assertTerminalSinksSafe(t, "generic metadata task", stringifyTerminalSinks(task.Metadata))
	assertTerminalSinksSafe(t, "generic metadata durable", durable)
	if task.Metadata["provider"] != "codex" {
		t.Fatalf("provider = %#v, want codex", task.Metadata["provider"])
	}
	if got, ok := task.Metadata["exit_code"].(json.Number); !ok || got.String() != "0" {
		t.Fatalf("exit_code = %#v, want json.Number(0)", task.Metadata["exit_code"])
	}
}

func TestTaskStore_MetadataSinksSanitizeGenericMetadata(t *testing.T) {
	store := newTestStore(t)
	active := makeTask("set-metadata-terminal-sink", "metadata-sinks", TaskStatusRunning)
	if err := store.Create(active); err != nil {
		t.Fatalf("Create: %v", err)
	}
	metadata := map[string]any{"provider": "grok", "attempt": json.Number("9007199254740993"), "token": "short-secret"}
	if err := store.SetMetadata(active.ID, metadata); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	imported := makeTask("import-metadata-terminal-sink", "metadata-sinks", TaskStatusCompleted)
	imported.Metadata = map[string]any{"cli": "codex", "code": "rate_limit", "analysis": terminalSinksPrivate}
	if err := store.Import(imported); err != nil {
		t.Fatalf("Import: %v", err)
	}
	for _, id := range []string{active.ID, imported.ID} {
		task, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		var durable string
		if err := store.db.QueryRow(`SELECT metadata FROM tasks WHERE id = ?`, id).Scan(&durable); err != nil {
			t.Fatalf("query metadata %s: %v", id, err)
		}
		assertTerminalSinksSafe(t, "direct/replay task "+id, stringifyTerminalSinks(task.Metadata))
		assertTerminalSinksSafe(t, "direct/replay durable "+id, durable)
	}
	activeTask, err := store.Get(active.ID)
	if err != nil {
		t.Fatalf("Get active: %v", err)
	}
	if activeTask.Metadata["provider"] != "grok" {
		t.Fatalf("provider = %#v, want grok", activeTask.Metadata["provider"])
	}
	if got, ok := activeTask.Metadata["attempt"].(json.Number); !ok || got.String() != "9007199254740993" {
		t.Fatalf("attempt = %#v, want exact json.Number", activeTask.Metadata["attempt"])
	}
}

func assertTerminalSinksSafe(t *testing.T, sink, value string) {
	t.Helper()
	if strings.Contains(value, terminalSinksSecret) || strings.Contains(value, terminalSinksPrivate) || strings.Contains(value, "short-secret") {
		t.Fatalf("%s leaked terminal data: %q", sink, value)
	}
}

func terminalSinksLogField(logger *recordingLogger, message, key string) string {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	for _, entry := range logger.entries {
		if entry.msg != message {
			continue
		}
		for i := 0; i+1 < len(entry.args); i += 2 {
			if entry.args[i] == key {
				if value, ok := entry.args[i+1].(string); ok {
					return value
				}
			}
		}
	}
	return ""
}

func stringifyTerminalSinks(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "marshal failure"
	}
	return string(encoded)
}
