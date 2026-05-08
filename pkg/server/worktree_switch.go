package server

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/mcp-mux/muxcore"
)

const (
	worktreeSwitchCanceledMessage = "Canceled: worktree switched mid-task"
	worktreeSessionMetadataKey    = "worktree_session_key"
)

var errProjectNotConnected = errors.New("project not connected")

type sessionMetaContextKey struct{}
type muxSessionIDContextKey struct{}

type worktreeSessionTracker struct {
	mu        sync.Mutex
	projectID string
}

func contextWithSessionMeta(ctx context.Context, meta muxcore.SessionMeta) context.Context {
	return context.WithValue(ctx, sessionMetaContextKey{}, meta)
}

func contextWithMuxSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, muxSessionIDContextKey{}, sessionID)
}

func sessionMetaFromContext(ctx context.Context) (muxcore.SessionMeta, bool) {
	meta, ok := ctx.Value(sessionMetaContextKey{}).(muxcore.SessionMeta)
	return meta, ok
}

func (d *fullDelegate) projectStateForRequest(ctx context.Context, project muxcore.ProjectContext) (*projectState, error) {
	state, err := d.loadProjectState(project.ID)
	if err != nil {
		return nil, err
	}

	key, ok := worktreeSessionKeyFromContext(ctx)
	if !ok {
		return state, nil
	}

	tracker := d.trackerForSession(key)
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	previousProjectID := tracker.projectID
	if previousProjectID == "" {
		tracker.projectID = project.ID
		return state, nil
	}
	if previousProjectID == project.ID {
		return state, nil
	}

	if err := d.handleWorktreeSwitch(ctx, previousProjectID, project.ID); err != nil {
		return nil, err
	}
	tracker.projectID = project.ID
	return state, nil
}

func (d *fullDelegate) loadProjectState(projectID string) (*projectState, error) {
	val, ok := d.projects.Load(projectID)
	if !ok {
		return nil, errProjectNotConnected
	}
	return val.(*projectState), nil
}

func (d *fullDelegate) trackerForSession(key string) *worktreeSessionTracker {
	tracker, _ := d.sessions.LoadOrStore(key, &worktreeSessionTracker{})
	return tracker.(*worktreeSessionTracker)
}

func (d *fullDelegate) lastProjectForSession(key string) string {
	tracker, ok := d.sessions.Load(key)
	if !ok {
		return ""
	}
	t := tracker.(*worktreeSessionTracker)
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.projectID
}

func (d *fullDelegate) forgetProjectSessions(projectID string) {
	d.sessions.Range(func(key, value any) bool {
		tracker := value.(*worktreeSessionTracker)
		tracker.mu.Lock()
		deleteTracker := tracker.projectID == projectID
		tracker.mu.Unlock()
		if deleteTracker {
			d.sessions.Delete(key)
		}
		return true
	})
}

func worktreeSessionKeyFromContext(ctx context.Context) (string, bool) {
	if sessionID, _ := ctx.Value(muxSessionIDContextKey{}).(string); sessionID != "" {
		return "mux:" + sessionID, true
	}
	meta, ok := sessionMetaFromContext(ctx)
	if !ok {
		return "", false
	}
	return worktreeSessionKeyFromMeta(meta)
}

func worktreeSessionKeyFromMeta(meta muxcore.SessionMeta) (string, bool) {
	platform := meta.Conn.Platform
	if platform == "" {
		platform = muxcore.PlatformUnknown
	}
	if meta.Conn.PeerPid != 0 {
		return platform + ":pid:" + strconv.Itoa(meta.Conn.PeerPid), true
	}
	if meta.Conn.PeerUid != 0 {
		return platform + ":uid:" + strconv.Itoa(meta.Conn.PeerUid), true
	}
	if meta.IsAuthorized() {
		return platform + ":authorized:" + meta.AuthorizedAt.UTC().Format(time.RFC3339Nano) + ":tenant:" + meta.TenantID, true
	}
	return "", false
}

