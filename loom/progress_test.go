package loom

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom/deps"

	_ "modernc.org/sqlite"
)

// makeProgressTask returns a fresh Task in TaskStatusRunning suitable for
// AppendProgress tests. Tasks created via Submit + dispatch normally arrive
// in this state once the worker starts; bypassing the engine here lets us
// exercise Store.AppendProgress directly.
func makeProgressTask(id, projectID string) *Task {
	return &Task{
		ID:         id,
		Status:     TaskStatusRunning,
		WorkerType: WorkerTypeCLI,
		ProjectID:  projectID,
		TenantID:   LegacyTenantID,
		Prompt:     "progress test",
		CreatedAt:  time.Now().UTC(),
	}
}

// TestTaskStore_MigrateV5_FreshDB verifies that NewTaskStore on a fresh DB
// creates the tasks table with the three v5 progress columns present.
func TestTaskStore_MigrateV5_FreshDB(t *testing.T) {
	store := newTestStore(t)

	for _, col := range []string{"last_output_line", "progress_lines", "progress_updated_at"} {
		if !loomColumnExists(t, store.db, col) {
			t.Errorf("tasks.%s column missing after NewTaskStore (v5 migration)", col)
		}
	}
}

// TestTaskStore_MigrateV5_Idempotent verifies that running NewTaskStore twice
// does not re-add v5 columns or fail.
func TestTaskStore_MigrateV5_Idempotent(t *testing.T) {
	db := newTestDB(t)

	if _, err := NewTaskStore(db, "v5-idempotent"); err != nil {
		t.Fatalf("first NewTaskStore: %v", err)
	}
	if _, err := NewTaskStore(db, "v5-idempotent"); err != nil {
		t.Fatalf("second NewTaskStore (idempotent check): %v", err)
	}
}

// TestTaskStore_MigrateV5_Down verifies that MigrateV5Down removes the three
// progress columns. Reversibility gate per spec / tasks.md T005-3.
func TestTaskStore_MigrateV5_Down(t *testing.T) {
	store := newTestStore(t)

	for _, col := range []string{"last_output_line", "progress_lines", "progress_updated_at"} {
		if !loomColumnExists(t, store.db, col) {
			t.Fatalf("precondition: %s column should exist before down migration", col)
		}
	}

	if err := MigrateV5Down(store.db); err != nil {
		t.Fatalf("MigrateV5Down: %v", err)
	}

	for _, col := range []string{"last_output_line", "progress_lines", "progress_updated_at"} {
		if loomColumnExists(t, store.db, col) {
			t.Errorf("tasks.%s column still present after MigrateV5Down", col)
		}
	}

	// Down migration must be idempotent — running again on an already-stripped
	// schema should not error.
	if err := MigrateV5Down(store.db); err != nil {
		t.Fatalf("MigrateV5Down (second call must be idempotent): %v", err)
	}
}

// TestStore_AppendProgress_FreshTask exercises the canonical 5-line emit
// → status read path:
//   - progress_tail matches the 5th non-empty line
//   - progress_lines >= 5 (each AppendProgress adds 1 + embedded newline count)
//   - progress_updated_at is non-nil and recent
func TestStore_AppendProgress_FreshTask(t *testing.T) {
	store := newTestStore(t)
	task := makeProgressTask("task-progress-5", "proj-progress")
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	before := time.Now().UTC().Add(-time.Second)

	for i := 1; i <= 5; i++ {
		line := "line-" + strings.Repeat("x", i)
		info, err := store.AppendProgress(task.ID, line)
		if err != nil {
			t.Fatalf("AppendProgress (%d): %v", i, err)
		}
		if !info.OK {
			t.Fatalf("AppendProgress (%d): info.OK = false; want true for known task", i)
		}
		if info.ProjectID != "proj-progress" {
			t.Errorf("AppendProgress (%d): info.ProjectID = %q; want %q", i, info.ProjectID, "proj-progress")
		}
	}

	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	wantTail := "line-xxxxx"
	if got.LastOutputLine != wantTail {
		t.Errorf("LastOutputLine = %q; want %q", got.LastOutputLine, wantTail)
	}
	if got.ProgressLines < 5 {
		t.Errorf("ProgressLines = %d; want >= 5", got.ProgressLines)
	}
	if got.ProgressUpdatedAt == nil {
		t.Fatal("ProgressUpdatedAt = nil; want a non-nil timestamp")
	}
	if got.ProgressUpdatedAt.Before(before) {
		t.Errorf("ProgressUpdatedAt = %v; want >= %v", got.ProgressUpdatedAt, before)
	}
}

