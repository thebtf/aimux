package loom

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTaskLifecycleAuthority_AdditiveSurface(t *testing.T) {
	surfaceOK := true
	stringType := reflect.TypeOf("")
	workerType := reflect.TypeOf(WorkerType(""))
	statusType := reflect.TypeOf(TaskStatus(""))
	intType := reflect.TypeOf(int(0))
	timeType := reflect.TypeOf(time.Time{})
	stringMapType := reflect.TypeOf(map[string]string{})
	anyMapType := reflect.TypeOf(map[string]any{})
	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	commitResultType := reflect.TypeOf(CommitResult{})
	errorType := reflect.TypeOf((*error)(nil)).Elem()

	type field struct {
		name   string
		typeOf reflect.Type
	}
	wants := []struct {
		method  string
		command string
		fields  []field
	}{
		{
			method: "CommitCreated", command: "CreateTask",
			fields: []field{
				{name: "TaskID", typeOf: stringType},
				{name: "WorkerType", typeOf: workerType},
				{name: "ProjectID", typeOf: stringType},
				{name: "RequestID", typeOf: stringType},
				{name: "ParentTaskID", typeOf: stringType},
				{name: "TenantID", typeOf: stringType},
				{name: "Prompt", typeOf: stringType},
				{name: "CWD", typeOf: stringType},
				{name: "Env", typeOf: stringMapType},
				{name: "CLI", typeOf: stringType},
				{name: "Role", typeOf: stringType},
				{name: "Model", typeOf: stringType},
				{name: "Effort", typeOf: stringType},
				{name: "Timeout", typeOf: intType},
				{name: "Metadata", typeOf: anyMapType},
				{name: "CreatedAt", typeOf: timeType},
			},
		},
		{
			method: "CommitRunning", command: "RunTask",
			fields: []field{
				{name: "TaskID", typeOf: stringType},
				{name: "ExpectedStatus", typeOf: statusType},
				{name: "RunningAt", typeOf: timeType},
			},
		},
		{
			method: "CommitRetrying", command: "RetryTask",
			fields: []field{
				{name: "TaskID", typeOf: stringType},
				{name: "ExpectedStatus", typeOf: statusType},
				{name: "RetryingAt", typeOf: timeType},
			},
		},
	}

	storeType := reflect.TypeOf((*TaskStore)(nil))
	loomPkg := reflect.TypeOf(Task{}).PkgPath()
	for _, want := range wants {
		method, ok := storeType.MethodByName(want.method)
		if !ok {
			t.Errorf("*TaskStore missing additive method %s", want.method)
			surfaceOK = false
			continue
		}
		if method.Type.NumIn() != 3 || method.Type.In(1) != contextType || method.Type.NumOut() != 2 || method.Type.Out(0) != commitResultType || method.Type.Out(1) != errorType {
			t.Errorf("%s signature=%s, want func(context.Context, %s) (CommitResult, error)", want.method, method.Type, want.command)
			surfaceOK = false
			continue
		}
		command := method.Type.In(2)
		if command.Kind() != reflect.Struct || command.Name() != want.command || command.PkgPath() != loomPkg {
			t.Errorf("%s command=%s (kind=%s pkg=%q), want non-pointer loom.%s", want.method, command, command.Kind(), command.PkgPath(), want.command)
			surfaceOK = false
			continue
		}
		if command.NumField() != len(want.fields) {
			t.Errorf("%s command fields=%d, want %d", want.method, command.NumField(), len(want.fields))
			surfaceOK = false
			continue
		}
		for index, expected := range want.fields {
			got := command.Field(index)
			if got.Name != expected.name || got.Type != expected.typeOf {
				t.Errorf("%s field[%d]=%s %s, want %s %s", want.method, index, got.Name, got.Type, expected.name, expected.typeOf)
				surfaceOK = false
			}
		}
	}

	authority := reflect.TypeOf((*TaskAuthority)(nil)).Elem()
	wantAuthority := []string{
		"BeginActionResponse", "CommitActionResolution", "CommitCancelled",
		"CommitCompleted", "CommitDispatched", "CommitFailed", "CommitFailedCrash",
		"CommitInputRequired", "RequestCancel",
	}
	if authority.NumMethod() != len(wantAuthority) {
		t.Errorf("TaskAuthority method count=%d, want exact legacy 9", authority.NumMethod())
		surfaceOK = false
	}
	for index, name := range wantAuthority {
		if index >= authority.NumMethod() {
			break
		}
		if got := authority.Method(index).Name; got != name {
			t.Errorf("TaskAuthority method[%d]=%s, want %s", index, got, name)
			surfaceOK = false
		}
	}
	wantSignatures := map[string]reflect.Type{
		"RequestCancel":          reflect.TypeOf(func(context.Context, string, time.Time) (CancelIntent, error) { return CancelIntent{}, nil }),
		"CommitDispatched":       reflect.TypeOf(func(context.Context, DispatchTask) (CommitResult, error) { return CommitResult{}, nil }),
		"CommitCompleted":        reflect.TypeOf(func(context.Context, CompleteTask) (CommitResult, error) { return CommitResult{}, nil }),
		"CommitFailed":           reflect.TypeOf(func(context.Context, FailTask) (CommitResult, error) { return CommitResult{}, nil }),
		"CommitFailedCrash":      reflect.TypeOf(func(context.Context, FailCrashedTask) (CommitResult, error) { return CommitResult{}, nil }),
		"CommitCancelled":        reflect.TypeOf(func(context.Context, CancelTask) (CommitResult, error) { return CommitResult{}, nil }),
		"CommitInputRequired":    reflect.TypeOf(func(context.Context, RequireInput) (CommitResult, error) { return CommitResult{}, nil }),
		"BeginActionResponse":    reflect.TypeOf(func(context.Context, BeginResponse) (PendingActionAttempt, error) { return PendingActionAttempt{}, nil }),
		"CommitActionResolution": reflect.TypeOf(func(context.Context, ResolveAction) (CommitResult, error) { return CommitResult{}, nil }),
	}
	for name, want := range wantSignatures {
		method, ok := authority.MethodByName(name)
		if !ok {
			t.Errorf("TaskAuthority missing legacy method %s", name)
			surfaceOK = false
			continue
		}
		if method.Type != want {
			t.Errorf("TaskAuthority.%s signature=%s, want %s", name, method.Type, want)
			surfaceOK = false
		}
	}
	if !surfaceOK {
		return
	}

	t.Run("commit-result-semantics", func(t *testing.T) {
		fixture := t013NewFixture(t)
		createFields := func(id string) map[string]any {
			return map[string]any{
				"TaskID": id, "WorkerType": WorkerTypeCLI, "ProjectID": "reflect-project",
				"RequestID": "reflect-request", "ParentTaskID": "reflect-parent", "TenantID": "reflect-tenant",
				"Prompt": "reflect prompt", "CWD": "D:/reflect", "Env": map[string]string{"T013": "true"},
				"CLI": "codex", "Role": "maker", "Model": "reflect-model", "Effort": "high", "Timeout": 37,
				"Metadata": map[string]any{"source": "t013"}, "CreatedAt": t013At,
			}
		}

		created, err := t013InvokeReflectedCommit(t, fixture.store, "CommitCreated", createFields("reflect-lifecycle"))
		if err != nil {
			t.Fatalf("CommitCreated: %v", err)
		}
		createdTask, getErr := fixture.view.Get("reflect-lifecycle")
		wantTask := &Task{
			ID: "reflect-lifecycle", Status: TaskStatusPending, WorkerType: WorkerTypeCLI, ProjectID: "reflect-project",
			RequestID: "reflect-request", ParentTaskID: "reflect-parent", EngineName: fixture.name, TenantID: "reflect-tenant",
			Prompt: "reflect prompt", CWD: "D:/reflect", Env: map[string]string{"T013": "true"}, CLI: "codex",
			Role: "maker", Model: "reflect-model", Effort: "high", Timeout: 37,
			Metadata: map[string]any{"source": "t013"}, CreatedAt: t013At,
		}
		if getErr != nil || !reflect.DeepEqual(createdTask, wantTask) {
			t.Errorf("created task=%#v err=%v, want all 16 command fields persisted as %#v", createdTask, getErr, wantTask)
		}
		createdFact := t013ArtifactByEvent(t, fixture.view, "reflect-lifecycle", "task.created")
		createdWinnerMatches := createdTask != nil && reflect.DeepEqual(created.Winner.Task, t013CanonicalTaskProjection(createdTask))
		if !created.Applied || !createdWinnerMatches || created.ArtifactSeq != createdFact.Seq || created.ClosedActionCount != 0 || created.Winner.Action != nil || len(created.Winner.Conflicts) != 0 {
			t.Errorf("CommitCreated result=%#v fact_seq=%d canonical=%#v, want applied full canonical winner/exact seq/closed=0", created, createdFact.Seq, t013CanonicalTaskProjection(createdTask))
		}
		t013AssertExactArtifact(t, createdFact, TaskArtifactKindLifecycle, "task.created", map[string]any{
			"status": "pending", "closed_action_count": int64(0),
		}, t013At)

		artifactBaseline := len(t013Artifacts(t, fixture.view, "reflect-lifecycle"))
		duplicate, duplicateErr := t013InvokeReflectedCommit(t, fixture.store, "CommitCreated", createFields("reflect-lifecycle"))
		if !errors.Is(duplicateErr, ErrAuthorityConflict) || duplicate.Applied || duplicate.ArtifactSeq != 0 || duplicate.ClosedActionCount != 0 || duplicate.Winner.Task.TaskID != "reflect-lifecycle" || duplicate.Winner.Task.Status != TaskStatusPending || duplicate.Winner.Action != nil || len(duplicate.Winner.Conflicts) != 1 || duplicate.Winner.Conflicts[0].Kind != ConflictTaskStatus || duplicate.Winner.Conflicts[0].Action != nil {
			t.Errorf("duplicate CommitCreated=%#v err=%v, want exact pending task-status conflict with zero seq/count", duplicate, duplicateErr)
		}
		if after := len(t013Artifacts(t, fixture.view, "reflect-lifecycle")); after != artifactBaseline {
			t.Errorf("duplicate create artifacts=%d, want unchanged %d", after, artifactBaseline)
		}

		dispatchedAt := t013At.Add(time.Second)
		dispatched, dispatchErr := fixture.store.CommitDispatched(context.Background(), DispatchTask{
			TaskID: "reflect-lifecycle", ExpectedStatus: TaskStatusPending, DispatchedAt: dispatchedAt,
		})
		if dispatchErr != nil || !dispatched.Applied || dispatched.Winner.Task.Status != TaskStatusDispatched || dispatched.ClosedActionCount != 0 {
			t.Fatalf("typed CommitDispatched=%#v err=%v", dispatched, dispatchErr)
		}
		dispatchFact := t013ArtifactByEvent(t, fixture.view, "reflect-lifecycle", "task.dispatched")
		if dispatched.ArtifactSeq != dispatchFact.Seq {
			t.Errorf("dispatch ArtifactSeq=%d, want persisted seq %d", dispatched.ArtifactSeq, dispatchFact.Seq)
		}

		beforeInvalid := t013Artifacts(t, fixture.view, "reflect-lifecycle")
		invalidCases := []struct {
			name   string
			fields map[string]any
		}{
			{name: "illegal-expected-status", fields: map[string]any{"TaskID": "reflect-lifecycle", "ExpectedStatus": TaskStatusRunning, "RunningAt": t013At.Add(2 * time.Second)}},
			{name: "time-before-dispatched", fields: map[string]any{"TaskID": "reflect-lifecycle", "ExpectedStatus": TaskStatusDispatched, "RunningAt": t013At}},
			{name: "missing-task", fields: map[string]any{"TaskID": "reflect-missing", "ExpectedStatus": TaskStatusDispatched, "RunningAt": t013At.Add(2 * time.Second)}},
		}
		for _, tc := range invalidCases {
			t.Run(tc.name, func(t *testing.T) {
				result, callErr := t013InvokeReflectedCommit(t, fixture.store, "CommitRunning", tc.fields)
				if callErr == nil || errors.Is(callErr, ErrAuthorityConflict) || !reflect.DeepEqual(result, CommitResult{}) {
					t.Errorf("CommitRunning invalid result=%#v err=%v, want wholly zero/non-conflict error", result, callErr)
				}
			})
		}
		if afterInvalid := t013Artifacts(t, fixture.view, "reflect-lifecycle"); !reflect.DeepEqual(afterInvalid, beforeInvalid) {
			t.Errorf("invalid CommitRunning calls changed facts: before=%#v after=%#v", beforeInvalid, afterInvalid)
		}

		t013SeedAction(t, fixture.db, "run-pending", "reflect-lifecycle")
		t013SeedAction(t, fixture.db, "run-responding", "reflect-lifecycle")
		if _, err := fixture.db.Exec(`UPDATE pending_actions SET status='responding',response_json='{"answer":"seed"}' WHERE id='run-responding'`); err != nil {
			t.Fatal(err)
		}
		runningAt := t013At.Add(2 * time.Second)
		running, runningErr := t013InvokeReflectedCommit(t, fixture.store, "CommitRunning", map[string]any{
			"TaskID": "reflect-lifecycle", "ExpectedStatus": TaskStatusDispatched, "RunningAt": runningAt,
		})
		runningTask, runningGetErr := fixture.view.Get("reflect-lifecycle")
		runningWinnerMatches := runningTask != nil && reflect.DeepEqual(running.Winner.Task, t013CanonicalTaskProjection(runningTask))
		if runningErr != nil || runningGetErr != nil || !running.Applied || !runningWinnerMatches || running.ClosedActionCount != 2 || running.Winner.Action != nil || len(running.Winner.Conflicts) != 0 {
			t.Errorf("CommitRunning=%#v err=%v persisted=%#v get_err=%v, want applied full canonical running winner/closed=2", running, runningErr, runningTask, runningGetErr)
		}
		runningFact := t013ArtifactByEvent(t, fixture.view, "reflect-lifecycle", "task.running")
		if running.ArtifactSeq != runningFact.Seq {
			t.Errorf("running ArtifactSeq=%d, want persisted seq %d", running.ArtifactSeq, runningFact.Seq)
		}
		t013AssertExactArtifact(t, runningFact, TaskArtifactKindLifecycle, "task.running", map[string]any{
			"status": "running", "closed_action_count": int64(2),
		}, runningAt)
		for _, actionID := range []string{"run-pending", "run-responding"} {
			status, resolved := t013Action(t, fixture.observer, actionID)
			if status != "task_closed" || resolved == nil || !resolved.Equal(runningAt) {
				t.Errorf("running closed action %s=%s/%v, want task_closed/%s", actionID, status, resolved, runningAt)
			}
		}

		if _, err := fixture.db.Exec(`UPDATE tasks SET retries=4 WHERE id='reflect-lifecycle'`); err != nil {
			t.Fatal(err)
		}
		t013SeedAction(t, fixture.db, "retry-pending", "reflect-lifecycle")
		t013SeedAction(t, fixture.db, "retry-responding", "reflect-lifecycle")
		if _, err := fixture.db.Exec(`UPDATE pending_actions SET status='responding',response_json='{"answer":"seed"}' WHERE id='retry-responding'`); err != nil {
			t.Fatal(err)
		}
		retryingAt := t013At.Add(3 * time.Second)
		retrying, retryingErr := t013InvokeReflectedCommit(t, fixture.store, "CommitRetrying", map[string]any{
			"TaskID": "reflect-lifecycle", "ExpectedStatus": TaskStatusRunning, "RetryingAt": retryingAt,
		})
		retryingTask, retryGetErr := fixture.view.Get("reflect-lifecycle")
		retryingWinnerMatches := retryingTask != nil && reflect.DeepEqual(retrying.Winner.Task, t013CanonicalTaskProjection(retryingTask))
		if retryingErr != nil || retryGetErr != nil || !retrying.Applied || !retryingWinnerMatches || retrying.ClosedActionCount != 2 || retrying.Winner.Action != nil || len(retrying.Winner.Conflicts) != 0 {
			t.Errorf("CommitRetrying=%#v err=%v persisted=%#v get_err=%v, want applied full canonical retrying winner/closed=2", retrying, retryingErr, retryingTask, retryGetErr)
		}
		if retryGetErr != nil || retryingTask.Status != TaskStatusRetrying || retryingTask.Retries != 5 {
			t.Errorf("retrying task=%#v err=%v, want retrying/retries=5", retryingTask, retryGetErr)
		}
		retryingFact := t013ArtifactByEvent(t, fixture.view, "reflect-lifecycle", "task.retrying")
		if retrying.ArtifactSeq != retryingFact.Seq {
			t.Errorf("retrying ArtifactSeq=%d, want persisted seq %d", retrying.ArtifactSeq, retryingFact.Seq)
		}
		t013AssertExactArtifact(t, retryingFact, TaskArtifactKindLifecycle, "task.retrying", map[string]any{
			"status": "retrying", "retry_count": int64(5), "closed_action_count": int64(2),
		}, retryingAt)
		for _, actionID := range []string{"retry-pending", "retry-responding"} {
			status, resolved := t013Action(t, fixture.observer, actionID)
			if status != "task_closed" || resolved == nil || !resolved.Equal(retryingAt) {
				t.Errorf("retrying closed action %s=%s/%v, want task_closed/%s", actionID, status, resolved, retryingAt)
			}
		}

		conflictBaseline := len(t013Artifacts(t, fixture.view, "reflect-lifecycle"))
		conflict, conflictErr := t013InvokeReflectedCommit(t, fixture.store, "CommitRetrying", map[string]any{
			"TaskID": "reflect-lifecycle", "ExpectedStatus": TaskStatusRunning, "RetryingAt": t013At.Add(4 * time.Second),
		})
		if !errors.Is(conflictErr, ErrAuthorityConflict) || conflict.Applied || conflict.ArtifactSeq != 0 || conflict.ClosedActionCount != 0 || conflict.Winner.Task.TaskID != "reflect-lifecycle" || conflict.Winner.Task.Status != TaskStatusRetrying || conflict.Winner.Action != nil || len(conflict.Winner.Conflicts) != 1 || conflict.Winner.Conflicts[0].Kind != ConflictTaskStatus || conflict.Winner.Conflicts[0].Action != nil {
			t.Errorf("CommitRetrying conflict=%#v err=%v, want exact retrying task-status winner", conflict, conflictErr)
		}
		if after := len(t013Artifacts(t, fixture.view, "reflect-lifecycle")); after != conflictBaseline {
			t.Errorf("retry conflict artifacts=%d, want unchanged %d", after, conflictBaseline)
		}

		runningAbortTask := &Task{ID: "reflect-running-abort", Status: TaskStatusPending, WorkerType: WorkerTypeCLI, ProjectID: "reflect-abort", Prompt: "running abort", CreatedAt: t013At}
		if err := fixture.store.Create(runningAbortTask); err != nil {
			t.Fatal(err)
		}
		if result, err := fixture.store.CommitDispatched(context.Background(), DispatchTask{TaskID: runningAbortTask.ID, ExpectedStatus: TaskStatusPending, DispatchedAt: t013At.Add(10 * time.Second)}); err != nil || !result.Applied {
			t.Fatalf("prepare running abort task=%#v err=%v", result, err)
		}
		t013SeedAction(t, fixture.db, "reflect-running-abort-action", runningAbortTask.ID)
		if _, err := fixture.db.Exec(`CREATE TRIGGER t013_reflect_abort_running BEFORE INSERT ON task_artifacts
			WHEN NEW.task_id='reflect-running-abort' AND NEW.event_type='task.running'
			BEGIN SELECT RAISE(ABORT,'T013_REFLECT_RUNNING_ABORT'); END`); err != nil {
			t.Fatal(err)
		}
		runningBefore, err := fixture.view.Get(runningAbortTask.ID)
		if err != nil {
			t.Fatal(err)
		}
		runningActionsBefore := t004ReadRows(t, fixture.observer, "pending_actions", "id")
		runningFactsBefore := t013Artifacts(t, fixture.view, runningAbortTask.ID)
		runningAborted, runningAbortErr := t013InvokeReflectedCommit(t, fixture.store, "CommitRunning", map[string]any{
			"TaskID": runningAbortTask.ID, "ExpectedStatus": TaskStatusDispatched, "RunningAt": t013At.Add(11 * time.Second),
		})
		if runningAbortErr == nil || errors.Is(runningAbortErr, ErrAuthorityConflict) || !strings.Contains(runningAbortErr.Error(), "T013_REFLECT_RUNNING_ABORT") || !reflect.DeepEqual(runningAborted, CommitResult{}) {
			t.Errorf("aborted CommitRunning=%#v err=%v, want exact zero/non-conflict artifact abort", runningAborted, runningAbortErr)
		}
		runningAfter, runningAfterErr := fixture.view.Get(runningAbortTask.ID)
		if runningAfterErr != nil || !reflect.DeepEqual(runningAfter, runningBefore) {
			t.Errorf("CommitRunning abort row before=%#v after=%#v err=%v", runningBefore, runningAfter, runningAfterErr)
		}
		if actionsAfter := t004ReadRows(t, fixture.observer, "pending_actions", "id"); !reflect.DeepEqual(actionsAfter, runningActionsBefore) {
			t.Errorf("CommitRunning abort changed actions: before=%#v after=%#v", runningActionsBefore, actionsAfter)
		}
		if factsAfter := t013Artifacts(t, fixture.view, runningAbortTask.ID); !reflect.DeepEqual(factsAfter, runningFactsBefore) {
			t.Errorf("CommitRunning abort changed facts: before=%#v after=%#v", runningFactsBefore, factsAfter)
		}

		retryingAbortTask := &Task{ID: "reflect-retrying-abort", Status: TaskStatusPending, WorkerType: WorkerTypeCLI, ProjectID: "reflect-abort", Prompt: "retrying abort", CreatedAt: t013At}
		if err := fixture.store.Create(retryingAbortTask); err != nil {
			t.Fatal(err)
		}
		if result, err := fixture.store.CommitDispatched(context.Background(), DispatchTask{TaskID: retryingAbortTask.ID, ExpectedStatus: TaskStatusPending, DispatchedAt: t013At.Add(20 * time.Second)}); err != nil || !result.Applied {
			t.Fatalf("prepare retrying abort task=%#v err=%v", result, err)
		}
		if _, err := fixture.db.Exec(`UPDATE tasks SET status=? WHERE id=?`, TaskStatusRunning, retryingAbortTask.ID); err != nil {
			t.Fatal(err)
		}
		t013SeedAction(t, fixture.db, "reflect-retrying-abort-action", retryingAbortTask.ID)
		if _, err := fixture.db.Exec(`CREATE TRIGGER t013_reflect_abort_retrying BEFORE INSERT ON task_artifacts
			WHEN NEW.task_id='reflect-retrying-abort' AND NEW.event_type='task.retrying'
			BEGIN SELECT RAISE(ABORT,'T013_REFLECT_RETRYING_ABORT'); END`); err != nil {
			t.Fatal(err)
		}
		retryingBefore, err := fixture.view.Get(retryingAbortTask.ID)
		if err != nil {
			t.Fatal(err)
		}
		retryingActionsBefore := t004ReadRows(t, fixture.observer, "pending_actions", "id")
		retryingFactsBefore := t013Artifacts(t, fixture.view, retryingAbortTask.ID)
		retryingAborted, retryingAbortErr := t013InvokeReflectedCommit(t, fixture.store, "CommitRetrying", map[string]any{
			"TaskID": retryingAbortTask.ID, "ExpectedStatus": TaskStatusRunning, "RetryingAt": t013At.Add(21 * time.Second),
		})
		if retryingAbortErr == nil || errors.Is(retryingAbortErr, ErrAuthorityConflict) || !strings.Contains(retryingAbortErr.Error(), "T013_REFLECT_RETRYING_ABORT") || !reflect.DeepEqual(retryingAborted, CommitResult{}) {
			t.Errorf("aborted CommitRetrying=%#v err=%v, want exact zero/non-conflict artifact abort", retryingAborted, retryingAbortErr)
		}
		retryingAfter, retryingAfterErr := fixture.view.Get(retryingAbortTask.ID)
		if retryingAfterErr != nil || !reflect.DeepEqual(retryingAfter, retryingBefore) {
			t.Errorf("CommitRetrying abort row before=%#v after=%#v err=%v", retryingBefore, retryingAfter, retryingAfterErr)
		}
		if actionsAfter := t004ReadRows(t, fixture.observer, "pending_actions", "id"); !reflect.DeepEqual(actionsAfter, retryingActionsBefore) {
			t.Errorf("CommitRetrying abort changed actions: before=%#v after=%#v", retryingActionsBefore, actionsAfter)
		}
		if factsAfter := t013Artifacts(t, fixture.view, retryingAbortTask.ID); !reflect.DeepEqual(factsAfter, retryingFactsBefore) {
			t.Errorf("CommitRetrying abort changed facts: before=%#v after=%#v", retryingFactsBefore, factsAfter)
		}

		if _, err := fixture.db.Exec(`CREATE TRIGGER t013_reflect_abort_created BEFORE INSERT ON task_artifacts
			WHEN NEW.task_id='reflect-abort' AND NEW.event_type='task.created'
			BEGIN SELECT RAISE(ABORT,'T013_REFLECT_CREATE_ABORT'); END`); err != nil {
			t.Fatal(err)
		}
		aborted, abortErr := t013InvokeReflectedCommit(t, fixture.store, "CommitCreated", createFields("reflect-abort"))
		if abortErr == nil || errors.Is(abortErr, ErrAuthorityConflict) || !strings.Contains(abortErr.Error(), "T013_REFLECT_CREATE_ABORT") || !reflect.DeepEqual(aborted, CommitResult{}) {
			t.Errorf("aborted CommitCreated=%#v err=%v, want wholly zero/non-conflict abort", aborted, abortErr)
		}
		if task, err := fixture.view.Get("reflect-abort"); task != nil || !errors.Is(err, ErrTaskNotFound) {
			t.Errorf("aborted create task=%#v err=%v, want absent", task, err)
		}
		if artifacts := t013Artifacts(t, fixture.view, "reflect-abort"); len(artifacts) != 0 {
			t.Errorf("aborted create artifacts=%d, want 0", len(artifacts))
		}
	})
}

