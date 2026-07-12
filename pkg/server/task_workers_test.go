package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/driver"
	"github.com/thebtf/aimux/pkg/executor/picker"
	"github.com/thebtf/aimux/pkg/executor/review"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/server/budget"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

func TestTenantAwareSubtaskLoomEnforcesTenantQuota(t *testing.T) {
	srv := testServerWithLoom(t)
	projectID := "quota-project"
	submitBlockingLoomTaskForTenant(t, srv, projectID, "tenant-a")

	client := tenantAwareSubtaskLoom{
		engine: srv.loom,
		quotaFor: func(tenantID string) *loom.TenantQuotaConfig {
			if tenantID == "tenant-a" {
				return &loom.TenantQuotaConfig{MaxLoomTasksQueued: 1}
			}
			return &loom.TenantQuotaConfig{MaxLoomTasksQueued: 10}
		},
	}

	ctxA, cancelA := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelA()
	_, err := client.Submit(ctxA, loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		TenantID:   "tenant-a",
		Prompt:     "over quota",
	})
	if !errors.Is(err, loom.ErrLoomQuotaExceeded) {
		t.Fatalf("tenant-a Submit error = %v, want ErrLoomQuotaExceeded", err)
	}

	ctxB, cancelB := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelB()
	tenantBTaskID, err := client.Submit(ctxB, loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  projectID,
		TenantID:   "tenant-b",
		Prompt:     "within quota",
	})
	if err != nil {
		t.Fatalf("tenant-b Submit returned error: %v", err)
	}
	if tenantBTaskID == "" {
		t.Fatal("tenant-b task ID is empty")
	}
}

func TestTenantAwareSubtaskLoomGetContextScopesTenant(t *testing.T) {
	srv := testServerWithLoom(t)
	projectID := "scoped-get-project"
	taskID, _ := submitBlockingLoomTaskForTenant(t, srv, projectID, "tenant-a")
	client := tenantAwareSubtaskLoom{engine: srv.loom}

	tenantBCtx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-b"})
	if _, err := client.GetContext(tenantBCtx, taskID); !errors.Is(err, loom.ErrTaskNotFound) {
		t.Fatalf("tenant-b GetContext error = %v, want ErrTaskNotFound", err)
	}

	tenantACtx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-a"})
	task, err := client.GetContext(tenantACtx, taskID)
	if err != nil {
		t.Fatalf("tenant-a GetContext returned error: %v", err)
	}
	if task.ID != taskID {
		t.Fatalf("task ID = %q, want %q", task.ID, taskID)
	}
}

func TestAdaptReviewPassOutputFailsClosedOnMalformedJSON(t *testing.T) {
	task := &loom.Task{Metadata: map[string]any{"review_pass": "structural"}}

	content, meta, err := adaptReviewPassOutput(task, "not json")
	if err == nil {
		t.Fatal("expected malformed review pass output to fail closed")
	}
	if content != "" {
		t.Fatalf("content = %q, want empty on error", content)
	}
	if meta != nil {
		t.Fatalf("meta = %v, want nil on error", meta)
	}
	if !strings.Contains(err.Error(), "structured JSON") {
		t.Fatalf("error = %q, want structured JSON detail", err)
	}
}

func TestAdaptReviewPassOutputAcceptsStructuredJSON(t *testing.T) {
	input := `{"findings":[],"summary":"review pass complete sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 PRIVATE_REASONING_SENTINEL"}`

	content, meta, err := adaptReviewPassOutput(&loom.Task{}, input)
	if err != nil {
		t.Fatalf("adaptReviewPassOutput: %v", err)
	}
	if strings.Contains(content, "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") || strings.Contains(content, "PRIVATE_REASONING_SENTINEL") {
		t.Fatalf("content leaked review-private text: %q", content)
	}
	if !json.Valid([]byte(content)) {
		t.Fatalf("content is not canonical JSON: %q", content)
	}
	if meta == nil {
		t.Fatal("meta is nil, want empty map")
	}
	if len(meta) != 0 {
		t.Fatalf("meta = %v, want empty map", meta)
	}
}

