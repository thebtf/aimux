package loom

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func createArtifactTask(t *testing.T, store *TaskStore, id, projectID string, status TaskStatus) {
	t.Helper()
	task := &Task{
		ID:         id,
		Status:     status,
		WorkerType: WorkerTypeCLI,
		ProjectID:  projectID,
		TenantID:   LegacyTenantID,
		Prompt:     "artifact prompt",
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
}

func TestTaskStore_AppendArtifact_AssignsDistinctSeqAndPreservesTaskStatus(t *testing.T) {
	store := newTestStore(t)
	createArtifactTask(t, store, "task-artifact-a", "proj-artifact", TaskStatusRunning)
	createArtifactTask(t, store, "task-artifact-b", "proj-artifact", TaskStatusFailed)

	first, err := store.AppendArtifact("task-artifact-a", TaskArtifactAppend{
		Kind:      TaskArtifactKindLifecycle,
		EventType: string(EventTaskRunning),
		Summary:   "task entered running",
		Payload:   map[string]any{"status": string(TaskStatusRunning)},
	})
	if err != nil {
		t.Fatalf("AppendArtifact(first): %v", err)
	}
	second, err := store.AppendArtifact("task-artifact-b", TaskArtifactAppend{
		Kind:      TaskArtifactKindTerminal,
		EventType: string(EventTaskFailed),
		Summary:   "task failed",
		Payload:   map[string]any{"status": string(TaskStatusFailed), "error_class": "worker_error"},
	})
	if err != nil {
		t.Fatalf("AppendArtifact(second): %v", err)
	}
	third, err := store.AppendArtifact("task-artifact-a", TaskArtifactAppend{
		Kind:          TaskArtifactKindProgress,
		EventType:     string(EventTaskProgress),
		Summary:       "progress line",
		Payload:       map[string]any{"line": "progress line"},
		ContentLength: 13,
		Redacted:      false,
		Truncated:     false,
	})
	if err != nil {
		t.Fatalf("AppendArtifact(third): %v", err)
	}

	if first.Seq == 0 || second.Seq == 0 || third.Seq == 0 {
		t.Fatalf("artifact seq values must be non-zero: first=%d second=%d third=%d", first.Seq, second.Seq, third.Seq)
	}
	if first.Seq == second.Seq || first.Seq == third.Seq || second.Seq == third.Seq {
		t.Fatalf("artifact seq values must be distinct: first=%d second=%d third=%d", first.Seq, second.Seq, third.Seq)
	}
	if first.TaskID != "task-artifact-a" || second.TaskID != "task-artifact-b" || third.TaskID != "task-artifact-a" {
		t.Fatalf("artifact task IDs not preserved: first=%q second=%q third=%q", first.TaskID, second.TaskID, third.TaskID)
	}

	gotA, err := store.Get("task-artifact-a")
	if err != nil {
		t.Fatalf("Get(task-artifact-a): %v", err)
	}
	if gotA.Status != TaskStatusRunning {
		t.Fatalf("artifact append changed task-artifact-a status: got %q want %q", gotA.Status, TaskStatusRunning)
	}
	gotB, err := store.Get("task-artifact-b")
	if err != nil {
		t.Fatalf("Get(task-artifact-b): %v", err)
	}
	if gotB.Status != TaskStatusFailed {
		t.Fatalf("artifact append changed task-artifact-b status: got %q want %q", gotB.Status, TaskStatusFailed)
	}
}

func TestTaskStore_ListArtifacts_CursorPagesAreTaskScoped(t *testing.T) {
	store := newTestStore(t)
	createArtifactTask(t, store, "task-page-a", "proj-page", TaskStatusRunning)
	createArtifactTask(t, store, "task-page-b", "proj-page", TaskStatusRunning)

	first, err := store.AppendArtifact("task-page-a", TaskArtifactAppend{
		Kind:      TaskArtifactKindLifecycle,
		EventType: string(EventTaskRunning),
		Summary:   "first task-page-a artifact",
		Payload:   map[string]any{"ordinal": "first"},
	})
	if err != nil {
		t.Fatalf("AppendArtifact(first): %v", err)
	}
	foreign, err := store.AppendArtifact("task-page-b", TaskArtifactAppend{
		Kind:      TaskArtifactKindLifecycle,
		EventType: string(EventTaskRunning),
		Summary:   "task-page-b artifact",
		Payload:   map[string]any{"ordinal": "foreign"},
	})
	if err != nil {
		t.Fatalf("AppendArtifact(foreign): %v", err)
	}
	second, err := store.AppendArtifact("task-page-a", TaskArtifactAppend{
		Kind:      TaskArtifactKindProgress,
		EventType: string(EventTaskProgress),
		Summary:   "second task-page-a artifact",
		Payload:   map[string]any{"ordinal": "second"},
	})
	if err != nil {
		t.Fatalf("AppendArtifact(second): %v", err)
	}

	page1, err := store.ListArtifacts("task-page-a", TaskArtifactListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("ListArtifacts page1: %v", err)
	}
	if len(page1.Items) != 1 {
		t.Fatalf("page1 len = %d; want 1", len(page1.Items))
	}
	if page1.Items[0].Seq != first.Seq {
		t.Fatalf("page1 first seq = %d; want first task-page-a seq %d", page1.Items[0].Seq, first.Seq)
	}
	if page1.Items[0].Seq == foreign.Seq {
		t.Fatalf("page1 leaked foreign task artifact seq %d", foreign.Seq)
	}
	if !page1.HasMore {
		t.Fatalf("page1 HasMore = false; want true")
	}
	if page1.NextCursor == "" {
		t.Fatalf("page1 NextCursor empty; want continuation cursor")
	}

	page2, err := store.ListArtifacts("task-page-a", TaskArtifactListOptions{
		Cursor: page1.NextCursor,
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("ListArtifacts page2: %v", err)
	}
	if len(page2.Items) != 1 {
		t.Fatalf("page2 len = %d; want 1", len(page2.Items))
	}
	if page2.Items[0].Seq != second.Seq {
		t.Fatalf("page2 first seq = %d; want second task-page-a seq %d", page2.Items[0].Seq, second.Seq)
	}
	if page2.Items[0].Seq == page1.Items[0].Seq {
		t.Fatalf("page2 duplicated page1 seq %d", page2.Items[0].Seq)
	}
	if page2.HasMore {
		t.Fatalf("page2 HasMore = true; want false")
	}
}

func TestTaskStore_AppendRuntimeEvent_FiltersByEventTypeChannelAndKeepsOrderedCursor(t *testing.T) {
	store := newTestStore(t)
	createArtifactTask(t, store, "task-runtime-events", "proj-runtime-events", TaskStatusRunning)

	first, err := store.AppendRuntimeEvent("task-runtime-events", TaskRuntimeEventAppend{
		EventType: "text_delta",
		Channel:   "stdout",
		Summary:   "first delta with sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
		Payload: map[string]any{
			"text": "hello sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789",
		},
	})
	if err != nil {
		t.Fatalf("AppendRuntimeEvent(first): %v", err)
	}
	status, err := store.AppendRuntimeEvent("task-runtime-events", TaskRuntimeEventAppend{
		EventType: "status",
		Channel:   "stderr",
		Summary:   "entered model dispatch",
		Payload:   map[string]any{"state": "dispatching"},
	})
	if err != nil {
		t.Fatalf("AppendRuntimeEvent(status): %v", err)
	}
	second, err := store.AppendRuntimeEvent("task-runtime-events", TaskRuntimeEventAppend{
		EventType: "text_delta",
		Channel:   "stdout",
		Summary:   "second delta",
		Payload:   map[string]any{"text": " world"},
	})
	if err != nil {
		t.Fatalf("AppendRuntimeEvent(second): %v", err)
	}

	if first.Kind != TaskArtifactKindRuntime || first.EventType != "text_delta" || first.Channel != "stdout" {
		t.Fatalf("first runtime identity = kind %q event_type %q channel %q", first.Kind, first.EventType, first.Channel)
	}
	if status.Kind != TaskArtifactKindRuntime || status.EventType != "status" || status.Channel != "stderr" {
		t.Fatalf("status runtime identity = kind %q event_type %q channel %q", status.Kind, status.EventType, status.Channel)
	}
	if !(first.Seq < status.Seq && status.Seq < second.Seq) {
		t.Fatalf("runtime event seq order = first %d status %d second %d; want append order", first.Seq, status.Seq, second.Seq)
	}
	if strings.Contains(first.Summary, "sk-proj-") || !first.Redacted {
		t.Fatalf("runtime summary redaction failed: summary=%q redacted=%v", first.Summary, first.Redacted)
	}
	payloadRaw, err := json.Marshal(first.Payload)
	if err != nil {
		t.Fatalf("marshal first payload: %v", err)
	}
	if strings.Contains(string(payloadRaw), "sk-proj-") {
		t.Fatalf("runtime payload leaked raw secret: %s", payloadRaw)
	}

	page1, err := store.ListArtifacts("task-runtime-events", TaskArtifactListOptions{
		Limit:      1,
		Kinds:      []TaskArtifactKind{TaskArtifactKindRuntime},
		EventTypes: []string{"text_delta"},
		Channels:   []string{"stdout"},
	})
	if err != nil {
		t.Fatalf("ListArtifacts page1: %v", err)
	}
	if len(page1.Items) != 1 || page1.Items[0].Seq != first.Seq {
		t.Fatalf("page1 items = %#v; want first text_delta stdout seq %d", page1.Items, first.Seq)
	}
	if !page1.HasMore || page1.NextCursor == "" {
		t.Fatalf("page1 cursor metadata = has_more %v next_cursor %q; want more text_delta stdout rows", page1.HasMore, page1.NextCursor)
	}

	page2, err := store.ListArtifacts("task-runtime-events", TaskArtifactListOptions{
		Cursor:     page1.NextCursor,
		Limit:      1,
		Kinds:      []TaskArtifactKind{TaskArtifactKindRuntime},
		EventTypes: []string{"text_delta"},
		Channels:   []string{"stdout"},
	})
	if err != nil {
		t.Fatalf("ListArtifacts page2: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].Seq != second.Seq {
		t.Fatalf("page2 items = %#v; want second text_delta stdout seq %d", page2.Items, second.Seq)
	}
	if page2.HasMore {
		t.Fatalf("page2 HasMore = true; want exhausted filtered cursor")
	}

	stderrPage, err := store.ListArtifacts("task-runtime-events", TaskArtifactListOptions{
		Kinds:      []TaskArtifactKind{TaskArtifactKindRuntime},
		EventTypes: []string{"status"},
		Channels:   []string{"stderr"},
	})
	if err != nil {
		t.Fatalf("ListArtifacts stderr: %v", err)
	}
	if len(stderrPage.Items) != 1 || stderrPage.Items[0].Seq != status.Seq {
		t.Fatalf("stderr filtered items = %#v; want status seq %d", stderrPage.Items, status.Seq)
	}
}

func TestTaskArtifactProjectionStatusForTask_RunningTaskWithEmptyRuntimeProjectionIsPartial(t *testing.T) {
	store := newTestStore(t)
	createArtifactTask(t, store, "task-empty-runtime-projection", "proj-empty-runtime-projection", TaskStatusRunning)

	task, err := store.Get("task-empty-runtime-projection")
	if err != nil {
		t.Fatalf("Get canonical task: %v", err)
	}
	page, err := store.ListArtifacts("task-empty-runtime-projection", TaskArtifactListOptions{
		Kinds: []TaskArtifactKind{TaskArtifactKindRuntime},
	})
	if err != nil {
		t.Fatalf("ListArtifacts runtime: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("runtime projection precondition failed: got %d artifacts, want 0", len(page.Items))
	}
	if got := TaskArtifactProjectionStatusForTask(task, page); got != TaskArtifactProjectionPartial {
		t.Fatalf("projection status = %q; want %q for running task with no runtime event rows", got, TaskArtifactProjectionPartial)
	}
	if task.Status != TaskStatusRunning {
		t.Fatalf("canonical task status changed: got %q want %q", task.Status, TaskStatusRunning)
	}
}

func TestTaskStore_ListArtifacts_MalformedCursorReturnsTypedError(t *testing.T) {
	store := newTestStore(t)
	createArtifactTask(t, store, "task-bad-cursor", "proj-cursor", TaskStatusRunning)

	_, err := store.ListArtifacts("task-bad-cursor", TaskArtifactListOptions{
		Cursor: "not-a-sequence-cursor",
		Limit:  1,
	})
	if !errors.Is(err, ErrInvalidArtifactCursor) {
		t.Fatalf("ListArtifacts malformed cursor error = %v; want ErrInvalidArtifactCursor", err)
	}
}

func TestTaskStore_AppendArtifact_BoundsSummaryAndMarksTruncated(t *testing.T) {
	store := newTestStore(t)
	createArtifactTask(t, store, "task-summary-bound", "proj-summary", TaskStatusRunning)

	longSummary := strings.Repeat("s", artifactSummaryMaxBytes+50)
	artifact, err := store.AppendArtifact("task-summary-bound", TaskArtifactAppend{
		Kind:      TaskArtifactKindProgress,
		EventType: string(EventTaskProgress),
		Summary:   longSummary,
		Payload:   map[string]any{"line": "bounded"},
	})
	if err != nil {
		t.Fatalf("AppendArtifact: %v", err)
	}

	if len(artifact.Summary) > artifactSummaryMaxBytes {
		t.Fatalf("artifact summary len = %d; want <= %d", len(artifact.Summary), artifactSummaryMaxBytes)
	}
	if !artifact.Truncated {
		t.Fatalf("artifact.Truncated = false; want true for bounded long summary")
	}
}

func TestLoomEngine_ArtifactLifecycle_DistinctPromptsProduceDistinctArtifacts(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	defer func() {
		_ = engine.Close(context.Background())
	}()
	engine.RegisterWorker(WorkerTypeCLI, &testWorker{wtype: WorkerTypeCLI, result: "lifecycle-ok"})

	firstID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-artifact-lifecycle",
		Prompt:     "first distinct prompt",
	})
	if err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	secondID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-artifact-lifecycle",
		Prompt:     "second distinct prompt",
	})
	if err != nil {
		t.Fatalf("Submit second: %v", err)
	}

	waitForTaskStatus(t, store, firstID, TaskStatusCompleted)
	waitForTaskStatus(t, store, secondID, TaskStatusCompleted)

	firstPage, err := store.ListArtifacts(firstID, TaskArtifactListOptions{
		Kinds: []TaskArtifactKind{TaskArtifactKindLifecycle, TaskArtifactKindTerminal},
	})
	if err != nil {
		t.Fatalf("ListArtifacts first: %v", err)
	}
	secondPage, err := store.ListArtifacts(secondID, TaskArtifactListOptions{
		Kinds: []TaskArtifactKind{TaskArtifactKindLifecycle, TaskArtifactKindTerminal},
	})
	if err != nil {
		t.Fatalf("ListArtifacts second: %v", err)
	}
	if len(firstPage.Items) == 0 {
		t.Fatalf("first task artifacts missing")
	}
	if len(secondPage.Items) == 0 {
		t.Fatalf("second task artifacts missing")
	}
	if firstPage.Items[0].TaskID != firstID {
		t.Fatalf("first artifact TaskID = %q; want %q", firstPage.Items[0].TaskID, firstID)
	}
	if secondPage.Items[0].TaskID != secondID {
		t.Fatalf("second artifact TaskID = %q; want %q", secondPage.Items[0].TaskID, secondID)
	}
	if firstPage.Items[0].Seq == secondPage.Items[0].Seq {
		t.Fatalf("distinct task prompts produced duplicate artifact identity seq=%d", firstPage.Items[0].Seq)
	}
}

