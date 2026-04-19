package loom

import (
	"database/sql"
	"testing"
	"time"
)

// TestTaskStore_Create_StampsDaemonUUID asserts that after SetDaemonUUID,
// newly created task rows have daemon_uuid equal to the configured value.
func TestTaskStore_Create_StampsDaemonUUID(t *testing.T) {
	store := newTestStore(t)

	const wantUUID = "deadbeef00112233445566778899aabb"
	store.SetDaemonUUID(wantUUID)

	task := makeTask("t-uuid", "proj", TaskStatusPending)
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var gotUUID sql.NullString
	row := store.db.QueryRow(`SELECT daemon_uuid FROM tasks WHERE id = 't-uuid'`)
	if err := row.Scan(&gotUUID); err != nil {
		t.Fatalf("scan daemon_uuid: %v", err)
	}
	if !gotUUID.Valid || gotUUID.String != wantUUID {
		t.Errorf("daemon_uuid = %q, want %q", gotUUID.String, wantUUID)
	}
}

// TestTaskStore_Create_StampsLastSeenAt asserts that newly created task rows
// have last_seen_at within 1 second of the insert time.
func TestTaskStore_Create_StampsLastSeenAt(t *testing.T) {
	store := newTestStore(t)

	before := time.Now().UTC().Add(-time.Second)

	task := makeTask("t-lsat", "proj", TaskStatusPending)
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	after := time.Now().UTC().Add(time.Second)

	var lastSeenStr sql.NullString
	row := store.db.QueryRow(`SELECT last_seen_at FROM tasks WHERE id = 't-lsat'`)
	if err := row.Scan(&lastSeenStr); err != nil {
		t.Fatalf("scan last_seen_at: %v", err)
	}
	if !lastSeenStr.Valid || lastSeenStr.String == "" {
		t.Fatal("last_seen_at is NULL or empty after Create")
	}

	lastSeen, err := time.Parse(time.RFC3339, lastSeenStr.String)
	if err != nil {
		t.Fatalf("parse last_seen_at %q: %v", lastSeenStr.String, err)
	}
	if lastSeen.Before(before) || lastSeen.After(after) {
		t.Errorf("last_seen_at %v outside expected window [%v, %v]", lastSeen, before, after)
	}
}

// TestTaskStore_Create_EmptyDaemonUUID asserts that when SetDaemonUUID has not
// been called, daemon_uuid is stored as an empty string (not NULL), keeping the
// column consistent for future reconciliation queries.
func TestTaskStore_Create_EmptyDaemonUUID(t *testing.T) {
	store := newTestStore(t)
	// Do NOT call SetDaemonUUID — daemonUUID field remains "".

	task := makeTask("t-nouuid", "proj", TaskStatusPending)
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var gotUUID sql.NullString
	row := store.db.QueryRow(`SELECT daemon_uuid FROM tasks WHERE id = 't-nouuid'`)
	if err := row.Scan(&gotUUID); err != nil {
		t.Fatalf("scan daemon_uuid: %v", err)
	}
	// Create() must write an explicit empty string — not NULL — so that
	// reconciliation queries using (daemon_uuid != ? OR daemon_uuid IS NULL)
	// reliably exclude rows from the current daemon even when UUID was not
	// configured. A NULL would make the IS NULL branch match unintentionally.
	if !gotUUID.Valid || gotUUID.String != "" {
		t.Errorf("daemon_uuid = %#v, want valid empty string (not NULL) when not configured", gotUUID)
	}
}
