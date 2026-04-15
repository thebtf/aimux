// Hello demonstrates the minimal loom.Submit + Get polling pattern.
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

type echoWorker struct{}

func (echoWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (echoWorker) Execute(_ context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	return &loom.WorkerResult{Content: "hello: " + task.Prompt}, nil
}

func main() {
	db, err := sql.Open("sqlite", "file:hello?cache=shared&mode=memory")
	if err != nil {
		log.Fatal(err)
	}
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
		task, err := engine.Get(id)
		if err != nil {
			log.Fatal(err)
		}
		if task.Status == loom.TaskStatusCompleted {
			fmt.Printf("status: %s\nresult: %s\n", task.Status, task.Result)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	log.Fatal("timeout waiting for completion")
}
