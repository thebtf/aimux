package session_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
	_ "modernc.org/sqlite"
)

// withTenant is a test helper that stores a TenantContext in the context.
func withTenant(ctx context.Context, tc tenant.TenantContext) context.Context {
	return session.WithTenant(ctx, tc)
}

// newTenantContext constructs a plain TenantContext for testing.
func newTenantContext(id string) tenant.TenantContext {
	return tenant.TenantContext{
		TenantID:         id,
		RequestStartedAt: time.Now(),
	}
}

// openStore opens a Store at the given path, failing the test on error.
func openStore(t *testing.T, path string) *session.Store {
	t.Helper()
	s, err := session.NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// snapshotSession creates a minimal Session in SQLite via SnapshotSession.
// The session's TenantID must match what the test expects.
func makeSession(id, tenantID string) *session.Session {
	now := time.Now()
	return &session.Session{
		ID:           id,
		CLI:          "codex",
		Mode:         types.SessionModeLive,
		Status:       types.SessionStatusCreated,
		TenantID:     tenantID,
		CreatedAt:    now,
		LastActiveAt: now,
	}
}

// TestTenantScopedStore_CrossTenantQueryReturnsEmpty verifies that tenant A
// cannot see sessions created by tenant B. The store must return an empty
// result, not a permission error (defence-in-depth / information hiding).
func TestTenantScopedStore_CrossTenantQueryReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, filepath.Join(dir, "cross.db"))

	tcA := newTenantContext("tenant-a")
	tcB := newTenantContext("tenant-b")

	ctxA := withTenant(context.Background(), tcA)
	ctxB := withTenant(context.Background(), tcB)

	ts := session.NewTenantScopedStore(store)

	// Tenant A inserts a session.
	if err := ts.SnapshotSession(ctxA, makeSession("sess-a", "tenant-a")); err != nil {
		t.Fatalf("SnapshotSession(A): %v", err)
	}

	// Tenant B tries to read it — must get nothing (ErrNotFound or empty list).
	rows, err := ts.QuerySessions(ctxB)
	if err != nil {
		t.Fatalf("QuerySessions(B): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("QuerySessions(B) returned %d rows, want 0 (cross-tenant isolation violated)", len(rows))
	}

	// Tenant A can see its own session.
	rowsA, err := ts.QuerySessions(ctxA)
	if err != nil {
		t.Fatalf("QuerySessions(A): %v", err)
	}
	if len(rowsA) != 1 {
		t.Errorf("QuerySessions(A) returned %d rows, want 1", len(rowsA))
	}
}

// TestTenantScopedStore_InsertRequiresTenantContext verifies that SnapshotSession
// returns an error (not a panic) when the context carries no TenantContext.
func TestTenantScopedStore_InsertRequiresTenantContext(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, filepath.Join(dir, "nocontext.db"))
	ts := session.NewTenantScopedStore(store)

	// Plain background context — no tenant injected.
	err := ts.SnapshotSession(context.Background(), makeSession("sess-1", ""))
	if err == nil {
		t.Fatal("expected error when TenantContext absent, got nil")
	}
}