func TestLoomEngine_ArtifactTerminal_FailedTaskKeepsSanitizedDetailOnlyInTaskRow(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	defer func() {
		_ = engine.Close(context.Background())
	}()
	engine.RegisterWorker(WorkerTypeCLI, &artifactProgressFailWorker{engine: engine})

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-artifact-failed",
		Prompt:     "fail after progress",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	task := waitForTaskStatus(t, store, taskID, TaskStatusFailed)
	if task.LastOutputLine == "" {
		t.Fatalf("failed task LastOutputLine empty; want stored progress metadata")
	}
	if strings.Contains(task.LastOutputLine, "sk-svcacct-") {
		t.Fatalf("failed task LastOutputLine leaked raw secret: %q", task.LastOutputLine)
	}
	if task.Error != "worker failed with [REDACTED]" {
		t.Fatalf("failed task Error = %q; want sanitized task-row detail", task.Error)
	}

	terminalPage, err := store.ListArtifacts(taskID, TaskArtifactListOptions{
		Kinds: []TaskArtifactKind{TaskArtifactKindTerminal},
	})
	if err != nil {
		t.Fatalf("ListArtifacts terminal: %v", err)
	}
	if len(terminalPage.Items) != 1 {
		t.Fatalf("failed task terminal artifacts = %d; want exactly 1", len(terminalPage.Items))
	}
	terminal := terminalPage.Items[0]
	if terminal.Kind != TaskArtifactKindTerminal {
		t.Fatalf("terminal artifact kind = %q; want %q", terminal.Kind, TaskArtifactKindTerminal)
	}
	if terminal.EventType != string(EventTaskFailed) {
		t.Fatalf("terminal artifact event = %q; want %q", terminal.EventType, EventTaskFailed)
	}
	if len(terminal.Payload) != 3 ||
		terminal.Payload["status"] != string(TaskStatusFailed) ||
		terminal.Payload["error_code"] != "task_failed" ||
		terminal.Payload["closed_action_count"] != float64(0) {
		t.Fatalf("terminal artifact payload = %#v; want exact canonical task.failed payload", terminal.Payload)
	}
	if strings.Contains(terminal.Summary, "Bearer abcdef") {
		t.Fatalf("terminal artifact summary leaked bearer token: %q", terminal.Summary)
	}

	got, err := store.Get(taskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != TaskStatusFailed {
		t.Fatalf("artifact terminal write changed status: got %q want %q", got.Status, TaskStatusFailed)
	}
}

func TestLoomEngine_ArtifactTerminal_NoWorkerWritesCanonicalFailureArtifact(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	defer func() {
		_ = engine.Close(context.Background())
	}()

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeInvestigator,
		ProjectID:  "proj-no-worker-artifact",
		Prompt:     "missing worker",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitForTaskStatus(t, store, taskID, TaskStatusFailed)

	page, err := store.ListArtifacts(taskID, TaskArtifactListOptions{Kinds: []TaskArtifactKind{TaskArtifactKindTerminal}})
	if err != nil {
		t.Fatalf("ListArtifacts terminal: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("no-worker terminal artifacts = %d; want exactly 1", len(page.Items))
	}
	terminal := page.Items[0]
	if terminal.Kind != TaskArtifactKindTerminal || terminal.EventType != string(EventTaskFailed) {
		t.Fatalf("no-worker terminal artifact kind/event = %q/%q; want %q/%q", terminal.Kind, terminal.EventType, TaskArtifactKindTerminal, EventTaskFailed)
	}
	if len(terminal.Payload) != 3 ||
		terminal.Payload["status"] != string(TaskStatusFailed) ||
		terminal.Payload["error_code"] != "task_failed" ||
		terminal.Payload["closed_action_count"] != float64(0) {
		t.Fatalf("no-worker terminal payload = %#v; want exact canonical task.failed payload", terminal.Payload)
	}
}

func TestTaskArtifactProjectionStatusForTask_EmptyProjectionIsPartialEvidence(t *testing.T) {
	store := newTestStore(t)
	createArtifactTask(t, store, "task-empty-projection", "proj-empty-projection", TaskStatusCompleted)

	task, err := store.Get("task-empty-projection")
	if err != nil {
		t.Fatalf("Get canonical task: %v", err)
	}
	page, err := store.ListArtifacts("task-empty-projection", TaskArtifactListOptions{})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("projection precondition failed: got %d artifacts, want 0", len(page.Items))
	}
	if got := TaskArtifactProjectionStatusForTask(task, page); got != TaskArtifactProjectionPartial {
		t.Fatalf("projection status = %q; want %q for existing task with missing projection rows", got, TaskArtifactProjectionPartial)
	}
	if task.Status != TaskStatusCompleted {
		t.Fatalf("canonical task status changed: got %q want %q", task.Status, TaskStatusCompleted)
	}
}

type artifactProgressFailWorker struct {
	engine *LoomEngine
}

func (w *artifactProgressFailWorker) Execute(_ context.Context, task *Task) (*WorkerResult, error) {
	if err := w.engine.AppendProgress(task.ID, "progress with sk-svcacct-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789AbCdEfGh"); err != nil {
		return nil, err
	}
	return nil, errors.New("worker failed with Bearer abcdef0123456789abcdef0123456789")
}

func (w *artifactProgressFailWorker) Type() WorkerType {
	return WorkerTypeCLI
}

func TestTaskStore_EventLedger_TaskLocalSeqOpaquePagingAndLegacyCursor(t *testing.T) {
	store := newTestStore(t)
	createArtifactTask(t, store, "t010-ledger-a", "t010-ledger", TaskStatusRunning)
	createArtifactTask(t, store, "t010-ledger-b", "t010-ledger", TaskStatusRunning)
	createArtifactTask(t, store, "t010-ledger-old-writer", "t010-ledger", TaskStatusRunning)

	appendEvent := func(taskID, label string) TaskArtifact {
		t.Helper()
		artifacts, err := t010AppendStoreRuntimeEvents(t, store, taskID, []TaskRuntimeEventAppend{{
			EventType: "text_delta",
			Channel:   "stdout",
			Summary:   label,
			Payload:   map[string]any{"label": label},
		}})
		if err != nil {
			t.Fatalf("AppendRuntimeEvents(%s,%s): %v", taskID, label, err)
		}
		if len(artifacts) != 1 {
			t.Fatalf("AppendRuntimeEvents(%s,%s) returned %d artifacts want 1", taskID, label, len(artifacts))
		}
		return artifacts[0]
	}
	a1 := appendEvent("t010-ledger-a", "A1")
	b1 := appendEvent("t010-ledger-b", "B1")
	a2 := appendEvent("t010-ledger-a", "A2")
	b2 := appendEvent("t010-ledger-b", "B2")
	a3 := appendEvent("t010-ledger-a", "A3")

	if got := []int64{t010ArtifactEventSeq(t, a1), t010ArtifactEventSeq(t, a2), t010ArtifactEventSeq(t, a3)}; got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("task A event_seq=%v want [1 2 3]", got)
	}
	if got := []int64{t010ArtifactEventSeq(t, b1), t010ArtifactEventSeq(t, b2)}; got[0] != 1 || got[1] != 2 {
		t.Fatalf("task B event_seq=%v want [1 2]", got)
	}
	if !(a1.Seq < b1.Seq && b1.Seq < a2.Seq && a2.Seq < b2.Seq && b2.Seq < a3.Seq) {
		t.Fatalf("global seq order changed: A1=%d B1=%d A2=%d B2=%d A3=%d", a1.Seq, b1.Seq, a2.Seq, b2.Seq, a3.Seq)
	}
	if a2.Seq == a1.Seq+1 || a3.Seq == a2.Seq+1 || b2.Seq == b1.Seq+1 {
		t.Fatalf("task-local rows lost global inter-task gaps: A=[%d %d %d] B=[%d %d]", a1.Seq, a2.Seq, a3.Seq, b1.Seq, b2.Seq)
	}

	page1, err := store.ListArtifacts("t010-ledger-a", TaskArtifactListOptions{Limit: 2})
	if err != nil {
		t.Fatalf("ListArtifacts page1: %v", err)
	}
	if len(page1.Items) != 2 || page1.Items[0].Seq != a1.Seq || page1.Items[1].Seq != a2.Seq {
		t.Fatalf("page1 items=%#v want A1/A2", page1.Items)
	}
	if t010ArtifactEventSeq(t, page1.Items[0]) != 1 || t010ArtifactEventSeq(t, page1.Items[1]) != 2 || !page1.HasMore || page1.NextCursor == "" {
		t.Fatalf("page1 local sequence/cursor=%#v", page1)
	}
	opaqueCursor := page1.NextCursor
	page2, err := store.ListArtifacts("t010-ledger-a", TaskArtifactListOptions{Cursor: opaqueCursor, Limit: 2})
	if err != nil {
		t.Fatalf("ListArtifacts page2: %v", err)
	}
	if page2.Cursor != opaqueCursor || len(page2.Items) != 1 || page2.Items[0].Seq != a3.Seq || t010ArtifactEventSeq(t, page2.Items[0]) != 3 || page2.HasMore {
		t.Fatalf("page2=%#v want exactly A3 through opaque cursor", page2)
	}
	for _, page := range []TaskArtifactPage{page1, page2} {
		for _, artifact := range page.Items {
			if artifact.TaskID != "t010-ledger-a" || artifact.Seq == b1.Seq || artifact.Seq == b2.Seq {
				t.Fatalf("task A page disclosed foreign artifact: %#v", artifact)
			}
		}
	}

	legacyCursor := strconv.FormatInt(a1.Seq, 10)
	legacyPage, err := store.ListArtifacts("t010-ledger-a", TaskArtifactListOptions{Cursor: legacyCursor, Limit: 10})
	if err != nil {
		t.Fatalf("ListArtifacts legacy cursor: %v", err)
	}
	if legacyPage.Cursor != legacyCursor || len(legacyPage.Items) != 2 ||
		legacyPage.Items[0].Seq != a2.Seq || legacyPage.Items[1].Seq != a3.Seq {
		t.Fatalf("legacy global cursor page=%#v want A2/A3", legacyPage)
	}
	for _, artifact := range legacyPage.Items {
		if artifact.TaskID != "t010-ledger-a" || artifact.Seq == b1.Seq || artifact.Seq == b2.Seq {
			t.Fatalf("legacy task A page disclosed foreign artifact: %#v", artifact)
		}
	}

	const legacyAuthorityInsert = "INSERT INTO task_artifacts(task_id,kind,event_type,channel,summary,payload_json,content_length,redacted,truncated,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)"
	legacyRows := []struct {
		eventType string
		payload   string
		createdAt time.Time
	}{
		{eventType: "legacy.first", payload: `{"ordinal":1}`, createdAt: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)},
		{eventType: "legacy.second", payload: `{"ordinal":2}`, createdAt: time.Date(2030, 1, 2, 3, 4, 6, 0, time.UTC)},
	}
	legacyTx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin legacy authority transaction: %v", err)
	}
	t.Cleanup(func() {
		_ = legacyTx.Rollback()
	})
	for _, row := range legacyRows {
		if _, err := legacyTx.Exec(
			legacyAuthorityInsert,
			"t010-ledger-old-writer",
			string(TaskArtifactKindRuntime),
			row.eventType,
			"",
			row.eventType,
			row.payload,
			len(row.payload),
			0,
			0,
			row.createdAt,
		); err != nil {
			t.Fatalf("legacy authority artifact insert %q: %v", row.eventType, err)
		}
	}
	transactionRows, err := legacyTx.Query(
		`SELECT event_seq FROM task_artifacts WHERE task_id=? ORDER BY seq`,
		"t010-ledger-old-writer",
	)
	if err != nil {
		t.Fatalf("query legacy event_seq inside authority transaction: %v", err)
	}
	var transactionEventSeqs []int64
	for transactionRows.Next() {
		var eventSeq int64
		if err := transactionRows.Scan(&eventSeq); err != nil {
			_ = transactionRows.Close()
			t.Fatalf("scan legacy event_seq inside authority transaction: %v", err)
		}
		transactionEventSeqs = append(transactionEventSeqs, eventSeq)
	}
	if err := transactionRows.Err(); err != nil {
		_ = transactionRows.Close()
		t.Fatalf("iterate legacy event_seq inside authority transaction: %v", err)
	}
	if err := transactionRows.Close(); err != nil {
		t.Fatalf("close legacy event_seq rows inside authority transaction: %v", err)
	}
	if len(transactionEventSeqs) != 2 || transactionEventSeqs[0] != 1 || transactionEventSeqs[1] != 2 {
		t.Fatalf("legacy event_seq inside authority transaction=%v want=[1 2]", transactionEventSeqs)
	}
	if err := legacyTx.Commit(); err != nil {
		t.Fatalf("commit legacy authority transaction: %v", err)
	}
	oldWriterPage, err := store.ListArtifacts("t010-ledger-old-writer", TaskArtifactListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListArtifacts old writer rows: %v", err)
	}
	if len(oldWriterPage.Items) != 2 {
		t.Fatalf("old writer page len=%d want=2", len(oldWriterPage.Items))
	}
	for i, artifact := range oldWriterPage.Items {
		if artifact.TaskID != "t010-ledger-old-writer" || t010ArtifactEventSeq(t, artifact) != int64(i+1) {
			t.Fatalf("old writer artifact[%d]=%#v want task-local event_seq=%d", i, artifact, i+1)
		}
	}
	next, err := t010AppendStoreRuntimeEvents(t, store, "t010-ledger-old-writer", []TaskRuntimeEventAppend{{
		EventType: "text_delta",
		Channel:   "stdout",
		Summary:   "new writer after legacy rows",
		Payload:   map[string]any{"ordinal": 3},
	}})
	if err != nil {
		t.Fatalf("AppendRuntimeEvents after old writer rows: %v", err)
	}
	if len(next) != 1 || t010ArtifactEventSeq(t, next[0]) != 3 {
		t.Fatalf("AppendRuntimeEvents after old writer returned %#v want one row with event_seq=3", next)
	}
	combined, err := store.ListArtifacts("t010-ledger-old-writer", TaskArtifactListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListArtifacts combined old/new writer rows: %v", err)
	}
	if len(combined.Items) != 3 {
		t.Fatalf("combined old/new writer page len=%d want=3", len(combined.Items))
	}
	for i, artifact := range combined.Items {
		if t010ArtifactEventSeq(t, artifact) != int64(i+1) {
			t.Fatalf("combined artifact[%d].event_seq=%d want=%d", i, t010ArtifactEventSeq(t, artifact), i+1)
		}
	}
}