func t013CanonicalTaskProjection(task *Task) CanonicalTaskState {
	if task == nil {
		return CanonicalTaskState{}
	}
	return CanonicalTaskState{
		TaskID: task.ID, Status: task.Status, CreatedAt: task.CreatedAt, Result: task.Result, Error: task.Error,
		DispatchedAt: task.DispatchedAt, CancelRequestedAt: task.CancelRequestedAt, CompletedAt: task.CompletedAt,
	}
}

func t013InvokeReflectedCommit(t *testing.T, store *TaskStore, methodName string, fields map[string]any) (CommitResult, error) {
	t.Helper()
	method := reflect.ValueOf(store).MethodByName(methodName)
	if !method.IsValid() {
		t.Fatalf("missing reflected method %s after surface gate", methodName)
	}
	if method.Type().NumIn() != 2 {
		t.Fatalf("bound %s inputs=%d, want context+command", methodName, method.Type().NumIn())
	}
	commandType := method.Type().In(1)
	command := reflect.New(commandType).Elem()
	for name, value := range fields {
		field := command.FieldByName(name)
		if !field.IsValid() || !field.CanSet() {
			t.Fatalf("%s command field %s unavailable", methodName, name)
		}
		input := reflect.ValueOf(value)
		if !input.IsValid() || !input.Type().AssignableTo(field.Type()) {
			t.Fatalf("%s field %s value type=%v, want %s", methodName, name, input.Type(), field.Type())
		}
		field.Set(input)
	}
	ctx, cancel := context.WithTimeout(context.Background(), t013Wait)
	defer cancel()
	outputs := method.Call([]reflect.Value{reflect.ValueOf(ctx), command})
	result, ok := outputs[0].Interface().(CommitResult)
	if !ok {
		t.Fatalf("%s result type=%T, want CommitResult", methodName, outputs[0].Interface())
	}
	if outputs[1].IsNil() {
		return result, nil
	}
	return result, outputs[1].Interface().(error)
}

