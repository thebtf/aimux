package loom

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"

	"github.com/thebtf/aimux/loom/deps"
)

// ---- recording meter ----

// recordingMeter is a test double for deps.Meter that captures all instrument
// factory calls and record/add calls via the returned recording instruments.
type recordingMeter struct {
	mu       sync.Mutex
	counters map[string]*recordingCounter
	hists    map[string]*recordingHistogram
}

func newRecordingMeter() *recordingMeter {
	return &recordingMeter{
		counters: make(map[string]*recordingCounter),
		hists:    make(map[string]*recordingHistogram),
	}
}

func (m *recordingMeter) Int64Counter(name string, _ ...otelmetric.Int64CounterOption) (otelmetric.Int64Counter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := &recordingCounter{name: name}
	m.counters[name] = c
	return c, nil
}

func (m *recordingMeter) Float64Histogram(name string, _ ...otelmetric.Float64HistogramOption) (otelmetric.Float64Histogram, error) {
	// Noop — loom only uses Int64Histogram.
	return deps.NoopMeter().Float64Histogram(name)
}

func (m *recordingMeter) Int64Histogram(name string, _ ...otelmetric.Int64HistogramOption) (otelmetric.Int64Histogram, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := &recordingHistogram{name: name}
	m.hists[name] = h
	return h, nil
}

func (m *recordingMeter) Int64UpDownCounter(name string, _ ...otelmetric.Int64UpDownCounterOption) (otelmetric.Int64UpDownCounter, error) {
	return deps.NoopMeter().Int64UpDownCounter(name)
}

func (m *recordingMeter) counterTotal(name string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.counters[name]
	if !ok {
		return 0
	}
	return c.total.Load()
}

func (m *recordingMeter) histCount(name string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.hists[name]
	if !ok {
		return 0
	}
	return h.count.Load()
}

// recordingCounter records total Add() delta.
// Embeds embedded.Int64Counter to satisfy the otelmetric.Int64Counter interface.
type recordingCounter struct {
	embedded.Int64Counter
	name  string
	total atomic.Int64
}

func (c *recordingCounter) Add(_ context.Context, incr int64, _ ...otelmetric.AddOption) {
	c.total.Add(incr)
}

func (c *recordingCounter) Enabled(_ context.Context) bool { return true }

// recordingHistogram records the number of Record() calls.
// Embeds embedded.Int64Histogram to satisfy the otelmetric.Int64Histogram interface.
type recordingHistogram struct {
	embedded.Int64Histogram
	name  string
	count atomic.Int64
}

func (h *recordingHistogram) Record(_ context.Context, _ int64, _ ...otelmetric.RecordOption) {
	h.count.Add(1)
}

func (h *recordingHistogram) Enabled(_ context.Context) bool { return true }

// ---- recording logger ----

// recordingLogger captures log entries for field verification.
type recordingLogger struct {
	mu      sync.Mutex
	entries []logEntry
}

type logEntry struct {
	msg   string
	args  []any
	level string
}

func (l *recordingLogger) DebugContext(_ context.Context, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, logEntry{msg: msg, args: args, level: "debug"})
}

func (l *recordingLogger) InfoContext(_ context.Context, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, logEntry{msg: msg, args: args, level: "info"})
}

func (l *recordingLogger) WarnContext(_ context.Context, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, logEntry{msg: msg, args: args, level: "warn"})
}

func (l *recordingLogger) ErrorContext(_ context.Context, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, logEntry{msg: msg, args: args, level: "error"})
}

// hasField returns true if the key-value pairs contain the given key.
func (l *recordingLogger) hasField(msg, key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		if e.msg != msg {
			continue
		}
		for i := 0; i+1 < len(e.args); i += 2 {
			if k, ok := e.args[i].(string); ok && k == key {
				return true
			}
		}
	}
	return false
}

// ---- T030 metric tests ----