func TestAdaptReviewPassOutputRejectsEmptySummary(t *testing.T) {
	input := `{"findings":[],"summary":"   "}`

	content, meta, err := adaptReviewPassOutput(&loom.Task{}, input)
	if err == nil {
		t.Fatal("expected empty summary to be rejected")
	}
	if content != "" {
		t.Fatalf("content = %q, want empty on error", content)
	}
	if meta != nil {
		t.Fatalf("meta = %v, want nil on error", meta)
	}
	if !strings.Contains(err.Error(), "non-empty summary") {
		t.Fatalf("error = %q, want non-empty summary detail", err)
	}
}

func TestProfileTaskWorkerReviewLeavesRedactBeforeStatusContent(t *testing.T) {
	srv := testServerWithLoom(t)
	srv.registry.SetAvailable("codex", true)
	line := 17
	outputs := map[loom.WorkerType]string{
		review.WorkerTypeReviewStructural:  `{"summary":"structural sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 PRIVATE_REASONING_SENTINEL","findings":[{"severity":"error","file":"pkg/structural.go","line":17,"body":"public structural finding"}]}`,
		review.WorkerTypeReviewBehavioural: `{"summary":"behavioural sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 hidden-thought-trace","findings":[{"severity":"warning","file":"pkg/behaviour.go","line":17,"body":"public behavioural finding"}]}`,
		review.WorkerTypeReviewAdversarial: `{"summary":"adversarial sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 internal-reasoning","findings":[{"severity":"info","file":"pkg/adversarial.go","line":17,"body":"public adversarial finding"}]}`,
	}
	for workerType, raw := range outputs {
		srv.loom.RegisterWorker(workerType, profileTaskWorker{
			server:     srv,
			workerType: workerType,
			taskClass:  "review",
			defaultCLI: "codex",
			adapt:      adaptReviewPassOutput,
			dispatchLeaf: func(_ context.Context, _ string, _ picker.TaskSpec) (string, error) {
				return raw, nil
			},
		})
		taskID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
			WorkerType: workerType,
			ProjectID:  "review-redaction",
			Prompt:     "review",
			Metadata:   map[string]any{"review_pass": string(workerType)},
		})
		if err != nil {
			t.Fatalf("Submit %s: %v", workerType, err)
		}
		task := waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusCompleted)
		status := loomStatusResult(srv, task, budget.BudgetParams{IncludeContent: true}, taskID)
		content, _ := status["content"].(string)
		for _, leaked := range []string{"sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", "PRIVATE_REASONING_SENTINEL", "hidden-thought-trace", "internal-reasoning"} {
			if strings.Contains(content, leaked) || strings.Contains(fmt.Sprint(task.Metadata), leaked) {
				t.Fatalf("%s durable leaf leaked %q", workerType, leaked)
			}
		}
		var pass struct {
			Findings []review.Finding `json:"findings"`
		}
		if err := json.Unmarshal([]byte(content), &pass); err != nil {
			t.Fatalf("%s status content is not JSON: %v", workerType, err)
		}
		if len(pass.Findings) != 1 || pass.Findings[0].Line == nil || *pass.Findings[0].Line != line || pass.Findings[0].File == "" || pass.Findings[0].Body == "" {
			t.Fatalf("%s finding = %#v, want retained public finding", workerType, pass.Findings)
		}
	}
}

