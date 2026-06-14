package loom

import (
	"context"
	"errors"
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

func TestLoomEngine_ArtifactTerminal_FailedTaskIncludesErrorAndLastProgress(t *testing.T) {
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

	terminalPage, err := store.ListArtifacts(taskID, TaskArtifactListOptions{
		Kinds: []TaskArtifactKind{TaskArtifactKindTerminal},
	})
	if err != nil {
		t.Fatalf("ListArtifacts terminal: %v", err)
	}
	if len(terminalPage.Items) == 0 {
		t.Fatalf("terminal artifact missing for failed task")
	}
	terminal := terminalPage.Items[len(terminalPage.Items)-1]
	if terminal.Kind != TaskArtifactKindTerminal {
		t.Fatalf("terminal artifact kind = %q; want %q", terminal.Kind, TaskArtifactKindTerminal)
	}
	if terminal.Payload["error_class"] != "worker_error" {
		t.Fatalf("terminal error_class = %v; want worker_error", terminal.Payload["error_class"])
	}
	if terminal.Payload["last_output_line"] != task.LastOutputLine {
		t.Fatalf("terminal last_output_line payload = %v; want %q", terminal.Payload["last_output_line"], task.LastOutputLine)
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

func TestLoomEngine_ArtifactTerminal_NoWorkerWritesFailureArtifact(t *testing.T) {
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
	if len(page.Items) == 0 {
		t.Fatalf("terminal artifact missing for no-worker failure")
	}
	if page.Items[len(page.Items)-1].Payload["error_class"] != "worker_unavailable" {
		t.Fatalf("no-worker error_class = %v; want worker_unavailable", page.Items[len(page.Items)-1].Payload["error_class"])
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
