package server

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
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

// swapDelegateToFull atomically replaces the lightweightDelegate with a
// fullDelegate. All projects that connected during Phase A are re-fired through
// OnProjectConnect on the full delegate so their MCP sessions are created.
//
// Steps:
//  1. Create fullDelegate, transfer the notifier from the lightweight stub.
//  2. Store the full delegate atomically.
//  3. Mark the lightweight stub as swapped (defensive panic guard).
//  4. Re-fire OnProjectConnect for every project recorded during Phase A.
//
// Safe to call from any goroutine. Must be called exactly once.
func (s *Server) swapDelegateToFull(h *aimuxHandler) {
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
}