func TestRuntimeEventsFromOutputLine_NormalizesStructuredJSONL(t *testing.T) {
	textEvents := runtimeEventsFromOutputLine("jsonl", `{"type":"item.completed","item":{"type":"agent_message","text":"hello"}}`)
	if len(textEvents) != 1 {
		t.Fatalf("text events len = %d; want 1", len(textEvents))
	}
	if textEvents[0].EventType != "text_delta" || textEvents[0].Channel != "stdout" || textEvents[0].Summary != "hello" {
		t.Fatalf("text event = %#v; want text_delta/stdout hello", textEvents[0])
	}

	statusEvents := runtimeEventsFromOutputLine("jsonl", `{"type":"turn.started"}`)
	if len(statusEvents) != 1 || statusEvents[0].EventType != "status" || statusEvents[0].Channel != "stdout" {
		t.Fatalf("status event = %#v; want status/stdout", statusEvents)
	}

	rawEvents := runtimeEventsFromOutputLine("jsonl", `{"type":"unknown.future","payload":"kept"}`)
	if len(rawEvents) != 1 || rawEvents[0].EventType != "raw" || rawEvents[0].Channel != "stdout" {
		t.Fatalf("raw event = %#v; want raw/stdout fallback", rawEvents)
	}
}

func TestRuntimeEventsFromOutputLine_PreservesPlainLinesAsRawStdout(t *testing.T) {
	events := runtimeEventsFromOutputLine("text", "plain harness line")
	if len(events) != 1 {
		t.Fatalf("events len = %d; want 1", len(events))
	}
	if events[0].EventType != "raw" || events[0].Channel != "stdout" || events[0].Summary != "plain harness line" {
		t.Fatalf("plain line event = %#v; want raw/stdout truthful fallback", events[0])
	}
}

func TestProfileTaskWorkerProgressSinkPersistsRuntimeSliceAndProgressTail(t *testing.T) {
	srv := testServerWithLoom(t)
	_, projectID := projectCtxAndID("proj-runtime-progress-sink")
	taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")
	waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusRunning)

	worker := profileTaskWorker{server: srv}
	sink := worker.progressSink(taskID, "jsonl")
	if sink == nil {
		t.Fatal("progress sink is nil")
	}
	sink(`{"type":"item.completed","item":{"type":"agent_message","text":"hello sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"}}`)

	page, err := srv.loom.ListArtifacts(taskID, loom.TaskArtifactListOptions{
		Kinds:      []loom.TaskArtifactKind{loom.TaskArtifactKindRuntime},
		EventTypes: []string{"text_delta"},
		Channels:   []string{"stdout"},
	})
	if err != nil {
		t.Fatalf("ListArtifacts runtime: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("runtime items = %#v; want one text_delta", page.Items)
	}
	item := page.Items[0]
	if item.Kind != loom.TaskArtifactKindRuntime || item.EventType != "text_delta" || item.Channel != "stdout" {
		t.Fatalf("runtime identity = kind %q event_type %q channel %q", item.Kind, item.EventType, item.Channel)
	}
	if strings.Contains(item.Summary, "sk-proj-") || !item.Redacted {
		t.Fatalf("runtime redaction failed: summary=%q redacted=%v", item.Summary, item.Redacted)
	}

	task, err := srv.loom.Get(taskID)
	if err != nil {
		t.Fatalf("loom.Get(%s): %v", taskID, err)
	}
	if task.LastOutputLine == "" || strings.Contains(task.LastOutputLine, "sk-proj-") {
		t.Fatalf("progress tail = %q; want parsed redacted text", task.LastOutputLine)
	}
}

