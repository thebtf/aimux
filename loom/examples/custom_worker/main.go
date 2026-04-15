// CustomWorker demonstrates how to satisfy loom.Worker from scratch using only
// stdlib — no SubprocessBase, no HTTPBase, just the Worker interface directly.
// It also shows how to use Subscribe for lifecycle event monitoring.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/thebtf/aimux/loom"
)

// upperCaseWorker is a pure-function Worker that uppercases the task prompt.
// It satisfies loom.Worker with nothing beyond stdlib.
type upperCaseWorker struct{}

func (upperCaseWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }

func (upperCaseWorker) Execute(_ context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	result := strings.ToUpper(task.Prompt)
	return &loom.WorkerResult{
		Content: result,
		Metadata: map[string]any{
			"original_length": len(task.Prompt),
			"upper_length":    len(result),
		},
	}, nil
}

func main() {
	db, err := sql.Open("sqlite", "file:custom?cache=shared&mode=memory")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	engine, err := loom.NewEngine(db)
	if err != nil {
		log.Fatal(err)
	}
	engine.RegisterWorker(loom.WorkerTypeCLI, upperCaseWorker{})

	// Subscribe to lifecycle events before submitting.
	var mu sync.Mutex
	var events []loom.TaskEvent

	unsubscribe := engine.Events().Subscribe(func(e loom.TaskEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	defer unsubscribe()

	ctx := loom.WithRequestID(context.Background(), "req-demo-001")
	id, err := engine.Submit(ctx, loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  "demo",
		Prompt:     "hello from loom",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("submitted: %s\n", id)

	// Poll until the task reaches a terminal state.
	for i := 0; i < 30; i++ {
		task, err := engine.Get(id)
		if err != nil {
			log.Fatal(err)
		}
		if task.Status.IsTerminal() {
			fmt.Printf("status: %s\n", task.Status)
			fmt.Printf("result: %s\n", task.Result)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Print the events received via Subscribe.
	time.Sleep(10 * time.Millisecond) // allow final events to be delivered
	mu.Lock()
	defer mu.Unlock()
	fmt.Printf("events received: %d\n", len(events))
	for _, e := range events {
		fmt.Printf("  %s → %s\n", e.Type, e.Status)
	}
}
