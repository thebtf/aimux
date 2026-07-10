package loom

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func t005AssertExactProviderFence(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_list('pending_actions')`)
	t004Must(t, err)
	defer rows.Close()
	found := 0
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		t004Must(t, rows.Scan(&seq, &name, &unique, &origin, &partial))
		if name == pendingActionsProviderIndex {
			found++
			if unique != 1 || origin != "c" || partial != 0 {
				t.Fatalf("provider fence metadata unique=%d origin=%q partial=%d", unique, origin, partial)
			}
		}
	}
	t004Must(t, rows.Err())
	if found != 1 {
		t.Fatalf("provider fence definitions=%d want=1", found)
	}

	rows, err = db.Query(`PRAGMA index_info(` + pendingActionsProviderIndex + `)`)
	t004Must(t, err)
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var seq, cid int
		var name string
		t004Must(t, rows.Scan(&seq, &cid, &name))
		columns = append(columns, name)
	}
	t004Must(t, rows.Err())
	want := pendingActionsProviderIndexColumns
	if len(columns) != len(want) {
		t.Fatalf("provider fence columns=%v want=%v", columns, want)
	}
	for i := range want {
		if columns[i] != want[i] {
			t.Fatalf("provider fence columns=%v want=%v", columns, want)
		}
	}
}

func t005AssertSynchronousNormal(t *testing.T, db *sql.DB) {
	t.Helper()
	var level int
	t004Must(t, db.QueryRow(`PRAGMA synchronous`).Scan(&level))
	if level != 1 {
		t.Fatalf("PRAGMA synchronous=%d want exact NORMAL(1)", level)
	}
}

func TestTaskStore_MigrateV9RepairIsOneImmediateTransaction(t *testing.T) {
	_, db, trace := t004OpenTraceStore(t)
	_, err := db.Exec(`DROP INDEX ` + pendingActionsProviderIndex)
	t004Must(t, err)
	_, err = db.Exec(`CREATE UNIQUE INDEX ` + pendingActionsProviderIndex + ` ON pending_actions(provider_request_id,connection_generation)`)
	t004Must(t, err)

	trace.Reset()
	if err := migrateV9(db); err != nil {
		t.Fatalf("migrateV9 repair: %v", err)
	}
	entries := trace.Snapshot()
	positions := map[string]int{}
	for i, entry := range entries {
		sqlText := strings.ToUpper(entry.SQL)
		switch {
		case entry.Op == "BEGIN IMMEDIATE":
			positions["begin"] = i
		case strings.Contains(sqlText, "PRAGMA INDEX_LIST"):
			positions["inspect"] = i
		case strings.HasPrefix(sqlText, "DROP INDEX"):
			positions["drop"] = i
		case strings.HasPrefix(sqlText, "CREATE UNIQUE INDEX"):
			positions["create"] = i
		case entry.Op == "COMMIT":
			positions["commit"] = i
		}
	}
	for _, name := range []string{"begin", "inspect", "drop", "create", "commit"} {
		if _, ok := positions[name]; !ok {
			t.Fatalf("migration trace missing %s: %#v", name, entries)
		}
	}
	if !(positions["begin"] < positions["inspect"] && positions["inspect"] < positions["drop"] && positions["drop"] < positions["create"] && positions["create"] < positions["commit"]) {
		t.Fatalf("migration repair order=%v trace=%#v", positions, entries)
	}
	connID := entries[positions["begin"]].ConnID
	for i := positions["begin"]; i <= positions["commit"]; i++ {
		if entries[i].ConnID != connID {
			t.Fatalf("migration repair crossed connections: begin=%d entry[%d]=%#v", connID, i, entries[i])
		}
	}
	t005AssertExactProviderFence(t, db)
}

func t005PrepareLegacyV9Fixture(t *testing.T, dsn string) {
	t.Helper()
	setup, err := sql.Open("sqlite", dsn)
	t004Must(t, err)
	setup.SetMaxOpenConns(1)
	_, err = setup.Exec(t004LiteralV8Schema)
	t004Must(t, err)
	_, err = setup.Exec(createPendingActionsTable)
	t004Must(t, err)
	_, err = setup.Exec(`CREATE UNIQUE INDEX ` + pendingActionsProviderIndex + ` ON pending_actions(provider_request_id,connection_generation)`)
	t004Must(t, err)
	_, err = setup.Exec(`INSERT INTO tasks(id,status,worker_type,project_id,prompt,created_at) VALUES('v9-hardening-row','running','cli','p','p','2030-01-01T00:00:00Z')`)
	t004Must(t, err)
	_, err = setup.Exec(`INSERT INTO pending_actions(id,task_id,kind,status,provider_request_id,connection_generation,request_json,expires_at,created_at) VALUES('v9-hardening-action','v9-hardening-row','question','pending','legacy-provider',9,'{"safe":true}','2030-01-02T00:00:00Z','2030-01-01T00:00:00Z')`)
	t004Must(t, err)
	t004Must(t, setup.Close())
}

func t005AssertLegacyV9Row(t *testing.T, db *sql.DB) {
	t.Helper()
	var status, requestJSON string
	t004Must(t, db.QueryRow(`SELECT status,request_json FROM pending_actions WHERE id='v9-hardening-action'`).Scan(&status, &requestJSON))
	if status != "pending" || requestJSON != `{"safe":true}` {
		t.Fatalf("legacy v9 row changed: status=%q request_json=%q", status, requestJSON)
	}
}

func TestTaskStore_MigrateV9ConcurrentConstructorsRepairLegacyFence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-constructors-v9.db")
	dsn := path + "?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000"
	t005PrepareLegacyV9Fixture(t, dsn)
	db, err := sql.Open("sqlite", dsn)
	t004Must(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := NewTaskStore(db, "concurrent-v9-"+string(rune('a'+i)))
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent NewTaskStore: %v", err)
		}
	}
	t005AssertExactProviderFence(t, db)
	t005AssertLegacyV9Row(t, db)
	t005AssertSynchronousNormal(t, db)
}

func TestTaskStore_MigrateV9ConcurrentConnectionsRepairLegacyFence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-connections-v9.db")
	dsn := path + "?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000"
	t005PrepareLegacyV9Fixture(t, dsn)
	dbs := make([]*sql.DB, 2)
	for i := range dbs {
		db, err := sql.Open("sqlite", dsn)
		t004Must(t, err)
		db.SetMaxOpenConns(1)
		dbs[i] = db
		t.Cleanup(func() { _ = db.Close() })
	}
	start := make(chan struct{})
	errs := make(chan error, len(dbs))
	var wg sync.WaitGroup
	for _, db := range dbs {
		wg.Add(1)
		go func(db *sql.DB) {
			defer wg.Done()
			<-start
			errs <- migrateV9(db)
		}(db)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrateV9: %v", err)
		}
	}
	t005AssertExactProviderFence(t, dbs[0])
	t005AssertLegacyV9Row(t, dbs[0])
}

func TestTaskStore_FreshFileConstructorEndsInWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh-file.db")
	db, err := sql.Open("sqlite", path+"?_busy_timeout=5000")
	t004Must(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := NewTaskStore(db, "fresh-file"); err != nil {
		t.Fatalf("NewTaskStore fresh file: %v", err)
	}
	var mode string
	t004Must(t, db.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("fresh file journal_mode=%q want=wal", mode)
	}
	t005AssertSynchronousNormal(t, db)
}

func TestTaskStore_SynchronousNormalUsesPinnedVerifiedConnection(t *testing.T) {
	_, db, trace := t004OpenTraceStore(t)
	trace.Reset()
	if err := ensureTaskStoreSynchronousNormal(db); err != nil {
		t.Fatalf("ensureTaskStoreSynchronousNormal: %v", err)
	}
	entries := trace.Snapshot()
	setIndex, readIndex := -1, -1
	for i, entry := range entries {
		sqlText := strings.ToUpper(entry.SQL)
		switch sqlText {
		case "PRAGMA SYNCHRONOUS=NORMAL":
			setIndex = i
		case "PRAGMA SYNCHRONOUS":
			readIndex = i
		}
	}
	if setIndex < 0 || readIndex < 0 || setIndex >= readIndex {
		t.Fatalf("synchronous verification trace=%#v", entries)
	}
	if entries[setIndex].ConnID != entries[readIndex].ConnID {
		t.Fatalf("synchronous set/read crossed connections: set=%#v read=%#v", entries[setIndex], entries[readIndex])
	}
	t005AssertSynchronousNormal(t, db)
}

func t005MainDatabaseFilename(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`PRAGMA database_list`)
	t004Must(t, err)
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, filename string
		t004Must(t, rows.Scan(&seq, &name, &filename))
		if name == "main" {
			return filename
		}
	}
	t004Must(t, rows.Err())
	t.Fatal("PRAGMA database_list has no main database")
	return ""
}

func TestTaskStore_MemoryJournalExceptionRequiresMemoryBacking(t *testing.T) {
	t.Run("shared-memory", func(t *testing.T) {
		db, err := sql.Open("sqlite", "file:t005-shared-memory?cache=shared&mode=memory")
		t004Must(t, err)
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		if filename := t005MainDatabaseFilename(t, db); filename != "" {
			t.Fatalf("shared-memory main filename=%q want empty", filename)
		}
		if _, err := NewTaskStore(db, "shared-memory"); err != nil {
			t.Fatalf("NewTaskStore shared memory: %v", err)
		}
		var mode string
		t004Must(t, db.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
		if !strings.EqualFold(mode, "memory") {
			t.Fatalf("shared-memory journal_mode=%q want=memory", mode)
		}
	})

	t.Run("file-backed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "file-backed-memory.db")
		db, err := sql.Open("sqlite", path+"?_busy_timeout=5000")
		t004Must(t, err)
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		var mode string
		t004Must(t, db.QueryRow(`PRAGMA journal_mode=MEMORY`).Scan(&mode))
		if !strings.EqualFold(mode, "memory") {
			t.Fatalf("fixture journal_mode=%q want=memory", mode)
		}
		if filename := t005MainDatabaseFilename(t, db); filename == "" {
			t.Fatal("file-backed main filename is empty")
		}
		if _, err := NewTaskStore(db, "file-backed-memory"); err != nil {
			t.Fatalf("NewTaskStore file-backed memory mode: %v", err)
		}
		t004Must(t, db.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
		if !strings.EqualFold(mode, "wal") {
			t.Fatalf("file-backed journal_mode=%q want=wal", mode)
		}
	})
}