func TestProfileTaskWorkerProgressSinksRejectUnnormalizedStructuredOutput(t *testing.T) {
	for _, output := range []struct {
		name   string
		format string
		line   string
	}{
		{
			name:   "private_json",
			format: "json",
			line:   `{"reasoning":"private chain","api_key":"sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"}`,
		},
		{
			name:   "unsupported_jsonl",
			format: "jsonl",
			line:   `{"type":"future.private","payload":"do not expose"}`,
		},
	} {
		for _, sinkKind := range []string{"direct", "workflow"} {
			t.Run(output.name+"/"+sinkKind, func(t *testing.T) {
				srv := testServerWithLoom(t)
				_, projectID := projectCtxAndID("proj-runtime-progress-rejected")
				taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")
				waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusRunning)

				worker := profileTaskWorker{server: srv}
				switch sinkKind {
				case "direct":
					worker.progressSink(taskID, output.format)(output.line)
				case "workflow":
					task, err := srv.loom.Get(taskID)
					if err != nil {
						t.Fatalf("loom.Get(%s): %v", taskID, err)
					}
					(&workflowRecipeExecutorSender{server: srv, task: task}).progressSink(output.format)(output.line)
				}

				task, err := srv.loom.Get(taskID)
				if err != nil {
					t.Fatalf("loom.Get(%s): %v", taskID, err)
				}
				if task.ProgressUpdatedAt != nil || task.LastOutputLine != "" {
					t.Fatalf("rejected structured output persisted as progress: updated=%v line=%q", task.ProgressUpdatedAt, task.LastOutputLine)
				}
				artifacts, err := srv.loom.ListArtifacts(taskID, loom.TaskArtifactListOptions{Kinds: []loom.TaskArtifactKind{loom.TaskArtifactKindRuntime}})
				if err != nil {
					t.Fatalf("ListArtifacts runtime: %v", err)
				}
				if len(artifacts.Items) != 0 {
					t.Fatalf("rejected structured output persisted as runtime artifacts: %#v", artifacts.Items)
				}
			})
		}
	}
}

func TestProfileTaskWorkerProgressSinksKeepTextAndSafeJSON(t *testing.T) {
	for _, output := range []struct {
		name   string
		format string
		line   string
		want   string
	}{
		{name: "blank_format", format: "", line: "default progress", want: "default progress"},
		{name: "text", format: "text", line: "plain progress", want: "plain progress"},
		{name: "json", format: "json", line: `{"content":"safe structured progress"}`, want: "safe structured progress"},
	} {
		for _, sinkKind := range []string{"direct", "workflow"} {
			t.Run(output.name+"/"+sinkKind, func(t *testing.T) {
				srv := testServerWithLoom(t)
				_, projectID := projectCtxAndID("proj-runtime-progress-safe")
				taskID, _ := submitBlockingLoomTask(t, srv, projectID, "")
				waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusRunning)

				worker := profileTaskWorker{server: srv}
				switch sinkKind {
				case "direct":
					worker.progressSink(taskID, output.format)(output.line)
				case "workflow":
					task, err := srv.loom.Get(taskID)
					if err != nil {
						t.Fatalf("loom.Get(%s): %v", taskID, err)
					}
					(&workflowRecipeExecutorSender{server: srv, task: task}).progressSink(output.format)(output.line)
				}

				task, err := srv.loom.Get(taskID)
				if err != nil {
					t.Fatalf("loom.Get(%s): %v", taskID, err)
				}
				if task.ProgressUpdatedAt == nil || task.LastOutputLine != output.want {
					t.Fatalf("safe output progress = updated=%v line=%q, want non-nil/%q", task.ProgressUpdatedAt, task.LastOutputLine, output.want)
				}
				artifacts, err := srv.loom.ListArtifacts(taskID, loom.TaskArtifactListOptions{Kinds: []loom.TaskArtifactKind{loom.TaskArtifactKindRuntime}})
				if err != nil {
					t.Fatalf("ListArtifacts runtime: %v", err)
				}
				if len(artifacts.Items) != 1 || artifacts.Items[0].Summary != output.want {
					t.Fatalf("safe output runtime artifacts = %#v; want one normalized %q artifact", artifacts.Items, output.want)
				}
			})
		}
	}
}

type t019ServerFailingSink struct {
	mu       sync.Mutex
	attempts int
}

func (sink *t019ServerFailingSink) AppendRuntimeEvents(context.Context, []workerruntime.RuntimeEvent) error {
	sink.mu.Lock()
	sink.attempts++
	sink.mu.Unlock()
	return errors.New("SQLITE_BUSY: injected server outage")
}

func (sink *t019ServerFailingSink) Checkpoint(context.Context) error { return nil }

func (sink *t019ServerFailingSink) Attempts() int {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.attempts
}

