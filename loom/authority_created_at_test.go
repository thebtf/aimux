package loom

import (
	"context"
	"testing"
	"time"
)

func TestCommitCreatedDerivesLastSeenAtFromCreatedAt(t *testing.T) {
	fixture := t013NewFixture(t)
	createdAt := t013At.Add(-24 * time.Hour)
	result, err := fixture.store.CommitCreated(context.Background(), CreateTask{
		TaskID: "created-last-seen", WorkerType: WorkerTypeCLI, ProjectID: "created-time",
		TenantID: LegacyTenantID, Prompt: "created time", CreatedAt: createdAt,
	})
	if err != nil || !result.Applied {
		t.Fatalf("CommitCreated=%#v/%v, want applied", result, err)
	}
	var lastSeenAt string
	if err := fixture.observer.QueryRow(`SELECT last_seen_at FROM tasks WHERE id='created-last-seen'`).Scan(&lastSeenAt); err != nil {
		t.Fatal(err)
	}
	if want := createdAt.UTC().Format(time.RFC3339); lastSeenAt != want {
		t.Fatalf("last_seen_at=%q, want command CreatedAt %q", lastSeenAt, want)
	}
}