func TestLoomEngine_EventLedger_BatchIsAtomicDurableBeforePushAndPersistsExactGap(t *testing.T) {
	path := t.TempDir() + "/t010-batch.db"
	db := t004OpenDB(t, path)
	store := t004NewStore(t, db, "t010-batch")
	createArtifactTask(t, store, "t010-batch-task", "t010-batch", TaskStatusRunning)
	observer := t004OpenDB(t, path)
	engine := New(store)
	defer func() { _ = engine.Close(context.Background()) }()

	const (
		wantDroppedEvents uint64 = 17
		wantDroppedBytes  uint64 = 4097
	)
	batch := []TaskRuntimeEventAppend{
		{
			EventType: "text_delta",
			Channel:   "stdout",
			Summary:   "normal runtime event",
			Payload:   map[string]any{"text": "normal"},
		},
		{
			EventType: "output_truncated",
			Channel:   "system",
			Summary:   "admission quota gap",
			Payload: map[string]any{
				"reason":         "admission_quota",
				"dropped_events": wantDroppedEvents,
				"dropped_bytes":  wantDroppedBytes,
			},
			Truncated: true,
		},
		{
			EventType: "runtime_completed",
			Channel:   "control",
			Summary:   "runtime terminal control event",
			Payload:   map[string]any{"exit_code": 0},
		},
	}

	var visibleRowsAtPush []int
	var observedEventTypes []EventType
	var pushErrors []error
	unsubscribe := engine.Events().Subscribe(func(event TaskEvent) {
		if event.TaskID != "t010-batch-task" {
			return
		}
		var count int
		err := observer.QueryRow(`SELECT count(*) FROM task_artifacts WHERE task_id='t010-batch-task'`).Scan(&count)
		if err != nil {
			pushErrors = append(pushErrors, err)
			return
		}
		observedEventTypes = append(observedEventTypes, event.Type)
		visibleRowsAtPush = append(visibleRowsAtPush, count)
	})
	defer unsubscribe()

	_, err := db.Exec(`
CREATE TRIGGER t010_abort_second_runtime_event
BEFORE INSERT ON task_artifacts
WHEN NEW.task_id='t010-batch-task' AND NEW.event_type='output_truncated'
BEGIN
	SELECT RAISE(ABORT, 't010 abort second insert');
END`)
	if err != nil {
		t.Fatalf("create abort trigger: %v", err)
	}
	failed, err := t010AppendRuntimeEvents(t, engine, "t010-batch-task", batch)
	if err == nil {
		t.Fatal("AppendRuntimeEvents with abort trigger succeeded; want error")
	}
	if len(failed) != 0 {
		t.Fatalf("failed batch returned %d artifacts; want none", len(failed))
	}
	var durableAfterFailure int
	if err := observer.QueryRow(`SELECT count(*) FROM task_artifacts WHERE task_id='t010-batch-task'`).Scan(&durableAfterFailure); err != nil {
		t.Fatalf("count failed batch rows: %v", err)
	}
	if durableAfterFailure != 0 {
		t.Fatalf("failed batch left %d durable prefix rows; want 0", durableAfterFailure)
	}
	if len(visibleRowsAtPush) != 0 || len(observedEventTypes) != 0 || len(pushErrors) != 0 {
		t.Fatalf("failed batch published task-visible events: types=%v rows=%v errors=%v", observedEventTypes, visibleRowsAtPush, pushErrors)
	}

	if _, err := db.Exec(`DROP TRIGGER t010_abort_second_runtime_event`); err != nil {
		t.Fatalf("drop abort trigger: %v", err)
	}
	persisted, err := t010AppendRuntimeEvents(t, engine, "t010-batch-task", batch)
	if err != nil {
		t.Fatalf("AppendRuntimeEvents success: %v", err)
	}
	if len(persisted) != len(batch) {
		t.Fatalf("persisted batch len=%d want=%d", len(persisted), len(batch))
	}
	for i, artifact := range persisted {
		if eventSeq := t010ArtifactEventSeq(t, artifact); eventSeq != int64(i+1) {
			t.Fatalf("returned artifact[%d].event_seq=%d want=%d", i, eventSeq, i+1)
		}
	}
	if len(pushErrors) != 0 {
		t.Fatalf("post-commit push observer errors: %v", pushErrors)
	}
	if len(visibleRowsAtPush) != 1 || len(observedEventTypes) != 1 {
		t.Fatalf("successful batch events: types=%v rows=%v want exactly one", observedEventTypes, visibleRowsAtPush)
	}
	if observedEventTypes[0] != EventType("task.artifacts_appended") {
		t.Fatalf("successful batch event type=%q want task.artifacts_appended", observedEventTypes[0])
	}
	for i, visible := range visibleRowsAtPush {
		if visible != len(batch) {
			t.Fatalf("push[%d] observed %d durable rows want complete batch %d", i, visible, len(batch))
		}
	}

	page, err := store.ListArtifacts("t010-batch-task", TaskArtifactListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListArtifacts batch: %v", err)
	}
	if len(page.Items) != len(batch) {
		t.Fatalf("durable batch len=%d want=%d", len(page.Items), len(batch))
	}
	gapCount := 0
	for i, artifact := range page.Items {
		if eventSeq := t010ArtifactEventSeq(t, artifact); eventSeq != int64(i+1) {
			t.Fatalf("durable artifact[%d].event_seq=%d want=%d", i, eventSeq, i+1)
		}
		if artifact.EventType != "output_truncated" || artifact.Channel != "system" {
			continue
		}
		gapCount++
		if artifact.Kind != TaskArtifactKindRuntime || !artifact.Truncated {
			t.Fatalf("gap kind/truncated=%q/%v want runtime/true", artifact.Kind, artifact.Truncated)
		}
		if reason, _ := artifact.Payload["reason"].(string); reason != "admission_quota" {
			t.Fatalf("gap reason=%v want admission_quota", artifact.Payload["reason"])
		}
	}
	if gapCount != 1 {
		t.Fatalf("quota-gap artifact count=%d want=1", gapCount)
	}
	var persistedGapCount, droppedEvents, droppedBytes int64
	if err := db.QueryRow(`
SELECT count(*),
       coalesce(max(json_extract(payload_json,'$.dropped_events')),0),
       coalesce(max(json_extract(payload_json,'$.dropped_bytes')),0)
FROM task_artifacts
WHERE task_id=? AND event_type='output_truncated' AND channel='system'
  AND truncated=1 AND json_extract(payload_json,'$.reason')='admission_quota'`,
		"t010-batch-task").Scan(&persistedGapCount, &droppedEvents, &droppedBytes); err != nil {
		t.Fatalf("query exact persisted quota gap: %v", err)
	}
	if persistedGapCount != 1 || droppedEvents != int64(wantDroppedEvents) || droppedBytes != int64(wantDroppedBytes) {
		t.Fatalf("persisted quota gap count/events/bytes=%d/%d/%d want=1/%d/%d",
			persistedGapCount, droppedEvents, droppedBytes, wantDroppedEvents, wantDroppedBytes)
	}
}