func TestProfileTaskWorker_ArtifactSinkFailureCancelsOnceAndSkipsProviderFallback(t *testing.T) {
	srv := testServerWithLoom(t)
	profiles := map[string]*config.CLIProfile{}
	for _, cli := range []string{"codex", "grok"} {
		binary := fakeExecutable(t, t.TempDir(), cli)
		profiles[cli] = &config.CLIProfile{
			Name:         cli,
			Binary:       binary,
			ResolvedPath: binary,
			OutputFormat: "text",
			Capabilities: []string{"code", "task", "review"},
		}
	}
	srv.cfg.CLIProfiles = profiles
	srv.cfg.Executor.Picker = picker.DefaultPickerConfig()
	srv.registry = driver.NewRegistry(profiles)
	for cli := range profiles {
		srv.registry.SetAvailable(cli, true)
	}
	srv.fallbackPicker = buildFallbackPicker(srv)

	sink := &t019ServerFailingSink{}
	var dispatches atomic.Int32
	worker := profileTaskWorker{
		server:     srv,
		workerType: loom.WorkerTypeCLI,
		taskClass:  "code",
		defaultCLI: "codex",
		newEventWriter: func(string) (*workerruntime.EventWriter, error) {
			var elapsed time.Duration
			config := workerruntime.DefaultEventWriterConfig(sink)
			config.FlushWindow = time.Nanosecond
			config.Now = func() time.Time { return time.Unix(0, 0).Add(elapsed) }
			config.Wait = func(_ context.Context, delay time.Duration) error {
				elapsed += delay
				return nil
			}
			return workerruntime.NewEventWriter(config)
		},
		dispatchLeaf: func(ctx context.Context, _ string, spec picker.TaskSpec) (string, error) {
			dispatches.Add(1)
			if spec.OnOutput == nil {
				return "", errors.New("missing output callback")
			}
			spec.OnOutput("streamed before outage")
			<-ctx.Done()
			return "", extypes.NewCanceled("provider cancelled", ctx.Err())
		},
	}
	task := &loom.Task{
		ID:       "artifact-sink-failure-task",
		CLI:      "codex",
		Prompt:   "exercise bounded writer failure",
		Metadata: map[string]any{"fallback_enabled": true, "max_attempts": 3},
	}

	type outcome struct {
		result *loom.WorkerResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := worker.Execute(context.Background(), task)
		done <- outcome{result: result, err: err}
	}()
	var got outcome
	select {
	case got = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not cancel after artifact sink retry exhaustion")
	}
	if got.result != nil {
		t.Fatalf("result = %#v, want nil", got.result)
	}
	var cliErr *extypes.CLIError
	if !errors.As(got.err, &cliErr) || cliErr.Code != extypes.CLIErrorCodeUnknown || cliErr.Message != "artifact_sink_unavailable" || cliErr.Retryable {
		t.Fatalf("error = %#v, want non-retryable Unknown artifact_sink_unavailable", got.err)
	}
	if dispatches.Load() != 1 {
		t.Fatalf("provider dispatches = %d, want exactly one with no fallback", dispatches.Load())
	}
	if sink.Attempts() != 8 {
		t.Fatalf("sink attempts = %d, want 8", sink.Attempts())
	}
}

type t019ServerRecordingSink struct {
	mu     sync.Mutex
	events []workerruntime.RuntimeEvent
}

func (sink *t019ServerRecordingSink) AppendRuntimeEvents(_ context.Context, batch []workerruntime.RuntimeEvent) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, batch...)
	return nil
}

func (sink *t019ServerRecordingSink) Checkpoint(context.Context) error { return nil }

func (sink *t019ServerRecordingSink) Providers() []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	providers := make([]string, len(sink.events))
	for i, event := range sink.events {
		providers[i] = event.Provider
	}
	return providers
}

