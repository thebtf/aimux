# loom Playbook

Seven complete recipes for common loom patterns, plus an anti-patterns section.
Each recipe is a self-contained runnable program or well-defined composition pattern.

References: [README.md](README.md) | [CONTRACT.md](CONTRACT.md) | [TESTING.md](TESTING.md) | [CHANGELOG.md](CHANGELOG.md)

---

## Recipe 1 — Subprocess Worker

Use `workers.SubprocessBase` to execute an OS subprocess and return its stdout as
the task result. This is the most common worker pattern for AI CLI tools.

**Canonical example:** `pkg/aimuxworkers/cli.go` in the aimux repository is a
22-line adapter that composes `SubprocessBase` with aimux's full executor stack
(ConPTY/PTY/Pipe selection, model fallback, env merging). It is the reference
implementation for the SubprocessBase pattern.

### How SubprocessBase works

```
Submit(task)
    └─▶ SubprocessBase.Run(ctx, task)
            └─▶ SpawnResolver.Resolve(ctx, task)  ← you implement this
                    └─▶ SubprocessSpawn{Command, Args, CWD, Env, Stdin}
            └─▶ SubprocessRunner.Run(ctx, spawn)  ← default: os/exec
                    └─▶ stdout captured as WorkerResult.Content
```

`SpawnResolver` decouples command construction from execution. In tests you can
inject a fake runner that never spawns a process.

### Minimal example

```go
package main

import (
    "context"
    "database/sql"
    "fmt"
    "log"
    "runtime"
    "time"

    _ "modernc.org/sqlite"
    "github.com/thebtf/aimux/loom"
    "github.com/thebtf/aimux/loom/workers"
)

// echoResolver builds a cross-platform echo command from the task prompt.
type echoResolver struct{}

func (echoResolver) Resolve(_ context.Context, task *loom.Task) (workers.SubprocessSpawn, error) {
    if runtime.GOOS == "windows" {
        return workers.SubprocessSpawn{
            Command: "cmd",
            Args:    []string{"/c", "echo", task.Prompt},
        }, nil
    }
    return workers.SubprocessSpawn{
        Command: "sh",
        Args:    []string{"-c", "echo \"$1\"", "--", task.Prompt},
    }, nil
}

// echoSubprocessWorker wraps SubprocessBase.
type echoSubprocessWorker struct {
    base workers.SubprocessBase
}

func (w *echoSubprocessWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (w *echoSubprocessWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    return w.base.Run(ctx, task)
}

func main() {
    db, _ := sql.Open("sqlite", "file:sub?cache=shared&mode=memory")
    defer db.Close()

    engine, err := loom.NewEngine(db, "subprocess-example")
    if err != nil {
        log.Fatal(err)
    }
    engine.RegisterWorker(loom.WorkerTypeCLI, &echoSubprocessWorker{
        base: workers.SubprocessBase{Resolver: echoResolver{}},
    })

    id, _ := engine.Submit(context.Background(), loom.TaskRequest{
        WorkerType: loom.WorkerTypeCLI,
        ProjectID:  "demo",
        Prompt:     "hello subprocess",
    })

    for i := 0; i < 20; i++ {
        t, _ := engine.Get(id)
        if t.Status.IsTerminal() {
            fmt.Printf("result: %s\n", t.Result)
            return
        }
        time.Sleep(50 * time.Millisecond)
    }
}
```

See `examples/subprocess/main.go` for the full runnable version.

### Injecting a custom runner (for testing or advanced use)

```go
type mockRunner struct{ out string }

func (r mockRunner) Run(_ context.Context, _ workers.SubprocessSpawn) (string, int, error) {
    return r.out, 0, nil
}

base := workers.SubprocessBase{
    Resolver: myResolver,
    Runner:   mockRunner{out: "mock output"},
}
```

In production you can pass aimux's full executor (with ConPTY, model fallback,
retry-on-rate-limit) as the `Runner` instead of the default `os/exec` runner.

---

## Recipe 2 — HTTP Worker