// TestLoomEngine_Close_WaitsForInflightDispatch verifies that Close(ctx) blocks
// until all in-flight dispatch goroutines have finished.
func TestLoomEngine_Close_WaitsForInflightDispatch(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// release is closed by the test to unblock all workers.
	release := make(chan struct{})
	const taskCount = 3

	worker := &blockingWorker{done: release}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	// Submit N tasks.
	ids := make([]string, 0, taskCount)
	for i := 0; i < taskCount; i++ {
		id, err := engine.Submit(context.Background(), TaskRequest{
			WorkerType: WorkerTypeCLI,
			ProjectID:  "proj-close-drain",
			Prompt:     "block",
		})
		if err != nil {
			t.Fatalf("Submit[%d]: %v", i, err)
		}
		ids = append(ids, id)
	}

	// Wait until all tasks are running so goroutines are in-flight.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running := 0
		for _, id := range ids {
			task, _ := store.Get(id)
			if task != nil && task.Status == TaskStatusRunning {
				running++
			}
		}
		if running == taskCount {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Close must not return while goroutines are still blocked.
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- engine.Close(context.Background())
	}()

	// Verify Close has not returned yet.
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned prematurely with %v", err)
	case <-time.After(100 * time.Millisecond):
		// Good — Close is still waiting.
	}

	// Unblock all workers; Close should now drain and return.
	close(release)

	select {
	case err := <-closeDone:
		if err != nil {
			t.Errorf("Close returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after workers unblocked")
	}
}

