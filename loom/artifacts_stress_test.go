package loom

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestArtifactStress_ContextCancellationCreatesNoPrefixOrWakeupAndCheckpointCompletes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context-batch.db")
	db, store := openArtifactReplayStore(t, path, "artifact-stress-context")
	const taskID = "artifact-stress-context"
	createArtifactTask(t, store, taskID, "artifact-stress", TaskStatusRunning)
	engine := New(store)
	defer func() { _ = engine.Close(context.Background()) }()

	var wakeups atomic.Int64
	unsubscribe := engine.Events().Subscribe(func(event TaskEvent) {
		if event.TaskID == taskID && event.Type == EventTaskArtifactsAppended {
			wakeups.Add(1)
		}
	})
	defer unsubscribe()

	batch := make([]TaskRuntimeEventAppend, 256)
	for i := range batch {
		batch[i] = TaskRuntimeEventAppend{
			EventType: "text_delta",
			Channel:   "stdout",
			Summary:   "stress",
			Payload:   map[string]any{"ordinal": i},
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.AppendRuntimeEventsContext(cancelled, taskID, batch); !errors.Is(err, context.Canceled) {
		t.Fatalf("AppendRuntimeEventsContext error = %v, want context.Canceled", err)
	}
	var rows int
	if err := db.QueryRow("SELECT count(*) FROM task_artifacts WHERE task_id=?", taskID).Scan(&rows); err != nil {
		t.Fatalf("count cancelled batch: %v", err)
	}
	if rows != 0 || wakeups.Load() != 0 {
		t.Fatalf("cancelled batch durable rows/wakeups = %d/%d, want 0/0", rows, wakeups.Load())
	}

	if _, err := engine.AppendRuntimeEventsContext(context.Background(), taskID, batch); err != nil {
		t.Fatalf("AppendRuntimeEventsContext success: %v", err)
	}
	if wakeups.Load() != 1 {
		t.Fatalf("successful batch wakeups = %d, want 1", wakeups.Load())
	}
	walBefore := artifactStressFileSize(path + "-wal")
	if err := engine.CheckpointWAL(context.Background()); err != nil {
		t.Fatalf("CheckpointWAL: %v", err)
	}
	walAfter := artifactStressFileSize(path + "-wal")
	t.Logf("batch_rows=%d wakeups=%d wal_before_bytes=%d wal_after_bytes=%d", len(batch), wakeups.Load(), walBefore, walAfter)
}

func artifactStressFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
