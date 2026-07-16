package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

// finalityRecordingSink is a minimal in-memory EventBatchSink so this test
// does not depend on a SQLite-backed loom engine. It records every persisted
// RuntimeEvent in admission order.
type finalityRecordingSink struct {
	mu     sync.Mutex
	events []workerruntime.RuntimeEvent
}

func (sink *finalityRecordingSink) AppendRuntimeEvents(_ context.Context, batch []workerruntime.RuntimeEvent) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, batch...)
	return nil
}

func (*finalityRecordingSink) Checkpoint(context.Context) error { return nil }

func (sink *finalityRecordingSink) snapshot() []workerruntime.RuntimeEvent {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]workerruntime.RuntimeEvent(nil), sink.events...)
}

// TestProfileTaskWorkerFallbackPublishesExactlyOneFinalTerminalLast covers an
// accepted T018 CR-002 review finding: one task runtime stream may contain
// multiple provider attempts (primary timeout, then fallback success), but
// only one task-level Terminal=true event may be persisted, and it must be
// last. Per-attempt sinks may flush normalizers but must not persist
// Terminal=true for an attempt that is not the task's real final outcome.
func TestProfileTaskWorkerFallbackPublishesExactlyOneFinalTerminalLast(t *testing.T) {
	dir := t.TempDir()
	codexPath := fakeExecutableWithContents(t, dir, "codex-slow",
		"#!/bin/sh\nsleep 5\n",
		"@echo off\r\nping -n 6 127.0.0.1 > nul\r\nexit /b 0\r\n",
	)
	claudePath := fakeExecutableWithContents(t, dir, "claude-fast",
		"#!/bin/sh\nprintf 'fallback output\\n'\n",
		"@echo off\r\necho fallback output\r\nexit /b 0\r\n",
	)

	codex := defaultRecipeProfile()
	codex.Binary = codexPath
	codex.ResolvedPath = codexPath
	codex.OutputFormat = "text"
	codex.TimeoutSeconds = 1

	claude := limitedRecipeProfile()
	claude.Name = "claude"
	claude.Binary = claudePath
	claude.ResolvedPath = claudePath
	claude.OutputFormat = "text"
	claude.TimeoutSeconds = 5

	registry := driver.NewRegistry(map[string]*config.CLIProfile{
		"codex":  codex,
		"claude": claude,
	})
	registry.SetAvailable("codex", true)
	registry.SetAvailable("claude", true)

	const taskID = "finality-task-1"
	engine := newTaskToolEngine(t)
	task := &loom.Task{
		ID:         taskID,
		Status:     loom.TaskStatusRunning,
		WorkerType: loom.WorkerType("finality-probe"),
		ProjectID:  "finality-project",
		TenantID:   "finality-tenant",
		Prompt:     "run the fallback finality check",
		CWD:        dir,
		Timeout:    1,
		CreatedAt:  time.Now().UTC(),
	}
	if err := engine.Import(task); err != nil {
		t.Fatalf("seed finality task: %v", err)
	}

	srv := &Server{cfg: &config.Config{}, registry: registry, loom: engine}
	srv.fallbackPicker = buildFallbackPicker(srv)
	if srv.fallbackPicker == nil {
		t.Fatal("buildFallbackPicker returned nil")
	}

	sink := &finalityRecordingSink{}
	worker := profileTaskWorker{
		server:     srv,
		workerType: loom.WorkerType("finality-probe"),
		taskClass:  "review",
		defaultCLI: "codex",
		newEventWriter: func(string) (*workerruntime.EventWriter, error) {
			return workerruntime.NewEventWriter(workerruntime.DefaultEventWriterConfig(sink))
		},
	}

	result, err := worker.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("result = nil")
	}

	events := sink.snapshot()
	terminalCount := 0
	terminalIdx := -1
	for i, event := range events {
		if event.Terminal {
			terminalCount++
			terminalIdx = i
		}
	}
	if terminalCount != 1 {
		t.Fatalf("terminal event count = %d, want exactly 1 (one task-level terminal, not one per provider attempt); events=%#v", terminalCount, events)
	}
	if terminalIdx != len(events)-1 {
		t.Fatalf("terminal event index = %d, want last index %d; events=%#v", terminalIdx, len(events)-1, events)
	}
}