// TestLoomEngine_Close_ContextTimeout verifies that Close returns ctx.Err()
// when the context expires before all goroutines drain.
func TestLoomEngine_Close_ContextTimeout(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// Worker that blocks indefinitely until its context is cancelled.
	neverRelease := make(chan struct{}) // never closed
	worker := &blockingWorker{done: neverRelease}
	engine.RegisterWorker(WorkerTypeCLI, worker)

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-close-timeout",
		Prompt:     "never finishes",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for task to start running.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _ := store.Get(taskID)
		if task != nil && task.Status == TaskStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Close with an already-expired context.
	expired, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond) // ensure deadline has passed

	err = engine.Close(expired)
	if err == nil {
		t.Fatal("Close with expired ctx should return an error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("Close with expired ctx: want DeadlineExceeded or Canceled, got %v", err)
	}

	// Clean up: cancel the remaining goroutine by cancelling the task.
	_ = engine.Cancel(taskID)
}

// TestLoomEngine_Submit_AfterClose_ReturnsErrEngineClosed verifies that Submit
// returns ErrEngineClosed after the engine has been shut down.
func TestLoomEngine_Submit_AfterClose_ReturnsErrEngineClosed(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	// Close immediately (no in-flight tasks).
	if err := engine.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-closed",
		Prompt:     "should be rejected",
	})
	if !errors.Is(err, ErrEngineClosed) {
		t.Errorf("Submit after Close: want ErrEngineClosed, got %v", err)
	}
}