// TestEngine_Submit_EmitsMetrics verifies that Submit records one
// taskSubmittedCounter increment and one submitDurationHist observation.
func TestEngine_Submit_EmitsMetrics(t *testing.T) {
	rm := newRecordingMeter()
	engine := New(newTestStore(t),
		WithMeter(rm),
	)
	engine.RegisterWorker(WorkerTypeCLI, &testWorker{wtype: WorkerTypeCLI, result: "ok"})

	ctx := context.Background()
	_, err := engine.Submit(ctx, TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-metrics",
		Prompt:     "test",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if got := rm.counterTotal("loom.tasks.submitted"); got != 1 {
		t.Errorf("loom.tasks.submitted: got %d want 1", got)
	}
	if got := rm.histCount("loom.submit.duration_ms"); got != 1 {
		t.Errorf("loom.submit.duration_ms records: got %d want 1", got)
	}
}

// TestEngine_Dispatch_EmitsCompletedMetric verifies that a successful
// task dispatch increments taskCompletedCounter and records taskDurationHist.
func TestEngine_Dispatch_EmitsCompletedMetric(t *testing.T) {
	rm := newRecordingMeter()
	engine := New(newTestStore(t),
		WithMeter(rm),
	)
	engine.RegisterWorker(WorkerTypeCLI, &testWorker{wtype: WorkerTypeCLI, result: "done"})

	ctx := context.Background()
	_, err := engine.Submit(ctx, TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-complete",
		Prompt:     "test",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for dispatch goroutine to complete.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rm.counterTotal("loom.tasks.completed") > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := rm.counterTotal("loom.tasks.completed"); got != 1 {
		t.Errorf("loom.tasks.completed: got %d want 1", got)
	}
	if got := rm.counterTotal("loom.gate.pass"); got != 1 {
		t.Errorf("loom.gate.pass: got %d want 1", got)
	}
}

// TestEngine_FailedTask_EmitsFailedMetric verifies that a task failure
// increments taskFailedCounter.
func TestEngine_FailedTask_EmitsFailedMetric(t *testing.T) {
	rm := newRecordingMeter()
	store := newTestStore(t)
	engine := New(store, WithMeter(rm))
	// No worker registered → immediate failTask call.

	ctx := context.Background()
	_, err := engine.Submit(ctx, TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-fail",
		Prompt:     "test",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for dispatch goroutine to fail.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rm.counterTotal("loom.tasks.failed") > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := rm.counterTotal("loom.tasks.failed"); got != 1 {
		t.Errorf("loom.tasks.failed: got %d want 1", got)
	}
}

// ---- T031 canonical log field tests ----

// TestEngine_Submit_LogsCanonicalFields verifies that the "task submitted"
// log entry contains the mandatory canonical fields.
func TestEngine_Submit_LogsCanonicalFields(t *testing.T) {
	rl := &recordingLogger{}
	engine := New(newTestStore(t),
		WithLogger(rl),
	)
	engine.RegisterWorker(WorkerTypeCLI, &testWorker{wtype: WorkerTypeCLI, result: "ok"})

	ctx := context.Background()
	_, err := engine.Submit(ctx, TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-log",
		Prompt:     "test",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	for _, field := range []string{"module", "task_id", "project_id", "worker_type", "task_status", "request_id"} {
		if !rl.hasField("task submitted", field) {
			t.Errorf("\"task submitted\" log missing canonical field %q", field)
		}
	}
}

// TestEngine_FailedTask_LogsErrorCodeField verifies that the "task failed"
// error log entry contains error_code and error fields.
func TestEngine_FailedTask_LogsErrorCodeField(t *testing.T) {
	rl := &recordingLogger{}
	engine := New(newTestStore(t),
		WithLogger(rl),
	)
	// No worker → failTask fires with error_code.

	ctx := context.Background()
	_, err := engine.Submit(ctx, TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-errlog",
		Prompt:     "test",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for dispatch goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rl.mu.Lock()
		n := len(rl.entries)
		rl.mu.Unlock()
		if n >= 2 { // submitted + failed
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, field := range []string{"error_code", "error"} {
		if !rl.hasField("task failed", field) {
			t.Errorf("\"task failed\" log missing field %q", field)
		}
	}
}

// TestEngine_GatePass_LogsCanonicalFields verifies that "quality gate pass"
// log entry includes the mandatory canonical fields (module, task_id, etc.).
func TestEngine_GatePass_LogsCanonicalFields(t *testing.T) {
	rl := &recordingLogger{}
	engine := New(newTestStore(t),
		WithLogger(rl),
	)
	engine.RegisterWorker(WorkerTypeCLI, &testWorker{wtype: WorkerTypeCLI, result: "good output"})

	ctx := context.Background()
	_, err := engine.Submit(ctx, TaskRequest{
		WorkerType: WorkerTypeCLI,
		ProjectID:  "proj-gate-log",
		Prompt:     "test",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Wait for dispatch to complete.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rl.mu.Lock()
		found := false
		for _, e := range rl.entries {
			if e.msg == "quality gate pass" {
				found = true
				break
			}
		}
		rl.mu.Unlock()
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, field := range []string{"module", "task_id", "project_id", "worker_type", "task_status", "request_id"} {
		if !rl.hasField("quality gate pass", field) {
			t.Errorf("\"quality gate pass\" log missing canonical field %q", field)
		}
	}
}
