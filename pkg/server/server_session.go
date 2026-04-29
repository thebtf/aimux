package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/logger"
	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/mcp-mux/muxcore"
)

// toolsListChangedNotification is the MCP JSON-RPC notification payload that
// instructs connected CC sessions to re-request tools/list. Single source of
// truth — used by both the new-state and reconnect paths in OnProjectConnect.
const toolsListChangedNotification = `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`

// projectContextKey is the context key for storing ProjectContext.
type projectContextKey struct{}

// callToolRequestKey stores the active CallToolRequest for direct-stdio child upstreams.
type callToolRequestKey struct{}

// tenantScopedLoomKey is the context key for TenantScopedLoomEngine injected at dispatch
// (AIMUX-12 Phase 5, T033). Tool handlers retrieve it via TenantScopedLoomFromContext.
type tenantScopedLoomKey struct{}

// ProjectContextFromContext retrieves the muxcore.ProjectContext from the request context.
// Falls back to muxcore-injected _meta fields in direct-stdio child-upstream mode.
func ProjectContextFromContext(ctx context.Context) (muxcore.ProjectContext, bool) {
	if v, ok := ctx.Value(projectContextKey{}).(muxcore.ProjectContext); ok {
		return v, true
	}
	if req, ok := ctx.Value(callToolRequestKey{}).(mcp.CallToolRequest); ok {
		if req.Params.Meta != nil {
			cwd, _ := req.Params.Meta.AdditionalFields["muxCwd"].(string)
			if cwd != "" {
				pc := muxcore.ProjectContext{
					ID:  muxcore.ProjectContextID(cwd),
					Cwd: cwd,
				}
				if rawEnv, ok := req.Params.Meta.AdditionalFields["muxEnv"].(map[string]any); ok {
					env := make(map[string]string, len(rawEnv))
					for k, v := range rawEnv {
						if str, ok := v.(string); ok {
							env[k] = str
						}
					}
					pc.Env = env
				}
				return pc, true
			}
		}
	}
	return muxcore.ProjectContext{}, false
}

// TenantScopedLoomFromContext retrieves the *loom.TenantScopedLoomEngine injected by
// HandleRequest. Returns (engine, true) when the scoped engine is present, (nil, false)
// when absent (e.g. unit tests that don't go through the full dispatch path).
//
// Tool handlers that need tenant-isolated loom access MUST use this instead of the
// raw s.loom field to ensure cross-tenant 404 semantics (CHK079, AIMUX-12 T033).
func TenantScopedLoomFromContext(ctx context.Context) (*loom.TenantScopedLoomEngine, bool) {
	v, ok := ctx.Value(tenantScopedLoomKey{}).(*loom.TenantScopedLoomEngine)
	return v, ok
}

// cwdFromRequestOrContext returns the working directory from either the MCP
// request parameter or the ProjectContext fallback. Returns empty string if
// neither provides a value (direct stdio mode without cwd param).
func cwdFromRequestOrContext(request mcp.CallToolRequest, ctx context.Context) string {
	if cwd := request.GetString("cwd", ""); cwd != "" {
		return cwd
	}
	if pc, ok := ProjectContextFromContext(ctx); ok && pc.Cwd != "" {
		return pc.Cwd
	}
	return ""
}

// projectState holds per-project state for a connected CC session group.
// Multiple CC sessions from the same worktree share one projectState (same ID).
// cwd and env are intentionally omitted: HandleRequest receives the current
// ProjectContext on every request, so caching them here would be redundant.
type projectState struct {
	id       string
	session  *mcpserver.InProcessSession
	refcount atomic.Int32 // number of CC sessions sharing this project
	ready    chan struct{} // closed after session registered; HandleRequest waits on this
}

// fullDelegate is the Phase B live handler. It owns per-project MCP sessions,
// handles JSON-RPC dispatch, and manages project connect/disconnect lifecycle.
// After the Phase A → Phase B delegate swap, aimuxHandler forwards all muxcore
// calls to the fullDelegate loaded from its atomic.Pointer[handlerDelegate].
type fullDelegate struct {
	srv      *Server
	projects sync.Map // map[string]*projectState keyed by ProjectContext.ID

	mu       sync.Mutex
	notifier muxcore.Notifier
}

