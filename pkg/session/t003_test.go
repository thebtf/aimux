package session_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

// TestSnapshotJob_StampsDaemonUUID asserts that a new job row has
// daemon_uuid = GetDaemonUUID() after SnapshotJob.
func TestSnapshotJob_StampsDaemonUUID(t *testing.T) {
	session.ResetDaemonUUID()
	wantUUID := session.GetDaemonUUID()

	dir := t.TempDir()
	store, err := session.NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	jm := session.NewJobManager()
	jm.SetStore(store)
	job := jm.Create("sess-1", "codex")
	jm.StartJob(job.ID, 100)

	// SnapshotJob is called implicitly by Create when store is set.
	// Call it explicitly again to snapshot the running state.
	if err := store.SnapshotJob(jm.Get(job.ID)); err != nil {
		t.Fatalf("SnapshotJob: %v", err)
	}

	db := store.DB()
	var gotUUID sql.NullString
	row := db.QueryRow(`SELECT daemon_uuid FROM jobs WHERE id = ?`, job.ID)
	if err := row.Scan(&gotUUID); err != nil {
		t.Fatalf("scan daemon_uuid: %v", err)
	}
	if !gotUUID.Valid || gotUUID.String != wantUUID {
		t.Errorf("daemon_uuid = %q, want %q", gotUUID.String, wantUUID)
	}
}

// TestSnapshotJob_StampsLastSeenAt asserts that a new job row has
// last_seen_at within 1 second of insert time.
func TestSnapshotJob_StampsLastSeenAt(t *testing.T) {
	session.ResetDaemonUUID()

	dir := t.TempDir()
	store, err := session.NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	before := time.Now().UTC().Add(-time.Second)

	jm := session.NewJobManager()
	job := jm.Create("sess-1", "codex")

	if err := store.SnapshotJob(jm.Get(job.ID)); err != nil {
		t.Fatalf("SnapshotJob: %v", err)
	}

	after := time.Now().UTC().Add(time.Second)

	db := store.DB()
	var lastSeenStr sql.NullString
	row := db.QueryRow(`SELECT last_seen_at FROM jobs WHERE id = ?`, job.ID)
	if err := row.Scan(&lastSeenStr); err != nil {
		t.Fatalf("scan last_seen_at: %v", err)
	}
	if !lastSeenStr.Valid || lastSeenStr.String == "" {
		t.Fatal("last_seen_at is NULL or empty after SnapshotJob")
	}

	lastSeen, err := time.Parse(time.RFC3339, lastSeenStr.String)
	if err != nil {
		t.Fatalf("parse last_seen_at %q: %v", lastSeenStr.String, err)
	}
	if lastSeen.Before(before) || lastSeen.After(after) {
		t.Errorf("last_seen_at %v outside expected window [%v, %v]", lastSeen, before, after)
	}
}

// TestSnapshotSession_StampsDaemonUUID asserts that a session row has
// daemon_uuid = GetDaemonUUID() after SnapshotSession.
func TestSnapshotSession_StampsDaemonUUID(t *testing.T) {
	session.ResetDaemonUUID()
	wantUUID := session.GetDaemonUUID()

	dir := t.TempDir()
	store, err := session.NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	reg := session.NewRegistry()
	sess := reg.Create("codex", types.SessionModeLive, "/tmp")

	if err := store.SnapshotSession(sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	db := store.DB()
	var gotUUID sql.NullString
	row := db.QueryRow(`SELECT daemon_uuid FROM sessions WHERE id = ?`, sess.ID)
	if err := row.Scan(&gotUUID); err != nil {
		t.Fatalf("scan daemon_uuid: %v", err)
	}
	if !gotUUID.Valid || gotUUID.String != wantUUID {
		t.Errorf("daemon_uuid = %q, want %q", gotUUID.String, wantUUID)
	}
}

// TestSnapshotAll_StampsDaemonUUID asserts that SnapshotAll stamps
// daemon_uuid on both session and job rows.
func TestSnapshotAll_StampsDaemonUUID(t *testing.T) {
	session.ResetDaemonUUID()
	wantUUID := session.GetDaemonUUID()

	dir := t.TempDir()
	store, err := session.NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	reg := session.NewRegistry()
	sess := reg.Create("codex", types.SessionModeLive, "/tmp")

	jm := session.NewJobManager()
	job := jm.Create(sess.ID, "codex")
	jm.StartJob(job.ID, 9999)

	if err := store.SnapshotAll(reg, jm); err != nil {
		t.Fatalf("SnapshotAll: %v", err)
	}

	db := store.DB()

	var sessUUID sql.NullString
	if err := db.QueryRow(`SELECT daemon_uuid FROM sessions WHERE id = ?`, sess.ID).Scan(&sessUUID); err != nil {
		t.Fatalf("scan session daemon_uuid: %v", err)
	}
	if !sessUUID.Valid || sessUUID.String != wantUUID {
		t.Errorf("session daemon_uuid = %q, want %q", sessUUID.String, wantUUID)
	}

	var jobUUID, lastSeenStr sql.NullString
	if err := db.QueryRow(`SELECT daemon_uuid, last_seen_at FROM jobs WHERE id = ?`, job.ID).Scan(&jobUUID, &lastSeenStr); err != nil {
		t.Fatalf("scan job daemon_uuid/last_seen_at: %v", err)
	}
	if !jobUUID.Valid || jobUUID.String != wantUUID {
		t.Errorf("job daemon_uuid = %q, want %q", jobUUID.String, wantUUID)
	}
	if !lastSeenStr.Valid || lastSeenStr.String == "" {
		t.Error("job last_seen_at is NULL or empty after SnapshotAll")
	}
}
