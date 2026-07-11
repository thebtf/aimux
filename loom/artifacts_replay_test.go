package loom

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func openArtifactReplayStore(t *testing.T, path, engineName string) (*sql.DB, *TaskStore) {
	t.Helper()
	dsn := filepath.ToSlash(path) + "?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	store, err := NewTaskStore(db, engineName)
	if err != nil {
		_ = db.Close()
		t.Fatalf("NewTaskStore(%s): %v", engineName, err)
	}
	return db, store
}

func appendArtifactReplayRow(t *testing.T, store *TaskStore, taskID, summary string) TaskArtifact {
	t.Helper()
	artifact, err := store.AppendArtifact(taskID, TaskArtifactAppend{
		Kind:      TaskArtifactKindRuntime,
		EventType: "text_delta",
		Channel:   "stdout",
		Summary:   summary,
		Payload:   map[string]any{"text": summary},
	})
	if err != nil {
		t.Fatalf("AppendArtifact(%q): %v", summary, err)
	}
	return artifact
}

func artifactReplayBookmark(t *testing.T, page TaskArtifactPage) string {
	t.Helper()
	if page.NextCursor != "" {
		return page.NextCursor
	}
	if len(page.Items) == 0 {
		t.Fatal("cannot recover a missing bookmark from an empty page")
	}
	t.Errorf("exhausted non-empty page next_cursor is empty; want opaque tail bookmark")
	return formatArtifactCursor(page.Items[len(page.Items)-1].Seq)
}