type t010RuntimeEventBatchAppender interface {
	AppendRuntimeEvents(string, []TaskRuntimeEventAppend) ([]TaskArtifact, error)
}

func t010AppendStoreRuntimeEvents(t *testing.T, store *TaskStore, taskID string, batch []TaskRuntimeEventAppend) ([]TaskArtifact, error) {
	t.Helper()
	appender, ok := any(store).(t010RuntimeEventBatchAppender)
	if !ok {
		t.Fatalf("*TaskStore does not implement AppendRuntimeEvents(string, []TaskRuntimeEventAppend) ([]TaskArtifact, error)")
	}
	return appender.AppendRuntimeEvents(taskID, batch)
}

func t010AppendRuntimeEvents(t *testing.T, engine *LoomEngine, taskID string, batch []TaskRuntimeEventAppend) ([]TaskArtifact, error) {
	t.Helper()
	appender, ok := any(engine).(t010RuntimeEventBatchAppender)
	if !ok {
		t.Fatalf("*LoomEngine does not implement AppendRuntimeEvents(string, []TaskRuntimeEventAppend) ([]TaskArtifact, error)")
	}
	return appender.AppendRuntimeEvents(taskID, batch)
}

func t010ArtifactEventSeq(t *testing.T, artifact TaskArtifact) int64 {
	t.Helper()
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal TaskArtifact event_seq shape: %v", err)
	}
	var shape struct {
		EventSeq *int64 `json:"event_seq"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("unmarshal TaskArtifact event_seq shape: %v", err)
	}
	if shape.EventSeq == nil {
		t.Fatal("TaskArtifact JSON is missing event_seq")
	}
	return *shape.EventSeq
}
