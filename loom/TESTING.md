# loom Testing Guide

Patterns for writing hermetic, deterministic tests for code that uses the loom
library. Every pattern shown here is used in the loom package's own test suite.

References: [CONTRACT.md](CONTRACT.md) | [PLAYBOOK.md](PLAYBOOK.md) | [CHANGELOG.md](CHANGELOG.md)

---

## Testing Philosophy

**Hermetic tests (NFR-3).** Unit tests MUST NOT:
- Spawn real subprocesses (except in the subprocess example itself).
- Make real network calls.
- Depend on wall-clock time or random IDs.
- Depend on filesystem state outside the test's temp directory.

**Why hermetic matters for loom specifically.** The engine runs dispatch in a
background goroutine. Non-hermetic tests introduce timing-dependent flakiness
that is hard to reproduce. Inject `FakeClock` and `SequentialIDGenerator` to
eliminate all time and ID sources from the test.

**Unit test = single package isolation.** Each package under `loom/` is tested
in isolation. Workers are tested with injected mock runners, not real processes.
The engine is tested with an in-memory SQLite database and stub workers.

---

## In-Memory SQLite Pattern

Use a named in-memory SQLite database per test. The `cache=shared&mode=memory`
parameters ensure all connections within the same test share the same in-memory
database (plain `":memory:"` gives each connection its own isolated database,
which breaks tests that open multiple connections).

```go
func newTestDB(t *testing.T) *sql.DB {
    t.Helper()
    dbName := "file:" + t.Name() + "?cache=shared&mode=memory"
    db, err := sql.Open("sqlite", dbName)
    if err != nil {
        t.Fatal(err)
    }
    // Single connection forces pool to use the shared in-memory DB.
    db.SetMaxOpenConns(1)
    t.Cleanup(func() { db.Close() })
    return db
}

func newTestStore(t *testing.T) *loom.TaskStore {
    t.Helper()
    db := newTestDB(t)
    store, err := loom.NewTaskStore(db)
    if err != nil {
        t.Fatal(err)
    }
    return store
}
```

Each test gets a fresh, isolated database. Tests run in parallel safely because
each test name produces a unique database URI.

```go
func TestSomething(t *testing.T) {
    store := newTestStore(t)
    engine := loom.New(store)
    // ...
}
```

---

## FakeClock + SequentialIDGenerator

Inject `deps.FakeClock` and `deps.SequentialIDGenerator` for fully deterministic
tests (US6).

```go
import (
    "testing"
    "time"

    "github.com/thebtf/aimux/loom"
    "github.com/thebtf/aimux/loom/deps"
)

func TestDeterministicEngine(t *testing.T) {
    epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
    clock := deps.NewFakeClock(epoch)
    idGen := deps.NewSequentialIDGenerator()

    engine := loom.New(newTestStore(t),
        loom.WithClock(clock),
        loom.WithIDGenerator(idGen),
    )
    engine.RegisterWorker(loom.WorkerTypeCLI, myStubWorker)

    ctx := context.Background()
    id, _ := engine.Submit(ctx, loom.TaskRequest{
        WorkerType: loom.WorkerTypeCLI,
        ProjectID:  "test",
        Prompt:     "hello",
    })

    // Task ID is deterministic.
    if id != "id-0" {
        t.Errorf("expected id-0, got %s", id)
    }

    // Advance time to simulate work duration.
    clock.Advance(100 * time.Millisecond)

    // Retrieve and check timestamps.
    task, _ := engine.Get(id)
    if !task.CreatedAt.Equal(epoch) {
        t.Errorf("expected CreatedAt = %v, got %v", epoch, task.CreatedAt)
    }
}
```

`FakeClock.Sleep(d)` advances frozen time by `d` without blocking. This lets
you test timeout behavior without real sleeps:

```go
clock := deps.NewFakeClock(time.Now())
// Simulate a 30-second task timeout in zero real time.
clock.Advance(31 * time.Second)
```

Sequential IDs make assertions stable across runs: the first task is always
`id-0`, the second `id-1`, etc.

---

## SpawnRunner Injection for Subprocess Tests

Never spawn a real subprocess in unit tests. Inject a mock `SubprocessRunner`
into `SubprocessBase`:

```go
// mockRunner returns a fixed stdout without launching any process.
type mockRunner struct {
    stdout   string
    exitCode int
    err      error
}

func (r *mockRunner) Run(_ context.Context, spawn workers.SubprocessSpawn) (string, int, error) {
    return r.stdout, r.exitCode, r.err
}

func TestSubprocessWorker_HappyPath(t *testing.T) {
    base := workers.SubprocessBase{
        Resolver: staticResolver{spawn: workers.SubprocessSpawn{
            Command: "echo",
            Args:    []string{"hello"},
        }},
        Runner: &mockRunner{stdout: "hello\n", exitCode: 0},
    }
    task := &loom.Task{ID: "t1", Prompt: "hello"}
    result, err := base.Run(context.Background(), task)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if result.Content != "hello\n" {
        t.Errorf("expected 'hello\\n', got %q", result.Content)
    }
}
```

See `loom/workers/subprocess_base_test.go` for the full canonical test suite
including cancellation, timeout, and error-propagation patterns.

---

## httptest.Server Pattern for HTTPBase Tests

Use `net/http/httptest.Server` to test `HTTPBase` without making real network
calls. This is the canonical pattern used in `loom/workers/http_base_test.go`.

