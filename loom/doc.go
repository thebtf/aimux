// Package loom provides a central task mediator with lifecycle management,
// dependency injection, and observability hooks for long-running work.
//
// LoomEngine dispatches Tasks to pluggable Workers, persists state in SQLite
// with crash recovery, emits lifecycle events via a callback EventBus, and
// exposes OpenTelemetry metrics and structured logging.
//
// Basic usage:
//
//	db, _ := sql.Open("sqlite", "tasks.db?_pragma=journal_mode(WAL)")
//	defer db.Close()
//
//	engine, err := loom.NewEngine(db, "my-daemon",
//	    loom.WithLogger(myLogger),
//	    loom.WithMeter(myMeter),
//	)
//	if err != nil { panic(err) }
//	defer engine.Close(context.Background())
//
//	engine.RegisterWorker(loom.WorkerTypeCLI, myWorker)
//
//	id, err := engine.Submit(ctx, loom.TaskRequest{
//	    WorkerType: loom.WorkerTypeCLI,
//	    ProjectID:  "my-project",
//	    Prompt:     "do the thing",
//	})
//
// See README.md, CONTRACT.md, PLAYBOOK.md, and TESTING.md for details.
package loom
