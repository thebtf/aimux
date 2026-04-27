// Subprocess demonstrates SubprocessBase composition: how to wrap an OS process
// as a loom Worker. Uses a cross-platform echo command (cmd /c echo on Windows,
// sh -c echo on Unix) so it runs on all supported platforms.
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

// promptEchoResolver builds a cross-platform echo command from the task prompt.
type promptEchoResolver struct{}

func (promptEchoResolver) Resolve(_ context.Context, task *loom.Task) (workers.SubprocessSpawn, error) {
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

// subprocessWorker wraps SubprocessBase as a loom.Worker.
// This is the canonical pattern: embed or hold SubprocessBase, delegate Execute.
type subprocessWorker struct {
	base workers.SubprocessBase
}

func (w *subprocessWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (w *subprocessWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	return w.base.Run(ctx, task)
}

func main() {
	db, err := sql.Open("sqlite", "file:subprocess?cache=shared&mode=memory")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	engine, err := loom.NewEngine(db, "subprocess-example")
	if err != nil {
		log.Fatal(err)
	}
	engine.RegisterWorker(loom.WorkerTypeCLI, &subprocessWorker{
		base: workers.SubprocessBase{Resolver: promptEchoResolver{}},
	})

	// Submit a normal task: subprocess echoes the prompt.
	id, err := engine.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  "demo",
		Prompt:     "hello subprocess",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("submitted: %s\n", id)

	for i := 0; i < 30; i++ {
		task, err := engine.Get(id)
		if err != nil {
			log.Fatal(err)
		}
		if task.Status.IsTerminal() {
			fmt.Printf("status: %s\n", task.Status)
			fmt.Printf("result: %s\n", task.Result)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	log.Fatal("timeout waiting for completion")
}