func TestArtifactReplay_DurableTailBookmarkSurvivesReopenAndEmptyPoll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tail-bookmark.db")
	db, store := openArtifactReplayStore(t, path, "artifact-replay-tail")
	createArtifactTask(t, store, "artifact-replay-tail", "artifact-replay", TaskStatusRunning)
	first := appendArtifactReplayRow(t, store, "artifact-replay-tail", "initial-1")
	second := appendArtifactReplayRow(t, store, "artifact-replay-tail", "initial-2")

	initial, err := store.ListArtifacts("artifact-replay-tail", TaskArtifactListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListArtifacts(initial tail): %v", err)
	}
	if initial.HasMore || len(initial.Items) != 2 {
		t.Fatalf("initial tail page = has_more %v items %d; want exhausted two-row page", initial.HasMore, len(initial.Items))
	}
	if initial.Items[0].Seq != first.Seq || initial.Items[1].Seq != second.Seq {
		t.Fatalf("initial tail seqs = [%d %d]; want [%d %d]", initial.Items[0].Seq, initial.Items[1].Seq, first.Seq, second.Seq)
	}
	tailBookmark := artifactReplayBookmark(t, initial)

	if err := db.Close(); err != nil {
		t.Fatalf("close store before reopen: %v", err)
	}
	_, reopened := openArtifactReplayStore(t, path, "artifact-replay-tail")
	third := appendArtifactReplayRow(t, reopened, "artifact-replay-tail", "after-reopen-1")
	fourth := appendArtifactReplayRow(t, reopened, "artifact-replay-tail", "after-reopen-2")

	resumed, err := reopened.ListArtifacts("artifact-replay-tail", TaskArtifactListOptions{
		Cursor: tailBookmark,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListArtifacts(resume after reopen): %v", err)
	}
	if resumed.HasMore || len(resumed.Items) != 2 {
		t.Fatalf("resumed page = has_more %v items %d; want exactly two new rows", resumed.HasMore, len(resumed.Items))
	}
	if resumed.Items[0].Seq != third.Seq || resumed.Items[1].Seq != fourth.Seq {
		t.Fatalf("resumed seqs = [%d %d]; want new rows [%d %d]", resumed.Items[0].Seq, resumed.Items[1].Seq, third.Seq, fourth.Seq)
	}
	for _, artifact := range resumed.Items {
		if artifact.Seq == first.Seq || artifact.Seq == second.Seq {
			t.Fatalf("resume duplicated pre-reopen artifact seq %d", artifact.Seq)
		}
	}
	resumedBookmark := artifactReplayBookmark(t, resumed)

	emptyPoll, err := reopened.ListArtifacts("artifact-replay-tail", TaskArtifactListOptions{
		Cursor: resumedBookmark,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListArtifacts(empty tail poll): %v", err)
	}
	if len(emptyPoll.Items) != 0 || emptyPoll.HasMore {
		t.Fatalf("empty tail poll = has_more %v items %d; want empty exhausted page", emptyPoll.HasMore, len(emptyPoll.Items))
	}
	if emptyPoll.NextCursor != resumedBookmark {
		t.Errorf("empty tail poll next_cursor = %q; want existing bookmark %q", emptyPoll.NextCursor, resumedBookmark)
	}

	createArtifactTask(t, reopened, "artifact-replay-empty", "artifact-replay", TaskStatusRunning)
	initialEmpty, err := reopened.ListArtifacts("artifact-replay-empty", TaskArtifactListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListArtifacts(initial empty): %v", err)
	}
	if len(initialEmpty.Items) != 0 || initialEmpty.HasMore || initialEmpty.NextCursor != "" {
		t.Fatalf("initial empty page = items %d has_more %v next_cursor %q; want empty bookmark", len(initialEmpty.Items), initialEmpty.HasMore, initialEmpty.NextCursor)
	}
}

func TestArtifactReplay_100000RowsPreservesCountHashAndOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large-replay.db")
	db, store := openArtifactReplayStore(t, path, "artifact-replay-large")
	const taskID = "artifact-replay-large"
	const total = 100_000
	createArtifactTask(t, store, taskID, "artifact-replay", TaskStatusRunning)

	_, err := db.Exec(`
		WITH digits(d) AS (VALUES(0),(1),(2),(3),(4),(5),(6),(7),(8),(9)),
		numbers(n) AS (
			SELECT d0.d + 10*d1.d + 100*d2.d + 1000*d3.d + 10000*d4.d
			FROM digits d0
			CROSS JOIN digits d1
			CROSS JOIN digits d2
			CROSS JOIN digits d3
			CROSS JOIN digits d4
		)
		INSERT INTO task_artifacts(
			task_id,kind,event_type,channel,summary,payload_json,content_length,redacted,truncated,created_at
		)
		SELECT ?,?,?,?,printf('event-%06d',n),printf('{"ordinal":%d}',n),0,0,0,?
		FROM numbers
		ORDER BY n`,
		taskID,
		string(TaskArtifactKindRuntime),
		"text_delta",
		"stdout",
		"2030-01-02T03:04:05Z",
	)
	if err != nil {
		t.Fatalf("bulk insert replay fixtures: %v", err)
	}

	expectedHash := sha256.New()
	for ordinal := 0; ordinal < total; ordinal++ {
		_, _ = fmt.Fprintf(expectedHash, "%d|event-%06d\n", ordinal+1, ordinal)
	}
	actualHash := sha256.New()
	count := 0
	var lastSeq int64
	cursor := ""
	for {
		page, err := store.ListArtifacts(taskID, TaskArtifactListOptions{Cursor: cursor, Limit: maxArtifactPageSize})
		if err != nil {
			t.Fatalf("ListArtifacts replay page after %d rows: %v", count, err)
		}
		if len(page.Items) == 0 {
			t.Fatalf("replay ended with an empty page after %d rows", count)
		}
		for _, artifact := range page.Items {
			count++
			if lastSeq != 0 && artifact.Seq != lastSeq+1 {
				t.Fatalf("global seq gap/duplicate at row %d: got %d after %d", count, artifact.Seq, lastSeq)
			}
			if artifact.EventSeq != int64(count) {
				t.Fatalf("event_seq at row %d = %d; want %d", count, artifact.EventSeq, count)
			}
			_, _ = fmt.Fprintf(actualHash, "%d|%s\n", artifact.EventSeq, artifact.Summary)
			lastSeq = artifact.Seq
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" {
			t.Fatalf("replay page after %d rows has_more=true with empty next_cursor", count)
		}
		cursor = page.NextCursor
	}
	if count != total {
		t.Fatalf("replay count = %d; want %d", count, total)
	}
	if got, want := fmt.Sprintf("%x", actualHash.Sum(nil)), fmt.Sprintf("%x", expectedHash.Sum(nil)); got != want {
		t.Fatalf("replay hash = %s; want %s", got, want)
	}

	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("PRAGMA integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("PRAGMA integrity_check = %q; want ok", integrity)
	}
	foreignRows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_check: %v", err)
	}
	defer foreignRows.Close()
	if foreignRows.Next() {
		t.Fatal("PRAGMA foreign_key_check returned a violation")
	}
	if err := foreignRows.Err(); err != nil {
		t.Fatalf("PRAGMA foreign_key_check rows: %v", err)
	}
}