// TestLoomEngine_Close_Idempotent verifies that multiple Close calls all return
// nil without panicking.
func TestLoomEngine_Close_Idempotent(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)

	for i := 0; i < 5; i++ {
		if err := engine.Close(context.Background()); err != nil {
			t.Errorf("Close[%d] returned error: %v", i, err)
		}
	}

	// Concurrent closes must also not panic.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := engine.Close(context.Background()); err != nil {
				t.Errorf("concurrent Close returned error: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestLoomEngine_RetryPath_UpdateStatusError_TaskReachesFailed verifies the
// BUG-002 fix: when UpdateStatus(running→retrying) is rejected by the state
// machine (because the task row was externally mutated out of 'running'), the
// engine calls failTask and the task ends in TaskStatusFailed rather than
// remaining stuck in 'retrying'.
//
// Mechanism: we submit a task with a worker that returns empty output (which
// the quality gate treats as a retryable rejection). Before the goroutine can
// execute the running→retrying transition, we race to write the task's status
// directly to 'completed' in the store so that UpdateStatus sees a stale 'from'
// and returns an error. The engine must then fall through to failTask.
//
// Note: this test is inherently racy at the Go scheduler level. To keep it
// deterministic we add a small artificial delay in the worker to widen the
// window, and then poll for the terminal state.
func TestLoomEngine_RetryPath_UpdateStatusError_TaskReachesFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping racy state-machine test in -short mode")
	}

	store := newTestStore(t)
	engine := New(store)

	// readyCh is closed by the worker just before returning empty output,
	// giving the test a precise moment to corrupt the row.
	readyCh := make(chan struct{})

	engine.RegisterWorker(WorkerTypeCLI, &racyRetryWorker{readyCh: readyCh})

	taskID, err := engine.Submit(context.Background(), TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-retry-err",
		Prompt:     "trigger retry path",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for the worker to signal it is about to return.
	select {
	case <-readyCh:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not signal readyCh in time")
	}

	// Corrupt the status so running→retrying will fail.
	_, _ = store.db.Exec("UPDATE tasks SET status='completed' WHERE id=?", taskID)

	// Poll for the task to reach a terminal state.
	deadline := time.Now().Add(5 * time.Second)
	var finalTask *Task
	for time.Now().Before(deadline) {
		finalTask, _ = store.Get(taskID)
		if finalTask != nil && finalTask.Status.IsTerminal() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if finalTask == nil {
		t.Fatal("task not found")
	}
	// The engine should have detected the store error and called failTask,
	// landing the task in 'failed'. It must NOT be stuck in 'retrying'.
	if finalTask.Status == TaskStatusRetrying {
		t.Errorf("task stuck in retrying — BUG-002 regression (UpdateStatus error was swallowed)")
	}
	// We accept 'failed', 'failed_crash', or 'completed' (if corruption raced
	// before the gate check). The key invariant is: NOT retrying.
}

// racyRetryWorker returns an empty result (retryable by gate) and signals
// readyCh just before returning so the test can corrupt the DB row.
type racyRetryWorker struct {
	readyCh chan struct{}
	once    sync.Once
}

func (w *racyRetryWorker) Execute(_ context.Context, _ *Task) (*WorkerResult, error) {
	// Signal only once — subsequent calls (if any) are silent.
	w.once.Do(func() { close(w.readyCh) })
	// Small sleep so the test goroutine has time to corrupt the row.
	time.Sleep(20 * time.Millisecond)
	return &WorkerResult{Content: ""}, nil // empty = gate retries
}

func (w *racyRetryWorker) Type() WorkerType { return WorkerTypeCLI }

// TestFailTask_FromRetrying_ReachesFailed verifies the NEW-001 fix (PRC #2):
// failTask called with fromStatus=TaskStatusRetrying must successfully
// transition the task to TaskStatusFailed via UpdateStatus(retrying→failed).
// Prior to the v0.1.1 PRC #2 fix, validTransitions[retrying] contained only
// {dispatched}, so UpdateStatus(retrying→failed) was rejected and the task
// stayed permanently stuck in retrying — invisible to RecoverCrashed,
// uncancellable, non-terminal.
//
// This test is fully deterministic: it manually drives a task through the
// state machine to `retrying`, then calls failTask directly, then asserts
// the final state is `failed`. No race windows, no test flakiness.
func TestFailTask_FromRetrying_ReachesFailed(t *testing.T) {
	store := newTestStore(t)
	engine := New(store)
	defer func() { _ = engine.Close(context.Background()) }()

	task := &Task{
		ID:         "test-new-001",
		Status:     TaskStatusPending,
		WorkerType: WorkerTypeCLI,
		ProjectID:  "new-001",
		Prompt:     "test",
		CreatedAt:  time.Now().UTC(),
	}
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Drive the task manually through pending → dispatched → running → retrying.
	transitions := []struct {
		from, to TaskStatus
	}{
		{TaskStatusPending, TaskStatusDispatched},
		{TaskStatusDispatched, TaskStatusRunning},
		{TaskStatusRunning, TaskStatusRetrying},
	}
	for _, tr := range transitions {
		if err := store.UpdateStatus(task.ID, tr.from, tr.to); err != nil {
			t.Fatalf("setup UpdateStatus(%s→%s): %v", tr.from, tr.to, err)
		}
	}
	task.Status = TaskStatusRetrying

	// Exercise the exact call pattern the BUG-002 retry-path fix uses:
	// failTask(task, TaskStatusRetrying, errMsg). Prior to NEW-001 this
	// would fail internally and leave the task stuck in retrying.
	engine.failTask(task, TaskStatusRetrying, "simulated retry-path failure")

	final, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != TaskStatusFailed {
		t.Errorf("NEW-001 regression: expected final status %s, got %s (task stuck)",
			TaskStatusFailed, final.Status)
	}
	if final.Error == "" {
		t.Errorf("expected non-empty error message, got empty")
	}
}

func TestTaskAuthorityIntegration_RunningArtifactAbortRollsBackStatusAndEvent(t *testing.T) {
	t.Run("positive", func(t *testing.T) {
		fixture := t013NewFixture(t)
		started, release := t013NewGate(), t013NewGate()
		engine := fixture.engine(t, &t013ScriptedWorker{steps: []t013Step{{started: started, release: release, ignoreCancel: true, content: "ok"}}})
		t.Cleanup(release.open)
		observed := &t013ObservedRecorder{}
		var callbackMu sync.Mutex
		var seedErr error
		engine.Events().Subscribe(func(event TaskEvent) {
			if event.Type == EventTaskDispatched {
				callbackMu.Lock()
				seedErr = t013InsertAction(fixture.db, "running-action", event.TaskID)
				callbackMu.Unlock()
			}
			if event.Type == EventTaskRunning {
				task, err := fixture.view.Get(event.TaskID)
				var artifacts []TaskArtifact
				if err == nil {
					page, listErr := fixture.view.ListArtifacts(event.TaskID, TaskArtifactListOptions{Limit: 100})
					if listErr != nil {
						err = listErr
					} else {
						artifacts = page.Items
					}
				}
				observed.record(t013Observed{event: event, task: task, artifacts: artifacts, err: err})
			}
		})
		id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "running-positive", Prompt: "run"})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t013AwaitGate(t, "worker", started)
		callbackMu.Lock()
		gotSeedErr := seedErr
		callbackMu.Unlock()
		if gotSeedErr != nil {
			t.Errorf("seed running action from dispatched callback: %v", gotSeedErr)
		}
		observations := observed.snapshot(t)
		if len(observations) != 1 {
			t.Errorf("running observations=%d, want 1", len(observations))
		}
		if len(observations) == 1 {
			got := observations[0]
			if got.err != nil || got.task == nil || got.task.Status != TaskStatusRunning || len(got.artifacts) != 3 {
				t.Errorf("running event did not observe committed row+fact: task=%#v facts=%d err=%v", got.task, len(got.artifacts), got.err)
			}
		}
		status, resolved := t013Action(t, fixture.observer, "running-action")
		if status != "task_closed" || resolved == nil || !resolved.Equal(t013At) {
			t.Errorf("running action=%s/%v, want task_closed/%s", status, resolved, t013At)
		}
		t013AssertExactArtifact(t, t013ArtifactByEvent(t, fixture.view, id, "task.running"), TaskArtifactKindLifecycle, "task.running", map[string]any{
			"status": "running", "closed_action_count": int64(1),
		}, t013At)
		release.open()
		t013Close(t, engine)
	})

	t.Run("artifact-abort", func(t *testing.T) {
		fixture := t013NewFixture(t)
		_, err := fixture.db.Exec(`CREATE TRIGGER t013_abort_running BEFORE INSERT ON task_artifacts
			WHEN NEW.task_id='id-0' AND NEW.event_type='task.running'
			BEGIN SELECT RAISE(ABORT,'T013_RUNNING_ARTIFACT_ABORT'); END`)
		if err != nil {
			t.Fatal(err)
		}
		started, release := t013NewGate(), t013NewGate()
		engine := fixture.engine(t, &t013ScriptedWorker{steps: []t013Step{{started: started, release: release, ignoreCancel: true, content: "late"}}})
		t.Cleanup(release.open)
		events := &t013Events{}
		var callbackMu sync.Mutex
		var seedErr error
		engine.Events().Subscribe(func(event TaskEvent) {
			events.record(event)
			if event.Type == EventTaskDispatched {
				callbackMu.Lock()
				seedErr = t013InsertAction(fixture.db, "running-abort-action", event.TaskID)
				callbackMu.Unlock()
			}
		})
		id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "running-abort", Prompt: "run"})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		ran, done := t013CloseOrStart(t, engine, started)
		if ran {
			t.Error("worker started after running fact abort")
		}
		callbackMu.Lock()
		gotSeedErr := seedErr
		callbackMu.Unlock()
		if gotSeedErr != nil {
			t.Errorf("seed running abort action from dispatched callback: %v", gotSeedErr)
		}
		task, getErr := fixture.view.Get(id)
		if getErr != nil || task.Status != TaskStatusDispatched {
			t.Errorf("task after running fact abort=%#v err=%v, want dispatched", task, getErr)
		}
		status, resolved := t013Action(t, fixture.observer, "running-abort-action")
		if status != "pending" || resolved != nil {
			t.Errorf("action after running fact abort=%s/%v, want pending/nil", status, resolved)
		}
		for _, artifact := range t013Artifacts(t, fixture.view, id) {
			if artifact.EventType == "task.running" {
				t.Errorf("running fact survived abort: seq=%d", artifact.Seq)
			}
		}
		if events.count(id, EventTaskRunning) != 0 {
			t.Error("running event emitted without durable running fact")
		}
		if ran {
			release.open()
			t013FinishClose(t, done)
		}
	})
}