// SetNotifier stores the notifier for broadcast use.
func (d *fullDelegate) SetNotifier(n muxcore.Notifier) {
	d.mu.Lock()
	d.notifier = n
	d.mu.Unlock()
}

// broadcastToolsListChanged sends a tools/list_changed notification to all
// connected CC sessions. No-op when no Notifier is configured (direct stdio mode).
func (d *fullDelegate) broadcastToolsListChanged() {
	d.mu.Lock()
	n := d.notifier
	d.mu.Unlock()
	if n != nil {
		n.Broadcast([]byte(toolsListChangedNotification))
	}
}

// HandleRequest processes one MCP JSON-RPC request with project context.
// Called concurrently from multiple goroutines by the muxcore engine owner.
func (d *fullDelegate) HandleRequest(ctx context.Context, project muxcore.ProjectContext, request []byte) ([]byte, error) {
	// Get or wait for project state.
	val, ok := d.projects.Load(project.ID)
	if !ok {
		// Project not yet connected — should not happen in normal flow,
		// but handle gracefully by returning a JSON-RPC error.
		errResp := mcp.NewJSONRPCError(mcp.NewRequestId(0), mcp.INTERNAL_ERROR, "project not connected: "+project.ID, nil)
		return json.Marshal(errResp)
	}
	state := val.(*projectState)

	// Wait for session registration to complete (OnProjectConnect may still be running).
	select {
	case <-state.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Inject ProjectContext into request context for tool handlers.
	ctx = context.WithValue(ctx, projectContextKey{}, project)

	// Resolve and inject TenantContext (AIMUX-12 Phase 5, T031).
	// peerUID=0 is the placeholder until muxcore #110 provides SO_PEERCRED/peer auth.
	// In legacy-default mode (no tenants.yaml) this is always LegacyDefault — no cost.
	//
	// In multi-tenant mode, the legitimate path is HandleRequestWithSessionMeta
	// which receives meta.TenantID pre-resolved by AuthorizeSession at session-accept.
	// Reaching this code path against an enrolled registry means AuthorizeSession was
	// either bypassed or absent; ErrTenantUnenrolled rejects the request to prevent
	// privilege escalation via the LegacyDefault operator-role fallback (PRC v3 B1).
	if d.srv.dispatchMW != nil {
		tc, tcErr := d.srv.dispatchMW.ResolveContext(project.ID, 0)
		if tcErr != nil {
			if errors.Is(tcErr, ErrTenantUnenrolled) {
				d.srv.dispatchMW.EmitUnenrolledBlocked(0, project.ID, "")
				errResp := mcp.NewJSONRPCError(mcp.NewRequestId(0), -32000,
					"tenant unenrolled: connecting UID is not registered in the multi-tenant registry",
					nil)
				return json.Marshal(errResp)
			}
			// Unknown error class — log and fall through to dispatch without
			// tenant context. Production should never hit this branch.
			d.srv.log.Warn("dispatch: ResolveContext returned unexpected error: %v", tcErr)
		} else {
			ctx = d.srv.dispatchMW.WithContext(ctx, tc)

			// T032: inject TenantContext into session.WithTenant so TenantScopedStore
			// methods work without an explicit ctx stamp at each call site.
			ctx = session.WithTenant(ctx, tc)

			// T033: inject TenantScopedLoomEngine so tool handlers can access loom
			// with per-tenant isolation (CHK076/CHK079) via TenantScopedLoomFromContext.
			// nil loom (memory-only mode, no SQLite) → no scoped engine injected.
			if d.srv.loom != nil {
				ctx = context.WithValue(ctx, tenantScopedLoomKey{},
					loom.NewTenantScopedEngine(d.srv.loom, tc.TenantID, nil))
			}

			// T034: emit allow audit event for every multi-tenant dispatch.
			// toolName is empty at this layer (pre-routing); Phase 8 / tool-level
			// wrappers will carry the resolved tool name. Legacy-default mode
			// (IsMultiTenant() == false) skips audit emission to avoid log noise
			// on single-tenant deployments — fail-open preserves NFR-4.
			if d.srv.dispatchMW.IsMultiTenant() {
				d.srv.dispatchMW.EmitAllow(tc, "")
			}
		}
	}

	// Inject the project's MCP session into context for HandleMessage.
	ctx = d.srv.mcp.WithContext(ctx, state.session)

	// Dispatch to MCPServer — direct JSON-RPC, no stdio transport.
	var msg json.RawMessage = request
	response := d.srv.mcp.HandleMessage(ctx, msg)

	// nil response = notification or server-initiated request ack — no bytes to return.
	if response == nil {
		return nil, nil
	}

	return json.Marshal(response)
}

// OnProjectConnect is called when a CC session connects to the daemon.
// Creates or increments refcount for the project's state.
func (d *fullDelegate) OnProjectConnect(project muxcore.ProjectContext) {
	// Create a candidate state for LoadOrStore. If another goroutine already
	// stored a state for this project ID, we discard this candidate and only
	// increment the existing refcount — avoiding a duplicate session registration.
	newState := &projectState{
		id:    project.ID,
		ready: make(chan struct{}),
	}
	newState.refcount.Store(1)

	// Atomically load existing state or store the new candidate.
	val, loaded := d.projects.LoadOrStore(project.ID, newState)
	if loaded {
		// Another session already connected — increment refcount on the winner.
		state := val.(*projectState)
		state.refcount.Add(1)
		d.srv.log.Info("session-handler: project %s reconnected (refcount=%d, cwd=%s)",
			project.ID, state.refcount.Load(), project.Cwd)
		d.broadcastToolsListChanged()
		return
	}

	// We won the race — initialize the session for newState.
	state := newState

	// Create InProcessSession for this project.
	state.session = mcpserver.NewInProcessSession(project.ID, nil)

	// Register session with MCPServer (enables per-project tool/resource scoping).
	if err := d.srv.mcp.RegisterSession(context.Background(), state.session); err != nil {
		d.srv.log.Warn("session-handler: failed to register session for project %s: %v", project.ID, err)
	}

	// Broadcast tools/list_changed so connected CC sessions refresh against the
	// per-project MCP session state.
	d.broadcastToolsListChanged()

	// Signal ready — HandleRequest waiters unblock after this.
	close(state.ready)

	d.srv.log.Info("session-handler: project %s connected (cwd=%s)",
		project.ID, project.Cwd)
}

// disconnectProject decrements the refcount for the given project and cleans up
// when no sessions remain. Returns true when the last session disconnected and
// cleanup was performed. Called by aimuxHandler.OnProjectDisconnect which needs
// the bool to decide whether to trigger deferred restart.
func (d *fullDelegate) disconnectProject(projectID string) bool {
	val, ok := d.projects.Load(projectID)
	if !ok {
		d.srv.log.Warn("session-handler: disconnect for unknown project %s", projectID)
		return false
	}
	state := val.(*projectState)

	remaining := state.refcount.Add(-1)
	if remaining > 0 {
		d.srv.log.Info("session-handler: project %s disconnected (refcount=%d)", projectID, remaining)
		return false // not last
	}

	// Last session disconnected — clean up.
	d.srv.mcp.UnregisterSession(context.Background(), state.session.SessionID())
	d.projects.Delete(projectID)

	d.srv.log.Info("session-handler: project %s fully disconnected, session unregistered", projectID)
	return true // last session cleaned up
}

// OnProjectDisconnect satisfies the handlerDelegate interface.
// Delegates to disconnectProject; the bool result is not exposed here.
func (d *fullDelegate) OnProjectDisconnect(projectID string) {
	d.disconnectProject(projectID)
}

// anyActiveProjects returns true if at least one project remains in the map.
func (d *fullDelegate) anyActiveProjects() bool {
	found := false
	d.projects.Range(func(_, _ any) bool {
		found = true
		return false // stop after first
	})
	return found
}

// aimuxHandler implements muxcore.SessionHandler and muxcore.ProjectLifecycle.
// During Phase A it holds a lightweightDelegate; after Phase B completes, the
// atomic pointer is swapped to a fullDelegate. All muxcore-facing method calls
// are forwarded to whichever delegate is current.
type aimuxHandler struct {
	srv      *Server
	delegate atomic.Pointer[handlerDelegate] // swapped from lightweight → full on Phase B

	updatePending atomic.Bool // set after successful binary update; daemon exits on last disconnect
	cancelFunc    func()      // cancels the engine context to stop daemon
}

// Compile-time interface assertions.
var _ muxcore.SessionHandler = (*aimuxHandler)(nil)
var _ muxcore.ProjectLifecycle = (*aimuxHandler)(nil)
var _ muxcore.NotifierAware = (*aimuxHandler)(nil)
var _ muxcore.NotificationHandler = (*aimuxHandler)(nil)
var _ muxcore.NotificationHandlerWithSessionMeta = (*aimuxHandler)(nil)
var _ muxcore.SessionHandlerWithSessionMeta = (*aimuxHandler)(nil)

// currentDelegate returns the active delegate. Panics if unset (misconfiguration).
func (h *aimuxHandler) currentDelegate() handlerDelegate {
	d := h.delegate.Load()
	if d == nil {
		panic("aimuxHandler: delegate not initialized")
	}
	return *d
}

// SetNotifier satisfies muxcore.NotifierAware. Called once by the muxcore engine
// before the first HandleRequest. Forwarded to the current delegate so the
// lightweightDelegate can store it for the swap broadcast, and the fullDelegate
// receives it directly when it becomes active.
func (h *aimuxHandler) SetNotifier(n muxcore.Notifier) {
	h.currentDelegate().SetNotifier(n)
}

// HandleRequest dispatches the request to the current delegate.
//
// During Phase A the lightweightDelegate blocks until the ready channel closes
// (within the grace period) or returns a -32001 retry-hint error. When it
// returns errReadyButShouldRedispatch, HandleRequest re-loads the delegate (now
// fullDelegate) and dispatches again — one extra atomic load per first-time call
// during the transition, negligible overhead thereafter.
func (h *aimuxHandler) HandleRequest(ctx context.Context, project muxcore.ProjectContext, request []byte) ([]byte, error) {
	for {
		resp, err := h.currentDelegate().HandleRequest(ctx, project, request)
		if err == errReadyButShouldRedispatch {
			// Phase B swap completed; re-dispatch through the full delegate.
			continue
		}
		return resp, err
	}
}

// HandleRequestWithSessionMeta implements muxcore.SessionHandlerWithSessionMeta.
// When muxcore has already resolved peer identity via AuthorizeSession, it calls
// this method instead of HandleRequest, providing the pre-resolved SessionMeta.
//
// The key difference from HandleRequest: meta.TenantID is already authoritative —
// it was set by AuthorizeSessionAdapter.Authorize at session-accept time. We
// construct the TenantContext directly from meta rather than falling back to the
// peerUID=0 placeholder path in dispatchMW.ResolveContext.
//
// If meta.TenantID is empty (legacy mode or no AuthorizeSession callback configured),
// we fall through to HandleRequest which uses ResolveContext with the peerUID=0
// fallback — preserving backward compatibility.
func (h *aimuxHandler) HandleRequestWithSessionMeta(
	ctx context.Context,
	project muxcore.ProjectContext,
	meta muxcore.SessionMeta,
	req []byte,
) ([]byte, error) {
	// Phase 8 hot path: TenantID was resolved at session-accept time by AuthorizeSession.
	// Construct TenantContext directly from meta — no registry lookup needed.
	if meta.TenantID != "" && h.srv.dispatchMW != nil {
		tc := tenant.TenantContext{
			TenantID:         meta.TenantID,
			PeerUid:          meta.Conn.PeerUid,
			SessionID:        project.ID,
			RequestStartedAt: time.Now(),
		}
		ctx = h.srv.dispatchMW.WithContext(ctx, tc)
		ctx = session.WithTenant(ctx, tc)
		if h.srv.loom != nil {
			ctx = context.WithValue(ctx, tenantScopedLoomKey{},
				loom.NewTenantScopedEngine(h.srv.loom, tc.TenantID, nil))
		}
		// Emit allow audit event for multi-tenant dispatch (mirrors T034 in HandleRequest).
		if h.srv.dispatchMW.IsMultiTenant() {
			h.srv.dispatchMW.EmitAllow(tc, "")
		}
		// Delegate to current (full) delegate with the pre-populated context.
		// The redispatch loop handles the Phase A → Phase B transition edge case.
		for {
			resp, err := h.currentDelegate().HandleRequest(ctx, project, req)
			if err == errReadyButShouldRedispatch {
				continue
			}
			return resp, err
		}
	}

	// Fallback: legacy mode (empty TenantID) → standard HandleRequest path.
	return h.HandleRequest(ctx, project, req)
}

// OnProjectConnect forwards to the current delegate.
func (h *aimuxHandler) OnProjectConnect(project muxcore.ProjectContext) {
	h.currentDelegate().OnProjectConnect(project)
}

// OnProjectDisconnect forwards to the current delegate and performs the
// deferred-restart check when the last session disconnects.
func (h *aimuxHandler) OnProjectDisconnect(projectID string) {
	d := h.currentDelegate()

	// fullDelegate.OnProjectDisconnect returns true when the last session cleaned up.
	// lightweightDelegate.OnProjectDisconnect always returns.
	var lastSessionGone bool
	if fd, ok := d.(*fullDelegate); ok {
		lastSessionGone = fd.disconnectProject(projectID)
		if lastSessionGone && h.updatePending.Load() {
			if !fd.anyActiveProjects() && h.cancelFunc != nil {
				h.srv.log.Info("session-handler: update pending, no active sessions — stopping daemon for restart")
				h.cancelFunc()
			}
		}
	} else {
		d.OnProjectDisconnect(projectID)
	}
}

// SetUpdatePending marks an update as pending. The daemon will exit when all
// CC sessions disconnect (refcount=0 for all projects), allowing the next
// shim invocation to start the updated binary.
func (h *aimuxHandler) SetUpdatePending() {
	h.updatePending.Store(true)
	h.srv.log.Info("session-handler: update pending — daemon will exit when all sessions disconnect")
}

// SetCancelFunc stores the context cancel function used to stop the engine.
// Called during server initialization with the engine's context cancel.
func (h *aimuxHandler) SetCancelFunc(cancel func()) {
	h.cancelFunc = cancel
}

// logForwardNotification is the wire format for the "notifications/aimux/log_forward"
// JSON-RPC notification dispatched by shim processes (AIMUX-11 Phase 3).
type logForwardNotification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// HandleNotification implements muxcore.NotificationHandler.
// muxcore owner dispatches this in a goroutine for every JSON-RPC message that
// has no "id" field (i.e. notifications). We intercept "notifications/aimux/log_forward"
// and route it to the LogIngester; all other notification methods are ignored.
//
// Peer PID cannot be obtained here — the notification path receives a ProjectContext,
// not a net.Conn. Per FR-12 the pid field is set to "?{project.ID[:8]}" and
// PeerCredsUnavailable is incremented on the ingester.
func (h *aimuxHandler) HandleNotification(ctx context.Context, project muxcore.ProjectContext, notification []byte) {
	const method = "notifications/aimux/log_forward"

	// Fast parse: only extract method field to decide routing.
	var n logForwardNotification
	if err := json.Unmarshal(notification, &n); err != nil {
		// Unparseable JSON — not our concern; muxcore should not forward garbage.
		return
	}

	if n.Method != method {
		// Not a log-forward notification; ignore (other MCP notification types handled elsewhere).
		return
	}

	ingester := h.srv.logIngester
	if ingester == nil {
		// Ingester not wired (shim mode or test without sink) — drop silently.
		return
	}

	// Decode LogEntry from params.
	var entry logger.LogEntry
	if err := json.Unmarshal(n.Params, &entry); err != nil {
		ingester.EnvelopeMalformed.Add(1)
		return
	}

	// Derive session tag: CLAUDE_SESSION_ID (first 8 chars) → project.ID (first 8 chars) → "anon".
	sess := sessionTagFromProject(project)

	// Peer PID is unavailable in the notification path (no net.Conn accessible).
	// Use a "?<id[:8]>" marker per FR-12 and count the event for observability (NFR-8).
	ingester.PeerCredsUnavailable.Add(1)
	pidMarker := "?" + idPrefix(string(project.ID))

	// CR-002 T006: log malformed-envelope errors so operator sees the cause,
	// not just a counter increment. ReceiveNotification returns an error only
	// for oversized message payloads (FR-9 log_max_line_bytes); other reject
	// paths (decode error, EnvelopeMalformed) are handled above and counted
	// without per-event noise.
	if err := ingester.ReceiveNotification(entry, pidMarker, sess); err != nil {
		if h.srv != nil && h.srv.log != nil {
			h.srv.log.Warn("log_forward: rejected entry from project=%s sess=%s: %v",
				idPrefix(string(project.ID)), sess, err)
		}
	}
}

// HandleNotificationWithSessionMeta implements muxcore.NotificationHandlerWithSessionMeta.
// When muxcore has peer identity available at session-open time, it calls this method
// instead of HandleNotification, providing the SessionMeta (TenantID, ConnInfo).
//
// Routing logic (AIMUX-12 Phase 6, T039):
//   - If meta.TenantID is non-empty AND srv.logPartitioner is wired, route the
//     log_forward entry bytes to LogPartitioner.WriteFor(meta.TenantID).
//   - Otherwise fall through to HandleNotification (legacy/fallback path) which
//     writes to the shared daemon log file.
//
// This preserves FR-12 backward compat: existing sessions without TenantID continue
// to use the PeerCredsUnavailable path in HandleNotification unchanged.
func (h *aimuxHandler) HandleNotificationWithSessionMeta(
	ctx context.Context,
	project muxcore.ProjectContext,
	meta muxcore.SessionMeta,
	notification []byte,
) {
	const method = "notifications/aimux/log_forward"

	// Fast path: only handle log_forward notifications.
	var n logForwardNotification
	if err := json.Unmarshal(notification, &n); err != nil {
		return
	}
	if n.Method != method {
		// Not a log_forward notification — nothing to do in the meta path.
		return
	}

	// If TenantID is present and a partitioner is wired, route to the tenant file.
	if meta.TenantID != "" && h.srv.logPartitioner != nil {
		var entry logger.LogEntry
		if err := json.Unmarshal(n.Params, &entry); err != nil {
			if h.srv.logIngester != nil {
				h.srv.logIngester.EnvelopeMalformed.Add(1)
			}
			return
		}

		sess := sessionTagFromProject(project)
		pidStr := fmt.Sprintf("%d", meta.Conn.PeerPid)

		// Sanitize the message using the package-level sanitizeMessage (FR-13),
		// then format and route to the tenant log file.
		sanitizedMsg := sanitizeMessage(entry.Message)
		line := formatLogLine(entry, sanitizedMsg, "shim", pidStr, sess)
		if _, err := h.srv.logPartitioner.WriteFor(meta.TenantID, []byte(line)); err != nil {
			if h.srv.log != nil {
				h.srv.log.Warn("log_forward(meta): partitioner write for tenant=%s failed: %v",
					meta.TenantID, err)
			}
		}
		return
	}

	// Fallback: delegate to the legacy HandleNotification path.
	// This preserves PeerCredsUnavailable counting and existing test coverage.
	h.HandleNotification(ctx, project, notification)
}

// formatLogLine produces a single formatted log line in the aimux envelope format.
// Used by HandleNotificationWithSessionMeta when routing to LogPartitioner.
// Format matches WriteEntryWithRoleStr output for cross-file grep consistency.
func formatLogLine(entry logger.LogEntry, sanitizedMessage, role, pidStr, sess string) string {
	return fmt.Sprintf("%s [%s] [%s-%s-%s] %s\n",
		entry.Time.Format("2006-01-02T15:04:05.000Z07:00"),
		entry.Level.String(),
		role,
		pidStr,
		sess,
		sanitizedMessage,
	)
}

// sessionTagFromProject derives a short session tag for log lines.
// Priority: CLAUDE_SESSION_ID env var (first 8 chars) → project.ID (first 8 chars) → "anon".
func sessionTagFromProject(project muxcore.ProjectContext) string {
	if sessID, ok := project.Env["CLAUDE_SESSION_ID"]; ok && len(sessID) >= 8 {
		return sessID[:8]
	}
	if len(project.ID) >= 8 {
		return string(project.ID)[:8]
	}
	if len(project.ID) > 0 {
		return string(project.ID)
	}
	return "anon"
}

// idPrefix returns the first 8 characters of a project ID string, or the full
// string if shorter. Used to build the "?<prefix>" peer-creds fallback marker.
func idPrefix(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// swapDelegateToFull atomically replaces the lightweightDelegate with a
// fullDelegate. All projects that connected during Phase A are re-fired through
// OnProjectConnect on the full delegate so their MCP sessions are created.
//
// Steps:
//  1. Create fullDelegate, transfer the notifier from the lightweight stub.
//  2. Store the full delegate atomically.
//  3. Mark the lightweight stub as swapped (defensive panic guard).
//  4. Re-fire OnProjectConnect for every project recorded during Phase A.
//  5. Write initDurationMs and initPhase=2 so health gauges are accurate.
//
// startedAt is the time Phase B work began; used to compute initDurationMs.
// Writing the gauge here (rather than in the caller) ensures any future caller
// of swapDelegateToFull automatically gets correct observability (ADR-001).
//
// Safe to call from any goroutine. Must be called exactly once.
func (s *Server) swapDelegateToFull(h *aimuxHandler, startedAt time.Time) {
	// Capture the lightweight delegate before the swap.
	old := h.delegate.Load()
	if old == nil {
		panic("swapDelegateToFull: no delegate installed")
	}
	lw, ok := (*old).(*lightweightDelegate)
	if !ok {
		// Already swapped — this is a bug.
		panic("swapDelegateToFull: current delegate is not a lightweightDelegate")
	}

	// Build the full delegate, inheriting the notifier from the lightweight stub.
	fd := &fullDelegate{srv: s}
	fd.notifier = lw.notifier

	// Atomically install the full delegate.
	var dFull handlerDelegate = fd
	h.delegate.Store(&dFull)

	// Signal Phase B completion — unblocks any HandleRequest callers waiting on ready.
	// Must happen AFTER the new delegate is stored so re-dispatched requests go to fullDelegate.
	lw.closeReady()

	// Prevent stale references from calling into the old stub.
	lw.markSwapped()

	if s.log != nil {
		s.log.Info("session-handler: Phase B delegate swap complete")
	}

	// Re-fire OnProjectConnect for every project that attached during Phase A.
	// The lightweight stub recorded these without creating MCP sessions.
	for _, id := range lw.connectedProjects() {
		pc := muxcore.ProjectContext{ID: id}
		fd.OnProjectConnect(pc)
	}

	// Write observability gauges. Store initDurationMs before initPhase=2 so
	// any reader that sees initPhase==2 is guaranteed to see a valid duration
	// (ADR-001: co-located with the swap, not in the caller).
	s.initDurationMs.Store(time.Since(startedAt).Milliseconds())
	s.initPhase.Store(2)
}
