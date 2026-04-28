package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/thebtf/aimux/pkg/tenant"
)

// tenantCtxKey is the unexported context key for storing a TenantContext.
// Using a private type prevents key collisions with other packages.
type tenantCtxKey struct{}

// WithTenant stores tc in ctx and returns the derived context.
// Handlers that need to perform tenant-scoped DB operations must wrap their
// context with WithTenant before calling TenantScopedStore methods.
func WithTenant(ctx context.Context, tc tenant.TenantContext) context.Context {
	return context.WithValue(ctx, tenantCtxKey{}, tc)
}

// tenantFromCtx extracts the TenantContext from ctx.
// Returns (TenantContext, true) when present, (zero, false) when absent.
func tenantFromCtx(ctx context.Context) (tenant.TenantContext, bool) {
	tc, ok := ctx.Value(tenantCtxKey{}).(tenant.TenantContext)
	return tc, ok
}

// ErrMissingTenantContext is returned by TenantScopedStore methods when the
// calling context carries no TenantContext. Callers must inject a TenantContext
// via WithTenant before calling store methods.
var ErrMissingTenantContext = errors.New("session: missing TenantContext in context (inject via WithTenant)")

// TenantScopedStore wraps *Store and automatically injects tenant_id predicates
// into all SELECT queries and tenant_id values into all INSERT operations.
//
// Design principles:
//   - All SELECT queries include AND tenant_id = ? to prevent cross-tenant reads.
//   - All INSERT operations read TenantContext from ctx; return ErrMissingTenantContext
//     if absent — NEVER panic.
//   - Cross-tenant get/list returns empty results, NOT a permission error, to
//     prevent information disclosure about other tenants' IDs (defence-in-depth).
type TenantScopedStore struct {
	store *Store
}

// NewTenantScopedStore wraps store with automatic tenant isolation.
func NewTenantScopedStore(store *Store) *TenantScopedStore {
	return &TenantScopedStore{store: store}
}

// SnapshotSession upserts sess into SQLite, stamping tenant_id from the TenantContext
// in ctx. Returns ErrMissingTenantContext if ctx carries no TenantContext.
//
// A shallow copy of sess is made before stamping TenantID; the caller's original
// Session pointer is not mutated.
func (ts *TenantScopedStore) SnapshotSession(ctx context.Context, sess *Session) error {
	tc, ok := tenantFromCtx(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("%w", ErrMissingTenantContext)
	}

	// Stamp the copy so the persisted row matches the tenant context.
	stamped := *sess
	stamped.TenantID = tc.TenantID

	return ts.store.SnapshotSession(&stamped)
}

// QuerySessions returns all session rows owned by the tenant in ctx.
// Returns an empty slice (not an error) when no rows match.
func (ts *TenantScopedStore) QuerySessions(ctx context.Context) ([]*Session, error) {
	tc, ok := tenantFromCtx(ctx)
	if !ok || tc.TenantID == "" {
		return nil, fmt.Errorf("%w", ErrMissingTenantContext)
	}

	rows, err := ts.store.db.QueryContext(ctx, `
		SELECT id, cli, mode, COALESCE(cli_session_id,''), pid, status, turns, COALESCE(cwd,''),
		       COALESCE(metadata_json,''), created_at, last_active_at, tenant_id
		FROM sessions
		WHERE tenant_id = ?
		ORDER BY created_at DESC
	`, tc.TenantID)
	if err != nil {
		return nil, fmt.Errorf("QuerySessions: %w", err)
	}
	defer rows.Close()

	var result []*Session
	for rows.Next() {
		s, err := scanSessionRow(rows)
		if err != nil {
			return nil, fmt.Errorf("QuerySessions scan: %w", err)
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("QuerySessions iterate: %w", err)
	}
	return result, nil
}

// GetSession returns the session with the given id, scoped to the tenant in ctx.
// Returns (nil, nil) when the session does not exist OR belongs to a different
// tenant — callers cannot distinguish the two cases (information hiding per ADR-09).
func (ts *TenantScopedStore) GetSession(ctx context.Context, id string) (*Session, error) {
	tc, ok := tenantFromCtx(ctx)
	if !ok || tc.TenantID == "" {
		return nil, fmt.Errorf("%w", ErrMissingTenantContext)
	}

	row := ts.store.db.QueryRowContext(ctx, `
		SELECT id, cli, mode, COALESCE(cli_session_id,''), pid, status, turns, COALESCE(cwd,''),
		       COALESCE(metadata_json,''), created_at, last_active_at, tenant_id
		FROM sessions
		WHERE id = ? AND tenant_id = ?
	`, id, tc.TenantID)

	s, err := scanSessionRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetSession: %w", err)
	}
	return s, nil
}

// rowScanner abstracts *sql.Row and *sql.Rows so scanSessionRow works with both.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSessionRow scans one session row from r.
// Returns sql.ErrNoRows when the row does not exist (for *sql.Row callers).
func scanSessionRow(r rowScanner) (*Session, error) {
	var (
		metadataJSON    string
		createdAtStr    string
		lastActiveAtStr string
		tenantID        string
	)
	s := &Session{}
	if err := r.Scan(
		&s.ID, &s.CLI, &s.Mode, &s.CLISessionID, &s.PID,
		&s.Status, &s.Turns, &s.CWD,
		&metadataJSON, &createdAtStr, &lastActiveAtStr, &tenantID,
	); err != nil {
		return nil, err
	}
	s.TenantID = tenantID

	if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
		s.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, lastActiveAtStr); err == nil {
		s.LastActiveAt = t
	}
	if metadataJSON != "" && metadataJSON != "null" {
		_ = json.Unmarshal([]byte(metadataJSON), &s.Metadata)
	}
	return s, nil
}