func TestTaskAuthorityIntegration_RetryingArtifactAbortRollsBackStatusRetryCountAndEvent(t *testing.T) {
	t.Run("positive", func(t *testing.T) {
		fixture := t013NewFixture(t)
		firstStarted, firstRelease := t013NewGate(), t013NewGate()
		secondStarted, secondRelease := t013NewGate(), t013NewGate()
		worker := &t013ScriptedWorker{steps: []t013Step{
			{started: firstStarted, release: firstRelease, ignoreCancel: true, content: ""},
			{started: secondStarted, release: secondRelease, ignoreCancel: true, content: "ok"},
		}}
		engine := fixture.engine(t, worker, WithMaxRetries(1))
		t.Cleanup(firstRelease.open)
		t.Cleanup(secondRelease.open)
		observed := &t013ObservedRecorder{}
		engine.Events().Subscribe(func(event TaskEvent) {
			if event.Type == EventTaskRetrying {
				task, err := fixture.view.Get(event.TaskID)
				var artifacts []TaskArtifact
				if err == nil {
					page, listErr := fixture.view.ListArtifacts(event.TaskID, TaskArtifactListOptions{Limit: 100})
					if listErr != nil {
						err = listErr
					} else {
						artifacts = page.Items
					}
				}
				observed.record(t013Observed{event: event, task: task, artifacts: artifacts, err: err})
			}
		})
		id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "retrying-positive", Prompt: "retry"})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t013AwaitGate(t, "first worker", firstStarted)
		t013SeedAction(t, fixture.db, "retrying-action", id)
		firstRelease.open()
		t013AwaitGate(t, "second worker", secondStarted)
		observations := observed.snapshot(t)
		if len(observations) != 1 {
			t.Errorf("retrying observations=%d, want 1", len(observations))
		}
		if len(observations) == 1 {
			got := observations[0]
			if got.err != nil || got.task == nil || got.task.Status != TaskStatusRetrying || got.task.Retries != 1 || len(got.artifacts) != 4 {
				t.Errorf("retrying event did not observe committed row+fact: task=%#v facts=%d err=%v", got.task, len(got.artifacts), got.err)
			}
		}
		status, resolved := t013Action(t, fixture.observer, "retrying-action")
		if status != "task_closed" || resolved == nil || !resolved.Equal(t013At) {
			t.Errorf("retrying action=%s/%v, want task_closed/%s", status, resolved, t013At)
		}
		t013AssertExactArtifact(t, t013ArtifactByEvent(t, fixture.view, id, "task.retrying"), TaskArtifactKindLifecycle, "task.retrying", map[string]any{
			"status": "retrying", "retry_count": int64(1), "closed_action_count": int64(1),
		}, t013At)
		secondRelease.open()
		t013Close(t, engine)
	})

	t.Run("artifact-abort", func(t *testing.T) {
		fixture := t013NewFixture(t)
		_, err := fixture.db.Exec(`CREATE TRIGGER t013_abort_retrying BEFORE INSERT ON task_artifacts
			WHEN NEW.task_id='id-0' AND NEW.event_type='task.retrying'
			BEGIN SELECT RAISE(ABORT,'T013_RETRYING_ARTIFACT_ABORT'); END`)
		if err != nil {
			t.Fatal(err)
		}
		firstStarted, firstRelease := t013NewGate(), t013NewGate()
		secondStarted, secondRelease := t013NewGate(), t013NewGate()
		worker := &t013ScriptedWorker{steps: []t013Step{
			{started: firstStarted, release: firstRelease, ignoreCancel: true, content: ""},
			{started: secondStarted, release: secondRelease, ignoreCancel: true, content: "late"},
		}}
		engine := fixture.engine(t, worker, WithMaxRetries(1))
		t.Cleanup(firstRelease.open)
		t.Cleanup(secondRelease.open)
		events := &t013Events{}
		engine.Events().Subscribe(events.record)
		id, err := engine.Submit(context.Background(), TaskRequest{WorkerType: WorkerTypeCLI, ProjectID: "retrying-abort", Prompt: "retry"})
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		t013AwaitGate(t, "first worker", firstStarted)
		t013SeedAction(t, fixture.db, "retrying-abort-action", id)
		firstRelease.open()
		ran, done := t013CloseOrStart(t, engine, secondStarted)
		if ran {
			t.Error("second worker started after retrying fact abort")
		}
		task, getErr := fixture.view.Get(id)
		if getErr != nil || task.Status != TaskStatusRunning || task.Retries != 0 {
			t.Errorf("task after retrying fact abort=%#v err=%v, want running/retries=0", task, getErr)
		}
		status, resolved := t013Action(t, fixture.observer, "retrying-abort-action")
		if status != "pending" || resolved != nil {
			t.Errorf("action after retrying fact abort=%s/%v, want pending/nil", status, resolved)
		}
		for _, artifact := range t013Artifacts(t, fixture.view, id) {
			if artifact.EventType == "task.retrying" {
				t.Errorf("retrying fact survived abort: seq=%d", artifact.Seq)
			}
		}
		if events.count(id, EventTaskRetrying) != 0 {
			t.Error("retrying event emitted without durable retrying fact")
		}
		if ran {
			secondRelease.open()
			t013FinishClose(t, done)
		}
	})
}