func TestProfileTaskWorkerOutputSink_NormalizesJSONWithoutChangingText(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		line     string
		wantType string
		wantText bool
		redacted bool
	}{
		{
			name:     "json_is_structural",
			format:   "json",
			line:     `{"reasoning":"private chain","api_key":"sk-proj-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"}`,
			wantType: "provider_event_unknown",
			redacted: true,
		},
		{
			name:     "text_stays_text",
			format:   "text",
			line:     "plain output",
			wantType: "command_output_delta",
			wantText: true,
		},
		{
			name:     "blank_defaults_to_text",
			line:     "legacy output",
			wantType: "command_output_delta",
			wantText: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := &t019ServerRecordingSink{}
			writer, err := workerruntime.NewEventWriter(workerruntime.DefaultEventWriterConfig(sink))
			if err != nil {
				t.Fatalf("NewEventWriter: %v", err)
			}
			profileTaskWorker{}.writerOutputSink(writer, nil, "format-safety-task", "claude", tt.format)(tt.line)
			if err := writer.CloseAndFlush(context.Background()); err != nil {
				t.Fatalf("CloseAndFlush: %v", err)
			}

			sink.mu.Lock()
			events := append([]workerruntime.RuntimeEvent(nil), sink.events...)
			sink.mu.Unlock()
			if len(events) != 1 {
				t.Fatalf("events = %#v, want one", events)
			}
			event := events[0]
			if event.Type != tt.wantType || event.Redacted != tt.redacted {
				t.Fatalf("event = %#v, want type %q redacted=%v", event, tt.wantType, tt.redacted)
			}
			_, hasText := event.Payload["text"]
			if hasText != tt.wantText {
				t.Fatalf("event payload = %#v, text field present=%v want %v", event.Payload, hasText, tt.wantText)
			}
		})
	}
}

func TestProfileTaskWorkerOutputSink_UnsupportedFormatFailsClosed(t *testing.T) {
	sink := &t019ServerRecordingSink{}
	writer, err := workerruntime.NewEventWriter(workerruntime.DefaultEventWriterConfig(sink))
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	profileTaskWorker{}.writerOutputSink(writer, nil, "format-safety-task", "future-provider", "yaml")("private: do not persist")

	if err := writer.CloseAndFlush(context.Background()); !errors.Is(err, workerruntime.ErrArtifactSinkUnavailable) {
		t.Fatalf("CloseAndFlush error = %v, want ErrArtifactSinkUnavailable", err)
	}
}

func TestProfileTaskWorker_FallbackCallbacksPreserveAttemptProviderIdentity(t *testing.T) {
	srv := testServerWithLoom(t)
	profiles := map[string]*config.CLIProfile{}
	for _, cli := range []string{"codex", "grok"} {
		binary := fakeExecutable(t, t.TempDir(), cli)
		profiles[cli] = &config.CLIProfile{
			Name:         cli,
			Binary:       binary,
			ResolvedPath: binary,
			OutputFormat: "text",
			Capabilities: []string{"code", "task", "review"},
		}
	}
	srv.cfg.CLIProfiles = profiles
	srv.cfg.Executor.Picker = picker.DefaultPickerConfig()
	srv.registry = driver.NewRegistry(profiles)
	for cli := range profiles {
		srv.registry.SetAvailable(cli, true)
	}
	srv.fallbackPicker = buildFallbackPicker(srv)

	sink := &t019ServerRecordingSink{}
	writer, err := workerruntime.NewEventWriter(workerruntime.DefaultEventWriterConfig(sink))
	if err != nil {
		t.Fatalf("NewEventWriter: %v", err)
	}
	worker := profileTaskWorker{
		server: srv,
		dispatchLeaf: func(_ context.Context, cli string, spec picker.TaskSpec) (string, error) {
			if spec.OnOutput == nil {
				return "", errors.New("missing output callback")
			}
			spec.OnOutput(cli + " output")
			if cli == "codex" {
				return "", extypes.NewRateLimit("primary rate limited", nil)
			}
			return "grok final", nil
		},
	}
	raw, selected, failed, err := worker.dispatch(
		context.Background(),
		"codex",
		map[string]any{"fallback_enabled": true, "max_attempts": 2},
		picker.TaskSpec{TaskClass: "code", Prompt: "fallback identity"},
		writer,
		nil,
		"fallback-provider-task",
	)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if raw != "grok final" || selected != "grok" || len(failed) != 1 || failed[0].CLI != "codex" {
		t.Fatalf("dispatch result = raw %q selected %q failed %#v", raw, selected, failed)
	}
	if err := writer.CloseAndFlush(context.Background()); err != nil {
		t.Fatalf("CloseAndFlush: %v", err)
	}
	providers := sink.Providers()
	if len(providers) != 2 || providers[0] != "codex" || providers[1] != "grok" {
		t.Fatalf("persisted providers = %v, want [codex grok]", providers)
	}
}