func (d *fullDelegate) handleWorktreeSwitch(ctx context.Context, previousProjectID, newProjectID string) error {
	if previousProject, err := d.loadProjectState(previousProjectID); err == nil {
		previousProject.draining.Store(true)
	}

	cfg := d.worktreeConfig()
	if d.srv != nil && d.srv.log != nil {
		d.srv.log.Info("worktree switch detected: previous_project_id=%s new_project_id=%s forced_switch=%t",
			previousProjectID, newProjectID, cfg.ForcedSwitch)
	}

	if cfg.ForcedSwitch {
		return d.forceCancelPreviousWorktree(ctx, previousProjectID, newProjectID)
	}

	drained, err := d.waitForPreviousWorktree(ctx, previousProjectID, time.Duration(cfg.DrainTimeoutSeconds)*time.Second)
	if err != nil {
		return err
	}
	if !drained && d.srv != nil && d.srv.log != nil {
		d.srv.log.Warn("worktree switch drain timeout: previous_project_id=%s new_project_id=%s drain_timeout_seconds=%d",
			previousProjectID, newProjectID, cfg.DrainTimeoutSeconds)
	}
	return nil
}

func (d *fullDelegate) forceCancelPreviousWorktree(ctx context.Context, previousProjectID, newProjectID string) error {
	if d.srv == nil || d.srv.loom == nil {
		return nil
	}
	tenantID := tenantIDFromContext(ctx)
	sessionKey, _ := worktreeSessionKeyFromContext(ctx)
	count, err := d.failActiveByPreviousWorktree(previousProjectID, tenantID, sessionKey)
	if err != nil {
		return err
	}
	if d.srv.log != nil {
		d.srv.log.Info("worktree switch forced cancel: previous_project_id=%s new_project_id=%s tenant_id=%s session_key=%s canceled_tasks=%d",
			previousProjectID, newProjectID, tenantID, sessionKey, count)
	}
	return nil
}

func (d *fullDelegate) failActiveByPreviousWorktree(previousProjectID, tenantID, sessionKey string) (int, error) {
	if sessionKey == "" || tenantID == "" {
		return 0, nil
	}
	tasks, err := loom.NewTenantScopedEngine(d.srv.loom, tenantID, nil).List(previousProjectID, loom.ActiveTaskStatuses()...)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, task := range tasks {
		if worktreeSessionKeyFromTask(task) != sessionKey {
			continue
		}
		ok, err := d.srv.loom.FailActive(task.ID, worktreeSwitchCanceledMessage)
		if err != nil {
			return count, err
		}
		if ok {
			count++
		}
	}
	return count, nil
}

func worktreeSessionKeyFromTask(task *loom.Task) string {
	if task == nil || task.Metadata == nil {
		return ""
	}
	if value, ok := task.Metadata[worktreeSessionMetadataKey].(string); ok {
		return value
	}
	if value, ok := task.Metadata["session_id"].(string); ok {
		return value
	}
	return ""
}

func tenantIDFromContext(ctx context.Context) string {
	if tc, ok := tenant.FromContext(ctx); ok {
		return tc.TenantID
	}
	if meta, ok := sessionMetaFromContext(ctx); ok {
		return meta.TenantID
	}
	return ""
}

func (d *fullDelegate) waitForPreviousWorktree(ctx context.Context, projectID string, timeout time.Duration) (bool, error) {
	if d.srv == nil || d.srv.loom == nil {
		return true, nil
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	tenantID := tenantIDFromContext(ctx)
	sessionKey, _ := worktreeSessionKeyFromContext(ctx)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		drained, err := d.previousWorktreeDrained(projectID, tenantID, sessionKey)
		if err != nil {
			return false, err
		}
		if drained {
			return true, nil
		}

		select {
		case <-timer.C:
			return false, nil
		case <-ticker.C:
		}
	}
}

func (d *fullDelegate) previousWorktreeDrained(projectID, tenantID, sessionKey string) (bool, error) {
	if tenantID == "" {
		return true, nil
	}
	tasks, err := loom.NewTenantScopedEngine(d.srv.loom, tenantID, nil).List(projectID, loom.ActiveTaskStatuses()...)
	if err != nil {
		return false, err
	}
	return worktreeTasksDrainedForSession(tasks, sessionKey), nil
}

func worktreeTasksDrainedForSession(tasks []*loom.Task, sessionKey string) bool {
	if sessionKey == "" {
		return len(tasks) == 0
	}
	for _, task := range tasks {
		if worktreeSessionKeyFromTask(task) == sessionKey {
			return false
		}
	}
	return true
}

func (d *fullDelegate) worktreeConfig() config.WorktreeConfig {
	cfg := config.WorktreeConfig{DrainTimeoutSeconds: 30}
	if d != nil && d.srv != nil && d.srv.cfg != nil {
		cfg = d.srv.cfg.Worktree
	}
	if cfg.DrainTimeoutSeconds <= 0 {
		cfg.DrainTimeoutSeconds = 30
	}
	return cfg
}
