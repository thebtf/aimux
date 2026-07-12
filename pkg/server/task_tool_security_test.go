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
	"github.com/thebtf/aimux/pkg/executor/code"
	"github.com/thebtf/mcp-mux/muxcore"
)

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

	var envJSON string
	var metadataJSON string
	if err := db.QueryRow("SELECT env, metadata FROM tasks WHERE id = ?", result.TaskID).Scan(&envJSON, &metadataJSON); err != nil {
		t.Fatalf("read durable task row: %v", err)
	}
	if strings.Contains(envJSON, secret) || strings.Contains(metadataJSON, secret) {
		t.Errorf("durable task row leaked raw secret: env=%s metadata=%s", envJSON, metadataJSON)
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
}
