package loom

import (
	"context"
	"fmt"
	"time"
)

// AuditEvent is emitted by TenantScopedLoomEngine on security-relevant decisions
// (quota rejections, cross-tenant denials). Phase 4 will provide a concrete
// AuditLog implementation; until then callers inject a fake recorder in tests.
type AuditEvent struct {
	// Type is the event category (e.g. "loom_submit_rejected").
	Type string

	// TenantID is the tenant that triggered the event.
	TenantID string

	// AttemptedAt is the wall-clock time of the attempt.
	AttemptedAt time.Time

	// CurrentDepth is the number of in-flight tasks counted at the time of rejection.
	CurrentDepth int

	// Limit is the configured quota cap.
	Limit int

	// ToolName is the worker type that was attempted.
	ToolName string
}

// AuditEmitter is the minimal interface that Phase 4 AuditLog will implement.
// TenantScopedLoomEngine depends on this interface rather than the concrete type
// to avoid a compile-time dependency on the not-yet-implemented pkg/audit package.
// Stub it in tests with fakeAuditEmitter.
type AuditEmitter interface {
	Emit(e AuditEvent)
}

// noopAuditEmitter discards all events. Used when no AuditEmitter is configured.
type noopAuditEmitter struct{}

func (noopAuditEmitter) Emit(AuditEvent) {}

// TenantQuotaConfig holds per-tenant quota and audit settings for
// TenantScopedLoomEngine. A nil pointer means "no quota, no audit".
type TenantQuotaConfig struct {
	// MaxLoomTasksQueued is the maximum number of in-flight tasks
	// (pending + dispatched + running) allowed for this tenant at one time.
	// Zero means unlimited.
	MaxLoomTasksQueued int

	// AuditEmitter receives events on quota rejections. If nil, events are discarded.
	AuditEmitter AuditEmitter
}

// TenantScopedLoomEngine is an interface-based decorator over *LoomEngine that
// scopes Submit/Get/List/Cancel operations to a single tenant (CHK076 fix).
// It holds a tenantID and injects it into every operation without forking the
// underlying engine struct. Cross-tenant operations return ErrTaskNotFound (not
// ErrCrossTenantDenied / 403) to prevent disclosure of foreign task existence
// (CHK079 fix, defence-in-depth).
//
// Construct via NewTenantScopedEngine. Do NOT instantiate the struct directly.
type TenantScopedLoomEngine struct {
	engine   *LoomEngine
	tenantID string
	quota    *TenantQuotaConfig
	auditor  AuditEmitter
}

// NewTenantScopedEngine creates a TenantScopedLoomEngine wrapping engine with the
// given tenantID. quota may be nil to disable quota enforcement and audit emission.
func NewTenantScopedEngine(engine *LoomEngine, tenantID string, quota *TenantQuotaConfig) *TenantScopedLoomEngine {
	var auditor AuditEmitter = noopAuditEmitter{}
	if quota != nil && quota.AuditEmitter != nil {
		auditor = quota.AuditEmitter
	}
	return &TenantScopedLoomEngine{
		engine:   engine,
		tenantID: tenantID,
		quota:    quota,
		auditor:  auditor,
	}
}

// Submit creates a task scoped to this tenant and dispatches it.
// Returns ErrLoomQuotaExceeded when the tenant's in-flight count has reached
// MaxLoomTasksQueued (FR-17, T060). The quota check uses a live SQL COUNT to
// avoid race-window issues from cached state.
func (t *TenantScopedLoomEngine) Submit(ctx context.Context, req TaskRequest) (string, error) {
	// W2 (AIMUX-12 v5.1.0): serialize quota-check + insert per-tenant via
	// LoomEngine's tenantSubmitLock. Closes the TOCTOU race where N concurrent
	// Submits read depth=cap-1, all pass check, all insert → cap exceeded by
	// goroutine count. Different tenants remain parallel.
	if t.quota != nil && t.quota.MaxLoomTasksQueued > 0 {
		lock := t.engine.TenantSubmitLock(t.tenantID)
		lock.Lock()
		defer lock.Unlock()

		// T060: quota enforcement — live SQL count, NOT cached state.
		depth, err := t.engine.store.CountForTenant(t.tenantID)
		if err != nil {
			return "", fmt.Errorf("loom tenant: quota check: %w", err)
		}
		if depth >= t.quota.MaxLoomTasksQueued {
			// Emit audit event before returning the error.
			t.auditor.Emit(AuditEvent{
				Type:         "loom_submit_rejected",
				TenantID:     t.tenantID,
				AttemptedAt:  time.Now().UTC(),
				CurrentDepth: depth,
				Limit:        t.quota.MaxLoomTasksQueued,
				ToolName:     string(req.WorkerType),
			})
			return "", ErrLoomQuotaExceeded
		}
	}

	// Inject tenant_id into the request.
	req.TenantID = t.tenantID
	return t.engine.Submit(ctx, req)
}

// Get retrieves a task by ID only if it belongs to this tenant.
// Returns ErrTaskNotFound when the task does not exist OR is owned by a different
// tenant (CHK079: 404, not 403 — no existence disclosure).
func (t *TenantScopedLoomEngine) Get(taskID string) (*Task, error) {
	return t.engine.store.GetForTenant(taskID, t.tenantID)
}

// List returns tasks for a project scoped to this tenant, optionally filtered by status.
func (t *TenantScopedLoomEngine) List(projectID string, statuses ...TaskStatus) ([]*Task, error) {
	return t.engine.store.ListForTenant(projectID, t.tenantID, statuses...)
}

// Cancel requests cancellation of a running task owned by this tenant.
// Returns ErrTaskNotFound when the task does not exist OR is owned by a different
// tenant (CHK079: 404, not 403 — no existence disclosure).
func (t *TenantScopedLoomEngine) Cancel(taskID string) error {
	// Verify tenant ownership via GetForTenant before signalling cancellation.
	// GetForTenant returns ErrTaskNotFound for missing or cross-tenant tasks.
	if _, err := t.engine.store.GetForTenant(taskID, t.tenantID); err != nil {
		return err // already wrapped as ErrTaskNotFound
	}
	return t.engine.Cancel(taskID)
}

// TenantID returns the tenant identifier this engine is scoped to.
func (t *TenantScopedLoomEngine) TenantID() string {
	return t.tenantID
}