func TestProfileTaskWorkerFallbackMetadataSanitizesFailedAttemptMessage(t *testing.T) {
	const secret = "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const privateReasoning = "PRIVATE_REASONING_SENTINEL"
	srv, db := testServerWithLoomDB(t)
	profiles := map[string]*config.CLIProfile{}
	for _, cli := range []string{"codex", "grok"} {
		binary := fakeExecutable(t, t.TempDir(), cli)
		profiles[cli] = &config.CLIProfile{Name: cli, Binary: binary, ResolvedPath: binary, OutputFormat: "text", Capabilities: []string{"code"}}
	}
	srv.cfg.CLIProfiles = profiles
	srv.cfg.Executor.Picker = picker.DefaultPickerConfig()
	srv.registry = driver.NewRegistry(profiles)
	for cli := range profiles {
		srv.registry.SetAvailable(cli, true)
	}
	srv.fallbackPicker = buildFallbackPicker(srv)
	sink := &t019ServerRecordingSink{}
	worker := profileTaskWorker{
		server:     srv,
		workerType: loom.WorkerTypeCLI,
		taskClass:  "code",
		defaultCLI: "codex",
		newEventWriter: func(string) (*workerruntime.EventWriter, error) {
			return workerruntime.NewEventWriter(workerruntime.DefaultEventWriterConfig(sink))
		},
		dispatchLeaf: func(_ context.Context, cli string, _ picker.TaskSpec) (string, error) {
			if cli == "codex" {
				return "", extypes.NewRateLimit("provider stderr: "+secret+" "+privateReasoning, nil)
			}
			return "safe fallback output", nil
		},
	}
	srv.loom.RegisterWorker(loom.WorkerTypeCLI, worker)
	taskID, err := srv.loom.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  "fallback-metadata",
		Prompt:     "test",
		Metadata:   map[string]any{"fallback_enabled": true, "max_attempts": 2},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	task := waitForTaskResourceStatus(t, srv, taskID, loom.TaskStatusCompleted)
	var metadataJSON string
	if err := db.QueryRow(`SELECT metadata FROM tasks WHERE id=?`, taskID).Scan(&metadataJSON); err != nil {
		t.Fatalf("query durable metadata: %v", err)
	}
	for name, value := range map[string]string{"sqlite": metadataJSON, "loom task": fmt.Sprintf("%v", task.Metadata), "public metadata": fmt.Sprintf("%v", taskSnapshotMetadataPayload(task.Metadata))} {
		if strings.Contains(value, secret) || strings.Contains(value, privateReasoning) {
			t.Fatalf("%s leaked fallback failure: %q", name, value)
		}
	}
	attempts, ok := task.Metadata["failed_attempts"].([]any)
	if !ok || len(attempts) != 1 {
		t.Fatalf("failed_attempts = %#v, want one attempt", task.Metadata["failed_attempts"])
	}
	attempt, ok := attempts[0].(map[string]any)
	if !ok || attempt["CLI"] != "codex" || attempt["Code"] != extypes.CLIErrorCodeRateLimit.String() {
		t.Fatalf("failed_attempt = %#v, want codex rate-limit provenance", attempts[0])
	}
}