Use `workers.HTTPBase` for tasks that call an external HTTP API. HTTPBase handles
retry-with-backoff for 5xx and network errors out of the box.

### Retry policy

- Retryable: 5xx responses, network errors (connection refused, timeout).
- Non-retryable: 4xx responses (caller error — retrying won't help).
- Default: 2 retries, 500ms base backoff (doubles each attempt: 500ms, 1000ms).

```go
package main

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "net/http/httptest"
    "time"

    _ "modernc.org/sqlite"
    "github.com/thebtf/aimux/loom"
    "github.com/thebtf/aimux/loom/workers"
)

// jsonAPIResolver builds the request from the task prompt.
type jsonAPIResolver struct{ baseURL string }

func (r jsonAPIResolver) Resolve(_ context.Context, task *loom.Task) (workers.HTTPRequest, error) {
    body, _ := json.Marshal(map[string]string{"prompt": task.Prompt})
    return workers.HTTPRequest{
        Method:  "POST",
        URL:     r.baseURL + "/generate",
        Headers: map[string]string{"Content-Type": "application/json"},
        Body:    body,
    }, nil
}

type apiWorker struct{ base *workers.HTTPBase }

func (w *apiWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (w *apiWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    return w.base.Run(ctx, task)
}

func main() {
    // Fake server that fails once then succeeds.
    var calls int
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        calls++
        if calls == 1 {
            w.WriteHeader(http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]string{"result": "api response"})
    }))
    defer srv.Close()

    db, _ := sql.Open("sqlite", "file:http?cache=shared&mode=memory")
    defer db.Close()

    engine, err := loom.NewEngine(db, "http-example")
    if err != nil {
        log.Fatal(err)
    }
    engine.RegisterWorker(loom.WorkerTypeCLI, &apiWorker{
        base: workers.NewHTTPBase(jsonAPIResolver{baseURL: srv.URL}),
    })

    id, _ := engine.Submit(context.Background(), loom.TaskRequest{
        WorkerType: loom.WorkerTypeCLI,
        ProjectID:  "demo",
        Prompt:     "generate something",
    })

    for i := 0; i < 30; i++ {
        t, _ := engine.Get(id)
        if t.Status.IsTerminal() {
            fmt.Printf("status: %s\nresult: %s\n", t.Status, t.Result)
            return
        }
        time.Sleep(100 * time.Millisecond)
    }
    log.Fatal("timeout")
}
```

See `examples/http/main.go` for the full runnable version.

### Configuring backoff

```go
base := &workers.HTTPBase{
    Resolver:   myResolver,
    MaxRetries: 3,
    BackoffMS:  200,  // 200ms, 400ms, 800ms between attempts
    Client: &http.Client{Timeout: 10 * time.Second},
}
```

---

## Recipe 3 — Streaming Worker

Wrap any `Worker` in `workers.StreamingBase` to receive line-by-line progress
callbacks while the inner worker runs. The inner worker runs to completion; each
line of its output is then delivered to your `ProgressHandler`.

This is useful for streaming task progress to a WebSocket connection, a log
aggregator, or an in-process progress channel.

```go
// progressWorker wraps SubprocessBase in StreamingBase.
type progressWorker struct {
    streaming *workers.StreamingBase
}

func (w *progressWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (w *progressWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    return w.streaming.Execute(ctx, task)
}

// Usage:
inner := &echoSubprocessWorker{
    base: workers.SubprocessBase{Resolver: myResolver},
}

progress := make(chan string, 100)
wrapped := &progressWorker{
    streaming: &workers.StreamingBase{
        Inner: inner,
        OnLine: func(line string) {
            select {
            case progress <- line:
            default: // drop if channel full — never block dispatch goroutine
            }
        },
    },
}
```

Key contract: `OnLine` is called synchronously on the dispatch goroutine.
**Do not block in OnLine.** Panics in `OnLine` are recovered — they do not affect
the inner worker's result or other subscribers.

---

## Recipe 4 — Fan-out Pattern

Submit N sub-tasks from a single worker invocation. The fan-out worker calls
`engine.Submit` internally to create child tasks with a different `WorkerType`.
Use a distinct `WorkerType` to avoid infinite recursion.

```go
const WorkerTypeFanout loom.WorkerType = "fanout"
const WorkerTypeLeaf   loom.WorkerType = "leaf"

type fanoutWorker struct{ engine *loom.LoomEngine }

func (w *fanoutWorker) Type() loom.WorkerType { return WorkerTypeFanout }
func (w *fanoutWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    // Spawn three leaf tasks from the parent's prompt.
    prompts := []string{task.Prompt + " A", task.Prompt + " B", task.Prompt + " C"}
    ids := make([]string, 0, len(prompts))
    for _, p := range prompts {
        id, err := w.engine.Submit(ctx, loom.TaskRequest{
            WorkerType: WorkerTypeLeaf,  // different type — no recursion
            ProjectID:  task.ProjectID,
            RequestID:  task.RequestID,
            Prompt:     p,
        })
        if err != nil {
            return nil, fmt.Errorf("fanout submit: %w", err)
        }
        ids = append(ids, id)
    }
    // Return the IDs as the parent result. Caller can poll each leaf.
    return &loom.WorkerResult{
        Content:  strings.Join(ids, "\n"),
        Metadata: map[string]any{"child_count": len(ids)},
    }, nil
}

type leafWorker struct{}

func (leafWorker) Type() loom.WorkerType { return WorkerTypeLeaf }
func (leafWorker) Execute(_ context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    return &loom.WorkerResult{Content: "done: " + task.Prompt}, nil
}
```

**Warning:** Fan-out creates a task explosion risk. Guard the maximum branching
factor, and ensure leaf workers do not themselves fan-out.

---

## Recipe 5 — Chained Tasks via Event Bus

Worker A's completion triggers Worker B via an event subscription. This is useful
when B needs A's output as its input, but you want A and B to be decoupled.

```go
const WorkerTypeStageA loom.WorkerType = "stage_a"
const WorkerTypeStageB loom.WorkerType = "stage_b"

func wireChain(engine *loom.LoomEngine) {
    engine.Events().Subscribe(func(e loom.TaskEvent) {
        if e.Type != loom.EventTaskCompleted {
            return
        }
        // Only chain stage_a tasks.
        task, err := engine.Get(e.TaskID)
        if err != nil || task.WorkerType != WorkerTypeStageA {
            return
        }
        // Submit stage_b with stage_a's result as the prompt.
        // IMPORTANT: launch this in a goroutine — never block the event handler.
        go func() {
            _, _ = engine.Submit(context.Background(), loom.TaskRequest{
                WorkerType: WorkerTypeStageB,
                ProjectID:  task.ProjectID,
                RequestID:  task.RequestID,
                Prompt:     task.Result,
            })
        }()
    })
}
```

**Critical:** Submit inside an event handler MUST be in a goroutine. The event
handler is called synchronously on the dispatch goroutine. Calling Submit inside
the handler directly can cause a deadlock on the event bus's internal locks if
other goroutines are concurrently subscribing.

---

## Recipe 6 — Pure-Function Worker

The simplest possible worker: no subprocess, no HTTP, just computation. Useful
for transformation tasks, formatting, validation, or any in-process computation.

```go
// upperCaseWorker converts the prompt to uppercase.
type upperCaseWorker struct{}

func (upperCaseWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (upperCaseWorker) Execute(_ context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    if task.Prompt == "" {
        return &loom.WorkerResult{Content: ""}, nil  // gate will retry
    }
    return &loom.WorkerResult{
        Content: strings.ToUpper(task.Prompt),
        Metadata: map[string]any{
            "original_length": len(task.Prompt),
        },
    }, nil
}
```

This is the pattern shown in `examples/custom_worker/main.go` and
`examples/hello/main.go`.

**Error semantics for pure workers:**
- If the computation can succeed with retry → return `(result, nil)` with an
  empty content; the gate will retry.
- If the input is fundamentally invalid → return `(nil, err)`; the task fails
  immediately without retry.
- Never return `(nil, nil)` — this panics in the gate check.

---

## Recipe 7 — Subprocess with Progress (Composition of Two Bases)

Compose `SubprocessBase` inside `StreamingBase` to get a subprocess that streams
progress lines while running.

```go
type progressSubprocessWorker struct {
    streaming workers.StreamingBase
}

func (w *progressSubprocessWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (w *progressSubprocessWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    return w.streaming.Execute(ctx, task)
}

// Constructor that wires the composition.
func newProgressSubprocessWorker(resolver workers.SpawnResolver, onLine workers.ProgressHandler) *progressSubprocessWorker {
    inner := &subprocessOnlyWorker{
        base: workers.SubprocessBase{Resolver: resolver},
    }
    return &progressSubprocessWorker{
        streaming: workers.StreamingBase{
            Inner:  inner,
            OnLine: onLine,
        },
    }
}

// subprocessOnlyWorker is the inner subprocess layer.
type subprocessOnlyWorker struct {
    base workers.SubprocessBase
}

func (w *subprocessOnlyWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (w *subprocessOnlyWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    return w.base.Run(ctx, task)
}
```

Usage:

```go
worker := newProgressSubprocessWorker(
    myResolver,
    func(line string) {
        // Called for each line of subprocess output.
        // Must return immediately — do not block.
        go handleProgressLine(line)
    },
)
engine.RegisterWorker(loom.WorkerTypeCLI, worker)
```

**Note on streaming semantics:** StreamingBase currently delivers progress after
the subprocess completes (it tails `WorkerResult.Content`). For true real-time
streaming from a subprocess, implement a custom Runner that writes lines to a
channel as they are produced.

---

## Anti-Patterns

These are common mistakes. Avoid them.

### Anti-pattern 1: Blocking in an event handler

```go
// WRONG — blocks the dispatch goroutine
engine.Events().Subscribe(func(e loom.TaskEvent) {
    result := callSomeSlowAPI()  // this blocks dispatch of ALL tasks
    log.Println(result)
})

// CORRECT — offload to goroutine
engine.Events().Subscribe(func(e loom.TaskEvent) {
    go func() {
        result := callSomeSlowAPI()
        log.Println(result)
    }()
})
```

The event bus delivers events synchronously on the dispatch goroutine. Blocking
the handler blocks ALL task dispatches, not just the one that emitted the event.

### Anti-pattern 2: Calling Submit from inside Execute synchronously

```go
// WRONG — Submit blocks waiting for the background goroutine that called Execute,
// which is the same goroutine. This causes a deadlock if the spawned task needs
// the current goroutine's dispatch to finish.
func (w *myWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    id, _ := engine.Submit(ctx, loom.TaskRequest{...})  // may deadlock
    // ... poll for id
}

// CORRECT — Submit is fine inside Execute IF you do not poll for the sub-task
// synchronously in the same Execute call. Or: launch in goroutine.
func (w *myWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    go func() {
        engine.Submit(context.Background(), loom.TaskRequest{...})
    }()
    return &loom.WorkerResult{Content: "sub-task submitted"}, nil
}
```

### Anti-pattern 3: Using the caller's context for long-running work

```go
// WRONG — if the HTTP handler context times out, the worker is cancelled
func handler(w http.ResponseWriter, r *http.Request) {
    engine.Submit(r.Context(), req)  // r.Context() cancels when request ends
}

// CORRECT — Submit with a background context; RequestID extracted separately
func handler(w http.ResponseWriter, r *http.Request) {
    ctx := loom.WithRequestID(context.Background(), r.Header.Get("X-Request-ID"))
    engine.Submit(ctx, req)  // background context; task survives request end
}
```

The `ctx` passed to `Submit` is only used to extract `RequestIDKey` and emit
metrics. It is NOT the task's execution context. Even so, passing a
short-lived context is a code smell — use `context.Background()` as the base.

### Anti-pattern 4: Subscribing inside a Worker

```go
// WRONG — each Execute call adds a new subscription, they accumulate
func (w *leakingWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
    w.engine.Events().Subscribe(func(e loom.TaskEvent) { ... })  // leaks
    // ...
}

// CORRECT — subscribe once during setup, outside the worker
unsubscribe := engine.Events().Subscribe(handler)
defer unsubscribe()
```

### Anti-pattern 5: Ignoring failed_crash tasks

```go
// WRONG — auto-resubmitting failed_crash without checking idempotency
tasks, _ := engine.List("proj", loom.TaskStatusFailedCrash)
for _, t := range tasks {
    engine.Submit(ctx, loom.TaskRequest{Prompt: t.Prompt}) // dangerous
}

// CORRECT — investigate before resubmitting
tasks, _ := engine.List("proj", loom.TaskStatusFailedCrash)
for _, t := range tasks {
    if isIdempotent(t) {
        engine.Submit(ctx, loom.TaskRequest{Prompt: t.Prompt})
    } else {
        log.Printf("manual review needed for task %s", t.ID)
    }
}
```

`failed_crash` tasks may have partially executed. Blindly resubmitting can cause
duplicate side effects. See [RECOVERY.md](RECOVERY.md) for the full playbook.

### Anti-pattern 6: Registering two workers for the same WorkerType

```go
// WRONG — second registration silently overwrites the first
engine.RegisterWorker(loom.WorkerTypeCLI, worker1)
engine.RegisterWorker(loom.WorkerTypeCLI, worker2)  // worker1 is gone

// CORRECT — use distinct WorkerType values
const WorkerTypeCLIFast loom.WorkerType = "cli_fast"
const WorkerTypeCLISlow loom.WorkerType = "cli_slow"
engine.RegisterWorker(WorkerTypeCLIFast, fastWorker)
engine.RegisterWorker(WorkerTypeCLISlow, slowWorker)
```

### Anti-pattern 7: Not calling RecoverCrashed on startup

```go
// WRONG — tasks from the previous run stay in 'dispatched' or 'running' forever
engine, _ := loom.NewEngine(db, "daemon-name")
engine.RegisterWorker(...)
// start accepting new tasks...

// CORRECT — always call RecoverCrashed before accepting new tasks
engine, _ := loom.NewEngine(db, "daemon-name")
n, err := engine.RecoverCrashed()
if err != nil {
    log.Fatalf("crash recovery failed: %v", err)
}
log.Printf("recovered %d crashed tasks", n)
engine.RegisterWorker(...)
```

Without `RecoverCrashed`, tasks that were in-flight during a daemon crash
remain stuck in `dispatched` or `running` forever. They will never be retried
and will never time out — they just sit there.

---

## Wiring Dependency Injection

All four deps interfaces can be injected at engine construction time:

```go
import (
    "log/slog"
    "github.com/thebtf/aimux/loom"
    "github.com/thebtf/aimux/loom/deps"
)

logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
clock  := deps.SystemClock()    // or deps.NewFakeClock(t) in tests
idGen  := deps.UUIDGenerator()  // or deps.NewSequentialIDGenerator() in tests
// meter from your OTel provider, or deps.NoopMeter() to silence metrics

engine, err := loom.NewEngine(db, "daemon-name",
    loom.WithLogger(logger),
    loom.WithClock(clock),
    loom.WithIDGenerator(idGen),
    loom.WithMeter(meter),
    loom.WithMaxRetries(3),
)
```

Omitting an option uses a safe default (noop logger, system clock, UUID v7 IDs,
noop meter, 2 max retries). You never need to pass `nil` — omit the option.

---

## Testing Helpers

For deterministic tests, inject `deps.NewFakeClock` and
`deps.NewSequentialIDGenerator`:

```go
clock := deps.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
idGen := deps.NewSequentialIDGenerator()

engine := loom.New(store,
    loom.WithClock(clock),
    loom.WithIDGenerator(idGen),
)

// Task IDs will be "id-0", "id-1", etc.
// Timestamps are frozen at 2026-01-01 until you call clock.Advance(d).
```

See [TESTING.md](TESTING.md) for the full test pattern guide.