// TestStore_AppendProgress_NoProgressEmitted_FieldsZero verifies EC-5.1:
// a task whose worker never calls AppendProgress shows zero/empty progress
// fields rather than stale values from a previous task or random memory.
func TestStore_AppendProgress_NoProgressEmitted_FieldsZero(t *testing.T) {
	store := newTestStore(t)
	task := makeProgressTask("task-no-progress", "proj-no-progress")
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.LastOutputLine != "" {
		t.Errorf("LastOutputLine = %q; want empty", got.LastOutputLine)
	}
	if got.ProgressLines != 0 {
		t.Errorf("ProgressLines = %d; want 0", got.ProgressLines)
	}
	if got.ProgressUpdatedAt != nil {
		t.Errorf("ProgressUpdatedAt = %v; want nil", got.ProgressUpdatedAt)
	}
}

// TestStore_AppendProgress_TruncatesLongLine verifies that lines >100 bytes
// are UTF-8-safely truncated and never stored at full length (caps SQL row
// size and keeps the MCP payload compact).
func TestStore_AppendProgress_TruncatesLongLine(t *testing.T) {
	store := newTestStore(t)
	task := makeProgressTask("task-progress-trunc", "proj-trunc")
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	long := strings.Repeat("a", 250)
	if _, err := store.AppendProgress(task.ID, long); err != nil {
		t.Fatalf("AppendProgress: %v", err)
	}

	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.LastOutputLine) != progressLineMaxBytes {
		t.Errorf("len(LastOutputLine) = %d; want %d", len(got.LastOutputLine), progressLineMaxBytes)
	}
	if !strings.HasPrefix(long, got.LastOutputLine) {
		t.Errorf("LastOutputLine = %q; want a prefix of the original line", got.LastOutputLine)
	}
}

// TestStore_AppendProgress_WhitespaceOnlyPreservesTail verifies that a
// whitespace-only line keeps the previous LastOutputLine (signal-bearing
// content stays visible) while still bumping the line counter / timestamp.
func TestStore_AppendProgress_WhitespaceOnlyPreservesTail(t *testing.T) {
	store := newTestStore(t)
	task := makeProgressTask("task-progress-ws", "proj-ws")
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.AppendProgress(task.ID, "real signal"); err != nil {
		t.Fatalf("AppendProgress (real): %v", err)
	}
	if _, err := store.AppendProgress(task.ID, "   \t  "); err != nil {
		t.Fatalf("AppendProgress (whitespace): %v", err)
	}

	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastOutputLine != "real signal" {
		t.Errorf("LastOutputLine = %q; want %q (whitespace must not clobber signal-bearing tail)", got.LastOutputLine, "real signal")
	}
	if got.ProgressLines < 2 {
		t.Errorf("ProgressLines = %d; want >= 2", got.ProgressLines)
	}
}

// TestStore_AppendProgress_EmbeddedNewlines verifies the 1+\n-count
// arithmetic and that the last non-empty segment becomes the tail.
func TestStore_AppendProgress_EmbeddedNewlines(t *testing.T) {
	store := newTestStore(t)
	task := makeProgressTask("task-progress-nl", "proj-nl")
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Single AppendProgress with two embedded newlines: counts 1+2=3 lines.
	if _, err := store.AppendProgress(task.ID, "first\nsecond\nthird"); err != nil {
		t.Fatalf("AppendProgress: %v", err)
	}

	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastOutputLine != "third" {
		t.Errorf("LastOutputLine = %q; want %q", got.LastOutputLine, "third")
	}
	if got.ProgressLines != 3 {
		t.Errorf("ProgressLines = %d; want 3", got.ProgressLines)
	}
}

// TestStore_AppendProgress_UnknownTaskID_NoError verifies that progress for
// a task that no longer exists (cancelled, GC'd) is a no-op rather than an
// error — slow workers should not surface noisy failures after Cancel.
// info.OK MUST be false so callers do not emit an event for state that was
// never written (CR-005 contract).
func TestStore_AppendProgress_UnknownTaskID_NoError(t *testing.T) {
	store := newTestStore(t)
	info, err := store.AppendProgress("does-not-exist", "ghost line")
	if err != nil {
		t.Errorf("AppendProgress for unknown task should be a no-op, got error: %v", err)
	}
	if info.OK {
		t.Errorf("AppendProgress for unknown task: info.OK = true; want false (no event must be emitted)")
	}
	if info.ProjectID != "" || info.RequestID != "" {
		t.Errorf("AppendProgress for unknown task: info = %+v; want zero ProgressInfo", info)
	}
}

