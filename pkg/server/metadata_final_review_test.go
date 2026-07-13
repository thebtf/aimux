package server

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	_ "modernc.org/sqlite"
)

func TestPersistedIntegralReviewControlsRemainRejected(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?cache=shared&mode=memory")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := loom.NewTaskStore(db, "metadata-final-review")
	if err != nil {
		t.Fatalf("NewTaskStore: %v", err)
	}

	for _, raw := range []string{"2.0", "1e3", "9223372036854775808", "1.0000000000000000000000000001"} {
		t.Run(raw, func(t *testing.T) {
			task := &loom.Task{
				ID:         "review-control-" + raw,
				Status:     loom.TaskStatusPending,
				WorkerType: loom.WorkerTypeCLI,
				ProjectID:  "metadata-final-review",
				Prompt:     "review",
				CreatedAt:  time.Now().UTC(),
				Metadata:   map[string]any{"max_attempts": json.Number(raw)},
			}
			if err := store.Create(task); err != nil {
				t.Fatalf("Create: %v", err)
			}
			reloaded, err := store.Get(task.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got := fallbackOptionsFromTaskMetadata(reloaded.Metadata).MaxAttempts; got != 0 {
				t.Fatalf("max_attempts %q after reload = %d, want rejected zero", raw, got)
			}
		})
	}
}