```go
func TestHTTPBase_RetryOn5xx(t *testing.T) {
    // Fail twice, succeed on third attempt.
    var calls atomic.Int32
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        n := calls.Add(1)
        if n <= 2 {
            w.WriteHeader(http.StatusInternalServerError)
            return
        }
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    }))
    defer srv.Close()

    base := &workers.HTTPBase{
        Resolver:   &staticHTTPResolver{req: workers.HTTPRequest{
            Method: "GET",
            URL:    srv.URL,
        }},
        MaxRetries: 3,
        BackoffMS:  1, // minimise backoff in tests
    }
    task := &loom.Task{ID: "h1"}
    result, err := base.Run(context.Background(), task)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if result.Content != "ok" {
        t.Errorf("expected 'ok', got %q", result.Content)
    }
    if calls.Load() != 3 {
        t.Errorf("expected 3 calls, got %d", calls.Load())
    }
}
```

---

## Recording Logger Pattern

Capture structured log entries for field assertions. This is the pattern from
`loom/engine_metrics_test.go`:

```go
type recordingLogger struct {
    mu      sync.Mutex
    entries []logEntry
}

type logEntry struct {
    msg   string
    args  []any
    level string
}

func (l *recordingLogger) InfoContext(_ context.Context, msg string, args ...any) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.entries = append(l.entries, logEntry{msg: msg, args: args, level: "info"})
}

func (l *recordingLogger) ErrorContext(_ context.Context, msg string, args ...any) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.entries = append(l.entries, logEntry{msg: msg, args: args, level: "error"})
}

// Implement DebugContext and WarnContext similarly.

// hasField returns true if any entry matching msg contains the given key.
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
```

See `loom/engine_metrics_test.go` — `TestEngine_Submit_LogsCanonicalFields` uses
this exact pattern to assert canonical field presence.

---

## Recording Meter Pattern

Capture OTel metric emissions without a real metrics SDK. Each recording
instrument embeds the OTel `embedded` type to satisfy the interface, and
overrides `Add`/`Record` to capture the value:

```go
// recordingCounter satisfies otelmetric.Int64Counter.
type recordingCounter struct {
    embedded.Int64Counter
    total atomic.Int64
}
func (c *recordingCounter) Add(_ context.Context, n int64, _ ...otelmetric.AddOption) {
    c.total.Add(n)
}
func (c *recordingCounter) Enabled(_ context.Context) bool { return true }
```

Wire a `recordingMeter` that returns these instruments from `Int64Counter` and
`Int64Histogram` factory calls, and delegates `Float64Histogram` /
`Int64UpDownCounter` to `deps.NoopMeter()`.

The full implementation is in `loom/engine_metrics_test.go` (`newRecordingMeter`,
`recordingCounter`, `recordingHistogram`). See `TestEngine_Submit_EmitsMetrics`
for the canonical usage pattern: inject via `WithMeter(rm)`, submit a task,
poll until the counter increments, assert the value.

---

## Failure Injection Patterns

### Worker Panic

```go
type panicWorker struct{}

func (panicWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (panicWorker) Execute(_ context.Context, _ *loom.Task) (*loom.WorkerResult, error) {
    panic("deliberate test panic")
}
```

The engine recovers the panic and marks the task `failed_crash`. Assert:

```go
waitForStatus(t, engine, id, loom.TaskStatusFailedCrash, 2*time.Second)
task, _ := engine.Get(id)
if task.Error == "" {
    t.Error("expected panic message in task.Error")
}
```

### Context Cancellation

```go
ctx, cancel := context.WithCancel(context.Background())

id, _ := engine.Submit(ctx, loom.TaskRequest{
    WorkerType: loom.WorkerTypeCLI,
    ProjectID:  "proj",
    Prompt:     "work",
})

// Cancel the running task via engine (not via ctx — ctx only affects Submit itself).
time.Sleep(10 * time.Millisecond) // let dispatch start
engine.Cancel(id)

waitForStatus(t, engine, id, loom.TaskStatusFailed, 2*time.Second)
```

Note: cancelling the Submit `ctx` does NOT cancel the task. Use `engine.Cancel(id)`
to cancel a running task.

### QualityGate Rejection

Test gate rejection by returning empty content from Execute:

```go
type emptyWorker struct{}

func (emptyWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (emptyWorker) Execute(_ context.Context, _ *loom.Task) (*loom.WorkerResult, error) {
    return &loom.WorkerResult{Content: ""}, nil  // gate rejects, retries, then fails
}
```

With `WithMaxRetries(0)`, the task fails on first empty result:

```go
engine := loom.New(store, loom.WithMaxRetries(0))
engine.RegisterWorker(loom.WorkerTypeCLI, emptyWorker{})
id, _ := engine.Submit(ctx, req)
waitForStatus(t, engine, id, loom.TaskStatusFailed, 2*time.Second)
```

### Waiting Helper

```go
func waitForStatus(t *testing.T, engine *loom.LoomEngine, id string,
    want loom.TaskStatus, timeout time.Duration) {
    t.Helper()
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        task, err := engine.Get(id)
        if err != nil {
            t.Fatalf("Get(%s): %v", id, err)
        }
        if task.Status == want {
            return
        }
        time.Sleep(10 * time.Millisecond)
    }
    task, _ := engine.Get(id)
    t.Errorf("task %s: want status %s after %v, got %s", id, want, timeout, task.Status)
}
```