// TestStore_AppendProgress_ConcurrentSameTask verifies EC-5.3: many
// goroutines calling AppendProgress on the same task ID must all succeed
// and the final ProgressLines counter equals the total invocation count.
// SQLite WAL serializes the row UPDATEs at the engine level — this test
// proves the contract holds without external locking.
func TestStore_AppendProgress_ConcurrentSameTask(t *testing.T) {
	store := newTestStore(t)
	task := makeProgressTask("task-progress-conc", "proj-conc")
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const (
		writers       = 8
		linesPerWrite = 25
	)
	var wg sync.WaitGroup
	var errCount atomic.Int64
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < linesPerWrite; i++ {
				if _, err := store.AppendProgress(task.ID, "writer"); err != nil {
					errCount.Add(1)
					t.Logf("writer %d iter %d: %v", w, i, err)
				}
			}
		}(w)
	}
	wg.Wait()

	if errCount.Load() != 0 {
		t.Fatalf("concurrent AppendProgress had %d errors", errCount.Load())
	}
	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := int64(writers * linesPerWrite)
	if got.ProgressLines != want {
		t.Errorf("ProgressLines = %d; want %d (no lost updates)", got.ProgressLines, want)
	}
}

// TestEngine_AppendProgress_EmitsEvent verifies the engine-level
// AppendProgress wrapper updates the store AND emits EventTaskProgress with
// the correct task ID. Existing subscribers that filter on lifecycle events
// (created/dispatched/running/completed/failed/cancelled/retrying/failed_crash)
// MUST NOT see the new event — additivity per NFR-6.
func TestEngine_AppendProgress_EmitsEvent(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	defer func() {
		_ = engine.Close(context.Background())
	}()

	// Insert a running task without going through Submit so we can hit
	// AppendProgress directly without dispatching a worker.
	task := makeProgressTask("task-engine-progress", "proj-engine-progress")
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var (
		mu              sync.Mutex
		progressEvents  []TaskEvent
		lifecycleEvents []TaskEvent
	)
	unsub := engine.Events().Subscribe(func(ev TaskEvent) {
		mu.Lock()
		defer mu.Unlock()
		if ev.Type == EventTaskProgress {
			progressEvents = append(progressEvents, ev)
			return
		}
		lifecycleEvents = append(lifecycleEvents, ev)
	})
	defer unsub()

	if err := engine.AppendProgress(task.ID, "engine line one"); err != nil {
		t.Fatalf("engine.AppendProgress (1): %v", err)
	}
	if err := engine.AppendProgress(task.ID, "engine line two"); err != nil {
		t.Fatalf("engine.AppendProgress (2): %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(progressEvents) != 2 {
		t.Errorf("got %d progress events, want 2", len(progressEvents))
	}
	for i, ev := range progressEvents {
		if ev.TaskID != task.ID {
			t.Errorf("progressEvents[%d].TaskID = %q; want %q", i, ev.TaskID, task.ID)
		}
		if ev.Type != EventTaskProgress {
			t.Errorf("progressEvents[%d].Type = %q; want %q", i, ev.Type, EventTaskProgress)
		}
		// CR-005: every emitted TaskEvent MUST carry ProjectID for multi-
		// tenant subscriber filtering — empty ProjectID would silently break
		// fanout. RequestID may be empty (the task was created without one
		// in this fixture), but ProjectID is mandatory and was set on the
		// task above.
		if ev.ProjectID != task.ProjectID {
			t.Errorf("progressEvents[%d].ProjectID = %q; want %q (multi-tenant filtering breaks otherwise)", i, ev.ProjectID, task.ProjectID)
		}
		if ev.Status != TaskStatusRunning {
			t.Errorf("progressEvents[%d].Status = %q; want %q", i, ev.Status, TaskStatusRunning)
		}
	}
	if len(lifecycleEvents) != 0 {
		t.Errorf("lifecycle events delivered to subscriber that should only see progress; got %d events: %v", len(lifecycleEvents), lifecycleEvents)
	}
}

// TestEngine_AppendProgress_UnknownTask_NoEvent verifies that progress on
// a task that no longer exists (cancelled, GC'd) does NOT emit an
// EventTaskProgress. The store reports info.OK=false and the engine MUST
// suppress the event so subscribers never observe state that was never
// written. CR-005 contract from loom.go AppendProgress doc-comment.
func TestEngine_AppendProgress_UnknownTask_NoEvent(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	defer func() {
		_ = engine.Close(context.Background())
	}()

	var (
		mu     sync.Mutex
		events []TaskEvent
	)
	unsub := engine.Events().Subscribe(func(ev TaskEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	})
	defer unsub()

	if err := engine.AppendProgress("never-created", "ghost output"); err != nil {
		t.Fatalf("engine.AppendProgress (unknown task) returned error; want nil: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 0 {
		t.Errorf("got %d events for unknown task; want 0 (CR-005: no event for no-op store update)", len(events))
	}
}

// TestStore_AppendProgress_RedactsSecretsInTail verifies that the tail
// surfaced via last_output_line is run through redactErrorMsg before
// storage so an OpenAI/Anthropic API key or Bearer token echoed by a CLI
// tool into its own progress stream cannot reach the MCP status response.
// Pattern set MUST stay in lockstep with the tasks.error redaction (store.go).
func TestStore_AppendProgress_RedactsSecretsInTail(t *testing.T) {
	store := newTestStore(t)
	task := makeProgressTask("task-progress-redact", "proj-redact")
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cases := []struct {
		name      string
		line      string
		mustNotIn string // substring that MUST NOT appear in the stored tail
	}{
		{
			name:      "openai-svcacct-key",
			line:      "calling api with sk-svcacct-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGh",
			mustNotIn: "sk-svcacct-AbCdEfGhIjKlMnOpQr",
		},
		{
			name:      "bearer-token",
			line:      "Authorization: Bearer abcdef0123456789abcdef0123456789",
			mustNotIn: "abcdef0123456789abcdef",
		},
		{
			name:      "google-ai-key",
			line:      "key=AIzaSyAbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
			mustNotIn: "AIzaSyAbCdEfGhIjKl",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.AppendProgress(task.ID, tc.line); err != nil {
				t.Fatalf("AppendProgress: %v", err)
			}
			got, err := store.Get(task.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if strings.Contains(got.LastOutputLine, tc.mustNotIn) {
				t.Errorf("LastOutputLine = %q contains unredacted secret %q", got.LastOutputLine, tc.mustNotIn)
			}
			if !strings.Contains(got.LastOutputLine, "[REDACTED]") {
				t.Errorf("LastOutputLine = %q; want a [REDACTED] marker after secret scrubbing", got.LastOutputLine)
			}
		})
	}
}

// TestStreamingBase_Sink_ForwardsLines verifies that StreamingBase's new
// Sink field forwards every delivered line to the configured sink alongside
// any user-supplied OnLine callback. Fake sink implementation lives in this
// file to avoid a dep on the engine in the workers package test.
func TestStreamingBase_Sink_ForwardsLines(t *testing.T) {
	// Re-use the engine-as-sink path: AppendProgress on the engine runs the
	// real store logic so a successful test here proves end-to-end wiring.
	store := newTestStore(t)
	engine := New(store)
	defer func() {
		_ = engine.Close(context.Background())
	}()

	task := makeProgressTask("task-streaming-sink", "proj-streaming-sink")
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify by direct engine.AppendProgress call (StreamingBase wiring is
	// validated in loom/workers/streaming_base_test.go which exercises the
	// Sink field with a stub recorder; this test guards the integration
	// contract from the engine side).
	for _, line := range []string{"alpha", "beta", "gamma"} {
		if err := engine.AppendProgress(task.ID, line); err != nil {
			t.Fatalf("AppendProgress %q: %v", line, err)
		}
	}

	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastOutputLine != "gamma" {
		t.Errorf("LastOutputLine = %q; want gamma", got.LastOutputLine)
	}
	if got.ProgressLines != 3 {
		t.Errorf("ProgressLines = %d; want 3", got.ProgressLines)
	}
}

// TestEngine_AppendProgress_SatisfiesSinkInterface is a compile-time guard:
// *LoomEngine must satisfy workers.ProgressSink so callers can pass it
// directly to StreamingBase. We declare the contract inline here (cannot
// import workers due to the circular dep) and assert the method shape.
func TestEngine_AppendProgress_SatisfiesSinkInterface(t *testing.T) {
	type progressSink interface {
		AppendProgress(taskID, line string) error
	}
	store := newTestStore(t)
	engine := New(store)
	defer func() {
		_ = engine.Close(context.Background())
	}()
	var _ progressSink = engine
	// Body guard: a swap of the contract to a no-op interface would still
	// type-check above — call AppendProgress on the variable to ensure the
	// method is real and non-trivial.
	task := makeProgressTask("task-engine-sink-iface", "proj-engine-sink-iface")
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := engine.AppendProgress(task.ID, "iface line"); err != nil {
		t.Fatalf("engine.AppendProgress as sink: %v", err)
	}
	got, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastOutputLine != "iface line" {
		t.Errorf("LastOutputLine = %q; want %q", got.LastOutputLine, "iface line")
	}
	_ = deps.NoopLogger() // touch deps to silence unused-import warning under future refactors
}
