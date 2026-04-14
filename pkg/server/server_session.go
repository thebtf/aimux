package server

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/thebtf/aimux/pkg/agents"
	"github.com/thebtf/mcp-mux/muxcore"
)

// projectContextKey is the context key for storing ProjectContext.
type projectContextKey struct{}

// projectAgentsKey is the context key for storing per-project agent overlay.
type projectAgentsKey struct{}

// ProjectContextFromContext retrieves the muxcore.ProjectContext from the request context.
// Returns the zero value and false if no ProjectContext is present (e.g., direct stdio mode).
func ProjectContextFromContext(ctx context.Context) (muxcore.ProjectContext, bool) {
	v, ok := ctx.Value(projectContextKey{}).(muxcore.ProjectContext)
	return v, ok
}

// ProjectAgentsFromContext retrieves the per-project agent overlay from the request context.
// Returns nil if no overlay is present (direct stdio mode or no project-specific agents).
func ProjectAgentsFromContext(ctx context.Context) []*agents.Agent {
	v, _ := ctx.Value(projectAgentsKey{}).([]*agents.Agent)
	return v
}

// projectState holds per-project state for a connected CC session group.
// Multiple CC sessions from the same worktree share one projectState (same ID).
// cwd and env are intentionally omitted: HandleRequest receives the current
// ProjectContext on every request, so caching them here would be redundant.
type projectState struct {
	id       string
	session  *mcpserver.InProcessSession
	agents   []*agents.Agent // project-specific agent overlay
	refcount atomic.Int32    // number of CC sessions sharing this project
	ready    chan struct{}    // closed after session registered; HandleRequest waits on this
}

// aimuxHandler implements muxcore.SessionHandler and muxcore.ProjectLifecycle.
// It dispatches MCP JSON-RPC requests to MCPServer.HandleMessage with per-project
// session isolation via InProcessSession.
type aimuxHandler struct {
	srv      *Server
	projects sync.Map // map[string]*projectState keyed by ProjectContext.ID
	notifier muxcore.Notifier
}

// Compile-time interface assertions.
var _ muxcore.SessionHandler = (*aimuxHandler)(nil)
var _ muxcore.ProjectLifecycle = (*aimuxHandler)(nil)
var _ muxcore.NotifierAware = (*aimuxHandler)(nil)

// SetNotifier satisfies muxcore.NotifierAware. Called once by the owner before
// the first HandleRequest. Stored for use in OnProjectConnect broadcasts.
func (h *aimuxHandler) SetNotifier(n muxcore.Notifier) {
	h.notifier = n
}

// HandleRequest processes one MCP JSON-RPC request with project context.
// Called concurrently from multiple goroutines by the muxcore engine owner.
func (h *aimuxHandler) HandleRequest(ctx context.Context, project muxcore.ProjectContext, request []byte) ([]byte, error) {
	// Get or wait for project state.
	val, ok := h.projects.Load(project.ID)
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

	// Inject ProjectContext and project agents into request context for tool handlers.
	ctx = context.WithValue(ctx, projectContextKey{}, project)
	if len(state.agents) > 0 {
		ctx = context.WithValue(ctx, projectAgentsKey{}, state.agents)
	}

	// Inject the project's MCP session into context for HandleMessage.
	ctx = h.srv.mcp.WithContext(ctx, state.session)

	// Dispatch to MCPServer — direct JSON-RPC, no stdio transport.
	var msg json.RawMessage = request
	response := h.srv.mcp.HandleMessage(ctx, msg)

	// nil response = notification or server-initiated request ack — no bytes to return.
	if response == nil {
		return nil, nil
	}

	return json.Marshal(response)
}

// OnProjectConnect is called when a CC session connects to the daemon.
// Creates or increments refcount for the project's state.
func (h *aimuxHandler) OnProjectConnect(project muxcore.ProjectContext) {
	// Create a candidate state for LoadOrStore. If another goroutine already
	// stored a state for this project ID, we discard this candidate and only
	// increment the existing refcount — avoiding a duplicate session registration.
	newState := &projectState{
		id:    project.ID,
		ready: make(chan struct{}),
	}
	newState.refcount.Store(1)

	// Atomically load existing state or store the new candidate.
	val, loaded := h.projects.LoadOrStore(project.ID, newState)
	if loaded {
		// Another session already connected — increment refcount on the winner.
		state := val.(*projectState)
		state.refcount.Add(1)
		h.srv.log.Info("session-handler: project %s reconnected (refcount=%d, cwd=%s)",
			project.ID, state.refcount.Load(), project.Cwd)
		return
	}

	// We won the race — initialize the session for newState.
	state := newState

	// Create InProcessSession for this project.
	state.session = mcpserver.NewInProcessSession(project.ID, nil)

	// Register session with MCPServer (enables per-project tool/resource scoping).
	if err := h.srv.mcp.RegisterSession(context.Background(), state.session); err != nil {
		h.srv.log.Warn("session-handler: failed to register session for project %s: %v", project.ID, err)
	}

	// Discover project-specific agents.
	state.agents = h.srv.agentReg.DiscoverForProject(project.Cwd)

	// Broadcast tools/list_changed so that connected CC sessions re-request
	// tools/list and discover any project-specific agents just found.
	if h.notifier != nil && len(state.agents) > 0 {
		notification := []byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`)
		h.notifier.Broadcast(notification)
	}

	// Signal ready — HandleRequest waiters unblock after this.
	close(state.ready)

	h.srv.log.Info("session-handler: project %s connected (cwd=%s, agents=%d)",
		project.ID, project.Cwd, len(state.agents))
}

// OnProjectDisconnect is called when a CC session disconnects.
// Decrements refcount; cleans up when no sessions remain.
func (h *aimuxHandler) OnProjectDisconnect(projectID string) {
	val, ok := h.projects.Load(projectID)
	if !ok {
		h.srv.log.Warn("session-handler: disconnect for unknown project %s", projectID)
		return
	}
	state := val.(*projectState)

	remaining := state.refcount.Add(-1)
	if remaining > 0 {
		h.srv.log.Info("session-handler: project %s disconnected (refcount=%d)", projectID, remaining)
		return
	}

	// Last session disconnected — clean up.
	h.srv.mcp.UnregisterSession(context.Background(), state.session.SessionID())
	h.projects.Delete(projectID)

	h.srv.log.Info("session-handler: project %s fully disconnected, session unregistered", projectID)
}