// TestTenantScopedStore_LegacyDefaultCompat verifies that sessions inserted with
// the legacy-default tenant are readable via the legacy-default TenantContext.
// This simulates pre-migration rows (tenant_id = 'legacy-default' by DEFAULT).
func TestTenantScopedStore_LegacyDefaultCompat(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// Create the DB (runs migrations, sets DEFAULT 'legacy-default').
	store := openStore(t, dbPath)

	// Insert a row directly with tenant_id='legacy-default' to simulate pre-migration data.
	db := store.DB()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT INTO sessions (id, cli, mode, status, created_at, last_active_at, tenant_id)
		VALUES ('legacy-sess', 'codex', 'live', 'created', ?, ?, 'legacy-default')
	`, now, now)
	if err != nil {
		t.Fatalf("direct insert legacy row: %v", err)
	}

	// LegacyDefault TenantContext should be able to see it.
	tc := newTenantContext(tenant.LegacyDefault)
	ctx := withTenant(context.Background(), tc)
	ts := session.NewTenantScopedStore(store)

	rows, err := ts.QuerySessions(ctx)
	if err != nil {
		t.Fatalf("QuerySessions(legacy-default): %v", err)
	}
	if len(rows) == 0 {
		t.Error("legacy-default tenant should see pre-migration rows, got 0")
	}
	found := false
	for _, s := range rows {
		if s.ID == "legacy-sess" {
			found = true
		}
	}
	if !found {
		t.Error("legacy-sess not found in legacy-default query results")
	}
}

// TestTenantScopedStore_IsolatedConcurrentInsert verifies that concurrent writes
// from two tenants do not intermix: each tenant reads only its own rows, even
// under parallel execution (run with -race to detect data races).
func TestTenantScopedStore_IsolatedConcurrentInsert(t *testing.T) {
	dir := t.TempDir()
	store := openStore(t, filepath.Join(dir, "concurrent.db"))
	ts := session.NewTenantScopedStore(store)

	tcA := newTenantContext("tenant-concurrent-a")
	tcB := newTenantContext("tenant-concurrent-b")
	ctxA := withTenant(context.Background(), tcA)
	ctxB := withTenant(context.Background(), tcB)

	const n = 5

	errCh := make(chan error, n*2)

	// Concurrent inserts for both tenants.
	for i := range n {
		go func(i int) {
			id := "sess-a-" + string(rune('0'+i))
			errCh <- ts.SnapshotSession(ctxA, makeSession(id, "tenant-concurrent-a"))
		}(i)
		go func(i int) {
			id := "sess-b-" + string(rune('0'+i))
			errCh <- ts.SnapshotSession(ctxB, makeSession(id, "tenant-concurrent-b"))
		}(i)
	}

	for range n * 2 {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent SnapshotSession: %v", err)
		}
	}

	rowsA, err := ts.QuerySessions(ctxA)
	if err != nil {
		t.Fatalf("QuerySessions(A): %v", err)
	}
	for _, s := range rowsA {
		if s.TenantID != "tenant-concurrent-a" {
			t.Errorf("tenant A query returned row with tenant_id=%q", s.TenantID)
		}
	}
	if len(rowsA) != n {
		t.Errorf("tenant A got %d rows, want %d", len(rowsA), n)
	}

	rowsB, err := ts.QuerySessions(ctxB)
	if err != nil {
		t.Fatalf("QuerySessions(B): %v", err)
	}
	for _, s := range rowsB {
		if s.TenantID != "tenant-concurrent-b" {
			t.Errorf("tenant B query returned row with tenant_id=%q", s.TenantID)
		}
	}
	if len(rowsB) != n {
		t.Errorf("tenant B got %d rows, want %d", len(rowsB), n)
	}
}

// TestTenantIDColumn_ExistsAfterMigration verifies that the migration added
// tenant_id to the sessions table and that schema_version was bumped to 4.
func TestTenantIDColumn_ExistsAfterMigration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v4.db")

	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	db := store.DB()

	if !columnExists(t, db, "sessions", "tenant_id") {
		t.Error("sessions.tenant_id column missing after v4 migration")
	}

	if v := schemaVersion(t, db); v != 4 {
		t.Errorf("schema_version = %d, want 4", v)
	}
}

// TestTenantIDColumn_DefaultIsLegacyDefault verifies that inserting a session
// row without specifying tenant_id results in the DEFAULT value 'legacy-default'.
func TestTenantIDColumn_DefaultIsLegacyDefault(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "default.db")

	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	db := store.DB()
	now := time.Now().UTC().Format(time.RFC3339)
	// Insert without tenant_id to exercise the DEFAULT.
	_, err = db.Exec(`
		INSERT INTO sessions (id, cli, mode, status, created_at, last_active_at)
		VALUES ('def-sess', 'codex', 'live', 'created', ?, ?)
	`, now, now)
	if err != nil {
		t.Fatalf("insert without tenant_id: %v", err)
	}

	var tenantID string
	row := db.QueryRow(`SELECT tenant_id FROM sessions WHERE id = 'def-sess'`)
	if err := row.Scan(&tenantID); err != nil {
		t.Fatalf("scan tenant_id: %v", err)
	}
	if tenantID != tenant.LegacyDefault {
		t.Errorf("default tenant_id = %q, want %q", tenantID, tenant.LegacyDefault)
	}
}

// TestMigrateV4_ExistingDB verifies that a v3 database (before tenant_id column)
// can be opened via NewStore (which applies migration v4) without error, and that
// existing rows receive the default tenant_id value.
func TestMigrateV4_ExistingDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v3.db")

	// Build a v3 schema manually (as if migration ran up to v3).
	rawDB, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	rawDB.SetMaxOpenConns(1)

	_, err = rawDB.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			cli TEXT NOT NULL,
			mode TEXT NOT NULL,
			cli_session_id TEXT,
			pid INTEGER DEFAULT 0,
			status TEXT NOT NULL,
			turns INTEGER DEFAULT 0,
			cwd TEXT,
			metadata_json TEXT,
			created_at TEXT NOT NULL,
			last_active_at TEXT NOT NULL,
			daemon_uuid TEXT,
			aborted_at TEXT,
			aborted_job_ids TEXT
		);
		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			cli TEXT NOT NULL,
			status TEXT NOT NULL,
			progress TEXT,
			content TEXT,
			exit_code INTEGER DEFAULT 0,
			error_json TEXT,
			poll_count INTEGER DEFAULT 0,
			pheromones_json TEXT,
			pipeline_json TEXT,
			pid INTEGER DEFAULT 0,
			created_at TEXT NOT NULL,
			progress_updated_at TEXT NOT NULL,
			completed_at TEXT,
			daemon_uuid TEXT,
			last_seen_at TEXT,
			aborted_at TEXT,
			FOREIGN KEY (session_id) REFERENCES sessions(id)
		);
		CREATE TABLE IF NOT EXISTS schema_version (version INT NOT NULL);
		INSERT INTO schema_version (version) VALUES (3);
	`)
	if err != nil {
		t.Fatalf("build v3 schema: %v", err)
	}

	// Insert a pre-migration session row (no tenant_id column yet).
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = rawDB.Exec(`
		INSERT INTO sessions (id, cli, mode, status, created_at, last_active_at)
		VALUES ('v3-sess', 'codex', 'live', 'created', ?, ?)
	`, now, now)
	if err != nil {
		t.Fatalf("insert v3 session: %v", err)
	}
	rawDB.Close()

	// Open via NewStore — must apply v4 migration.
	store, err := session.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore on v3 db: %v", err)
	}
	defer store.Close()

	db := store.DB()

	if !columnExists(t, db, "sessions", "tenant_id") {
		t.Fatal("sessions.tenant_id column missing after v3→v4 migration")
	}

	if v := schemaVersion(t, db); v != 4 {
		t.Errorf("schema_version = %d, want 4", v)
	}

	// Existing row must have tenant_id = 'legacy-default' (NOT NULL DEFAULT).
	var tenantID string
	row := db.QueryRow(`SELECT tenant_id FROM sessions WHERE id = 'v3-sess'`)
	if err := row.Scan(&tenantID); err != nil {
		t.Fatalf("scan v3-sess tenant_id: %v", err)
	}
	if tenantID != tenant.LegacyDefault {
		t.Errorf("v3-sess tenant_id = %q after migration, want %q", tenantID, tenant.LegacyDefault)
	}
}
