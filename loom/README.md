# loom

Task mediator library for Go. Submit tasks, dispatch to workers, persist state,
deliver lifecycle events to subscribers, and recover from crashes.

## Overview

`loom` is a small, dependency-light Go library that gives you a persistent task
queue with pluggable workers, lifecycle events, quality validation, and crash
recovery — all in fewer than 1000 lines of production code.

Key properties:

- **Submit returns immediately.** Dispatch happens in a background goroutine whose
  lifetime is independent of the caller's context. A disconnecting HTTP client or
  cancelled request does NOT cancel an already-dispatched task.
- **All state is persisted.** Tasks survive process restart. `RecoverCrashed` marks
  in-flight tasks as `failed_crash` on the next daemon boot.
- **Pluggable workers.** Register any type that satisfies `Worker` — subprocess
  wrappers, HTTP clients, pure-function executors, or composed bases.
- **Quality gate.** Worker results are validated before the task is marked
  completed. Empty output and rate-limit responses trigger automatic retry.
- **Observable.** Eight OTel metric instruments plus structured log fields on every
  significant operation.

## Install

    go get github.com/thebtf/aimux/loom@v0.1.0

The module path is `github.com/thebtf/aimux/loom`. It is a standalone nested
module — it does NOT pull in the full aimux server or any MCP dependencies.

Minimum Go version: 1.25.

## Quick Start

The minimal pattern is: create an engine from an SQLite database, register a
worker, submit a task, and poll until completion.

```go
package main

import (
    "context"
    "database/sql"
    "fmt"
    "log"
    "time"

    _ "modernc.org/sqlite"
    "github.com/thebtf/aimux/loom"
)

// echoWorker returns the prompt back as the result.
type echoWorker struct{}

func (echoWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (echoWorker) Execute(_ context.Context, t *loom.Task) (*loom.WorkerResult, error) {
    return &loom.WorkerResult{Content: "hello: " + t.Prompt}, nil
}

func main() {
    db, _ := sql.Open("sqlite", "file:hello?cache=shared&mode=memory")
    defer db.Close()

    engine, err := loom.NewEngine(db)
    if err != nil {
        log.Fatal(err)
    }
    engine.RegisterWorker(loom.WorkerTypeCLI, echoWorker{})

    id, err := engine.Submit(context.Background(), loom.TaskRequest{
        WorkerType: loom.WorkerTypeCLI,
        ProjectID:  "demo",
        Prompt:     "world",
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("submitted: %s\n", id)

    for i := 0; i < 20; i++ {
        task, _ := engine.Get(id)
        if task.Status == loom.TaskStatusCompleted {
            fmt.Printf("status: %s\nresult: %s\n", task.Status, task.Result)
            return
        }
        time.Sleep(50 * time.Millisecond)
    }
    log.Fatal("timeout")
}
```

See `examples/hello/main.go` for the full runnable version.

## Concepts

### Task

A `Task` is the unit of work. It carries an auto-generated `ID`, a `ProjectID`
for multi-tenant isolation, a `RequestID` for distributed tracing, a free-form
`Prompt` string, optional `Env`/`CWD` overrides, and routing hints (`CLI`,
`Role`, `Model`).

Tasks are created by `Submit` and persisted to SQLite immediately. Their status
advances through a documented state machine (see [CONTRACT.md](CONTRACT.md)).

### Worker

A `Worker` is any type that implements two methods:

```go
type Worker interface {
    Execute(ctx context.Context, task *Task) (*WorkerResult, error)
    Type() WorkerType
}
```

The library ships three composable bases in `loom/workers/`:

| Base | Use case |
|---|---|
| `SubprocessBase` | Wraps an os/exec subprocess |
| `HTTPBase` | Makes HTTP calls with retry/backoff |
| `StreamingBase` | Adds line-by-line progress to any inner Worker |

### Engine

`LoomEngine` owns dispatch, persistence, event delivery, and cancellation. It is
created once per process via `NewEngine(db, opts...)`.

```go
engine, _ := loom.NewEngine(db,
    loom.WithLogger(myLogger),
    loom.WithMeter(myMeter),
)
engine.RegisterWorker(loom.WorkerTypeCLI, myWorker)
```

### QualityGate

After `Execute` returns, the result is evaluated before the task transitions to
`completed`. The gate rejects empty output and rate-limit responses (both trigger
retry up to `maxRetries`). Thrashing detection (Jaccard similarity across the
last N results) prevents infinite retry loops.

### Events

Subscribe to task lifecycle events via the event bus:

```go
unsubscribe := engine.Events().Subscribe(func(e loom.TaskEvent) {
    fmt.Printf("%s → %s\n", e.TaskID, e.Status)
})
defer unsubscribe()
```

Delivery is synchronous on the dispatch goroutine. Subscribers must return
quickly — offload heavy work to their own goroutine.

### Crash Recovery

Call `RecoverCrashed()` once during daemon startup:

```go
n, err := engine.RecoverCrashed()
log.Printf("marked %d crashed tasks as failed_crash", n)
```

This marks any tasks still in `dispatched` or `running` state as `failed_crash`.

## Dependencies

The `loom` module has a strict, minimal dependency closure:

| Dependency | Purpose |
|---|---|
| stdlib | All language primitives |
| `github.com/google/uuid` | Task ID generation (UUID v7) |
| `go.opentelemetry.io/otel/metric` | OTel metric API (API-only, no SDK) |
| `modernc.org/sqlite` | Pure-Go SQLite driver (no CGO) |

No MCP SDK, no aimux server code, no external HTTP clients beyond stdlib.

## Links

- [CONTRACT.md](CONTRACT.md) — formal interface specification, state machine, and stability contract
- [PLAYBOOK.md](PLAYBOOK.md) — 7+ complete recipes for common patterns
- [TESTING.md](TESTING.md) — unit and integration test patterns
- [RECOVERY.md](RECOVERY.md) — terminal states and operator playbook
- [CHANGELOG.md](CHANGELOG.md) — v0.1.0 release notes and full public API surface
