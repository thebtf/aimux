package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/thebtf/mcp-mux/muxcore"
)

// errReadyButShouldRedispatch is returned by lightweightDelegate.HandleRequest when
// the ready channel closes within the grace period. The aimuxHandler dispatches the
// request again through the full delegate after receiving this sentinel.
var errReadyButShouldRedispatch = fmt.Errorf("lightweight delegate: ready, redispatch through full delegate")

// handlerDelegate is the internal dispatch interface implemented by both
// lightweightDelegate (Phase A stub) and fullDelegate (Phase B live handler).
// aimuxHandler holds an atomic.Pointer[handlerDelegate] and swaps from lightweight
// to full when Phase B completes.
//
// The interface mirrors all muxcore-callable methods of aimuxHandler so the swap
// is invisible to the muxcore engine.
type handlerDelegate interface {
	OnProjectConnect(project muxcore.ProjectContext)
	OnProjectDisconnect(projectID string)
	HandleRequest(ctx context.Context, project muxcore.ProjectContext, request []byte) ([]byte, error)
	SetNotifier(n muxcore.Notifier)
}

// lightweightDelegate is the Phase A stub. It:
//   - tracks which projects have connected (so OnProjectDisconnect can be symmetric),
//   - blocks HandleRequest callers on the ready channel,
//   - returns a JSON-RPC error -32001 with retry_after_seconds when the grace period
//     expires before ready closes,
//   - returns errReadyButShouldRedispatch when ready closes within the grace period.
//
// After the swap, lightweightDelegate is no longer reachable; a defensive panic in
// HandleRequest guards against stale references.
type lightweightDelegate struct {
	readyCh  chan struct{}  // bidirectional; closed via closeReady()
	ready    <-chan struct{} // receive-only alias for readyCh; used in select
	graceSec int            // warmup_grace_seconds from config

	mu       sync.Mutex
	projects map[string]struct{} // projects that connected during Phase A

	notifier muxcore.Notifier // stored for potential broadcast on swap

	// swapped is set atomically to 1 when the delegate has been replaced.
	// HandleRequest checks this flag after the ready channel closes to guard
	// against being called post-swap.
	swapped atomic.Int32
}

// newLightweightDelegate constructs a Phase A stub.
// Caller closes the stub via closeReady() when Phase B completes.
// graceSec is the maximum seconds to block callers in HandleRequest.
func newLightweightDelegate(graceSec int) *lightweightDelegate {
	ch := make(chan struct{})
	return &lightweightDelegate{
		readyCh:  ch,
		ready:    ch,
		graceSec: graceSec,
		projects: make(map[string]struct{}),
	}
}

// closeReady signals Phase B completion. HandleRequest callers unblock and
// return errReadyButShouldRedispatch so aimuxHandler re-dispatches through
// the full delegate. Safe to call exactly once; subsequent calls panic (Go channel).
func (d *lightweightDelegate) closeReady() {
	close(d.readyCh)
}

// OnProjectConnect records the project ID and logs the attach.
// No MCP session is created — that happens in fullDelegate.OnProjectConnect after
// Phase B completes.
func (d *lightweightDelegate) OnProjectConnect(project muxcore.ProjectContext) {
	d.mu.Lock()
	d.projects[project.ID] = struct{}{}
	d.mu.Unlock()
}

// OnProjectDisconnect removes the project ID from the in-memory map.
func (d *lightweightDelegate) OnProjectDisconnect(projectID string) {
	d.mu.Lock()
	delete(d.projects, projectID)
	d.mu.Unlock()
}

// HandleRequest blocks until the ready channel closes or the grace period expires.
//   - If the delegate was already swapped: panics (defensive; must not be reachable post-swap).
//   - If ready closes within the grace period: returns errReadyButShouldRedispatch so
//     aimuxHandler re-dispatches through the full delegate.
//   - If the grace period expires before ready closes: returns a JSON-RPC error -32001.
//   - If ctx is cancelled before either: returns ctx.Err().
func (d *lightweightDelegate) HandleRequest(ctx context.Context, project muxcore.ProjectContext, request []byte) ([]byte, error) {
	if d.swapped.Load() != 0 {
		// Should never be reached — aimuxHandler only calls the current delegate.
		panic("lightweightDelegate.HandleRequest called after delegate swap")
	}

	grace := d.graceSec
	if grace <= 0 {
		grace = 15
	}
	deadline := time.Duration(grace) * time.Second

	select {
	case <-d.ready:
		// Full delegate is live. Signal aimuxHandler to re-dispatch.
		return nil, errReadyButShouldRedispatch

	case <-time.After(deadline):
		// Grace period elapsed — return a retriable JSON-RPC error.
		return buildRetryHintResponse(request, grace), nil

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SetNotifier stores the notifier so the swap broadcast can use it if needed.
func (d *lightweightDelegate) SetNotifier(n muxcore.Notifier) {
	d.notifier = n
}

// markSwapped sets the swapped flag so stale HandleRequest calls panic defensively.
func (d *lightweightDelegate) markSwapped() {
	d.swapped.Store(1)
}

// connectedProjects returns a snapshot of project IDs that attached during Phase A.
// Used by aimuxHandler.swapDelegateToFull to re-fire OnProjectConnect on the full delegate.
func (d *lightweightDelegate) connectedProjects() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]string, 0, len(d.projects))
	for id := range d.projects {
		ids = append(ids, id)
	}
	return ids
}

// buildRetryHintResponse constructs a JSON-RPC error response for the given raw request.
// Error code -32001 signals "daemon initialising — retry after N seconds" (FR-5).
// If the request ID cannot be parsed, id=0 is used.
func buildRetryHintResponse(request []byte, retrySec int) []byte {
	// Extract the request id for the response envelope.
	var envelope struct {
		ID any `json:"id"`
	}
	_ = json.Unmarshal(request, &envelope)

	id := envelope.ID

	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32001,
			"message": "daemon initialising — retry after server is ready",
			"data": map[string]any{
				"retry_after_seconds": retrySec,
			},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		// Fallback to minimal hard-coded response — should never happen.
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32001,"message":"daemon initialising"}}`)
	}
	return data
}

// retryHintErrorCode is the JSON-RPC error code emitted during Phase A when
// a HandleRequest call exhausts the grace period.
const retryHintErrorCode = -32001

// isRetryHintResponse reports whether raw is a JSON-RPC response carrying
// the -32001 retry-hint error code. Used in tests to verify Phase A behaviour.
func isRetryHintResponse(raw []byte) bool {
	var resp struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return false
	}
	return resp.Error != nil && resp.Error.Code == retryHintErrorCode
}

// mcpJSONRPCErrorResponse builds a minimal JSON-RPC error response for internal use.
// internalErr wraps server-side errors that are not retry-hint related.
func mcpJSONRPCErrorResponse(id any, code int, message string) []byte {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	data, _ := json.Marshal(resp)
	return data
}

// mcp internal error code (matches mcp.INTERNAL_ERROR from mcp-go).
const mcpInternalError = mcp.INTERNAL_ERROR
