// HTTP demonstrates HTTPBase usage with an in-process httptest.Server.
// Shows retry-on-5xx: the server fails on the first request, then succeeds.
// HTTPBase retries automatically with exponential backoff.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/loom/workers"
)

// echoHTTPResolver builds a POST request to the given URL with the task prompt
// as the request body.
type echoHTTPResolver struct{ url string }

func (r *echoHTTPResolver) Resolve(_ context.Context, task *loom.Task) (workers.HTTPRequest, error) {
	return workers.HTTPRequest{
		Method: "POST",
		URL:    r.url,
		Body:   []byte(task.Prompt),
	}, nil
}

// httpWorker wraps HTTPBase as a loom.Worker.
type httpWorker struct {
	base *workers.HTTPBase
}

func (w *httpWorker) Type() loom.WorkerType { return loom.WorkerTypeCLI }
func (w *httpWorker) Execute(ctx context.Context, task *loom.Task) (*loom.WorkerResult, error) {
	return w.base.Run(ctx, task)
}

func main() {
	// Server that fails on the first call, succeeds on subsequent calls.
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			// Simulate a transient 500 error — HTTPBase will retry.
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "internal error (attempt %d)", n)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok (attempt %d)", n)
	}))
	defer srv.Close()

	db, err := sql.Open("sqlite", "file:httpex?cache=shared&mode=memory")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	engine, err := loom.NewEngine(db, "http-example")
	if err != nil {
		log.Fatal(err)
	}

	base := &workers.HTTPBase{
		Resolver:   &echoHTTPResolver{url: srv.URL},
		MaxRetries: 2,
		BackoffMS:  10, // short backoff for the example
	}
	engine.RegisterWorker(loom.WorkerTypeCLI, &httpWorker{base: base})

	id, err := engine.Submit(context.Background(), loom.TaskRequest{
		WorkerType: loom.WorkerTypeCLI,
		ProjectID:  "demo",
		Prompt:     "hello http",
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
			fmt.Printf("server calls: %d\n", callCount.Load())
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	log.Fatal("timeout waiting for completion")
}
