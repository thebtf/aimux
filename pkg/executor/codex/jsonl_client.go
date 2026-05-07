package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

const (
	// notificationBufSize is the buffered channel capacity for server notifications.
	// On overflow the notification is dropped silently — the read loop must not block.
	notificationBufSize = 64

	// maxScanTokenSize caps the JSONL scanner buffer. Codex responses can be large
	// when they include file diffs or long agent messages.
	maxScanTokenSize = 4 * 1024 * 1024 // 4 MB
)

// pendingCall holds the response channel for an in-flight RPC call.
type pendingCall struct {
	ch chan JSONRPCResponse
}

// JSONLClient is a JSON-RPC 2.0 transport over stdio JSONL framing.
//
// Architecture.md §3 names it the sole extraction candidate — the pattern is
// reusable for any future app-server-style CLI that speaks JSON-RPC over stdio.
//
// Protocol:
//   - Outbound: one JSON object per line written to stdin.
//   - Inbound: one JSON object per line read from stdout via bufio.Scanner.
//   - Responses are correlated by numeric id via sync.Map.
//   - Notifications are dispatched to the Notifications channel (buffered, drop-on-full).
//   - Server-initiated requests (id present + method present) are auto-rejected with -32601.
type JSONLClient struct {
	stdin         io.WriteCloser
	stdout        io.Reader
	notifications chan json.RawMessage
	pending       sync.Map      // map[int64]*pendingCall
	nextID        atomic.Int64
	writeMu       sync.Mutex // serialize concurrent writes to stdin
	closeOnce     sync.Once
	done          chan struct{}
}

// NewJSONLClient constructs a client that writes to stdin and reads from stdout.
// Call Start to begin the read loop.
func NewJSONLClient(stdin io.WriteCloser, stdout io.Reader) *JSONLClient {
	return &JSONLClient{
		stdin:         stdin,
		stdout:        stdout,
		notifications: make(chan json.RawMessage, notificationBufSize),
		done:          make(chan struct{}),
	}
}

// Notifications returns the channel on which server notifications are delivered.
// Consumers MUST read from this channel promptly to avoid silent drops.
// The channel is closed when the read loop exits.
func (c *JSONLClient) Notifications() <-chan json.RawMessage {
	return c.notifications
}

// Start begins the read loop goroutine. It returns when ctx is cancelled or
// stdout is closed (EOF). Callers should call Start in a goroutine and wait
// on done via Context cancellation.
func (c *JSONLClient) Start(ctx context.Context) {
	defer c.close(fmt.Errorf("jsonl_client: read loop exited"))
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, maxScanTokenSize), maxScanTokenSize)

	for {
		// Check context cancellation before each scan to enable prompt shutdown.
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !scanner.Scan() {
			// EOF or scanner error — stdin was closed, process exited.
			return
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg inboundMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Unparseable line — skip silently. Codex may emit non-JSON lines
			// (e.g., startup log lines before the first JSONL message).
			continue
		}

		switch {
		case msg.isResponse():
			c.dispatchResponse(msg)
		case msg.isNotification():
			c.dispatchNotification(line)
		case msg.isServerRequest():
			c.rejectServerRequest(ctx, msg)
		}
	}
}

// dispatchResponse routes a response to the waiting Call goroutine.
func (c *JSONLClient) dispatchResponse(msg inboundMessage) {
	id := *msg.ID
	v, ok := c.pending.LoadAndDelete(id)
	if !ok {
		// Stale response for a call that timed out — discard.
		return
	}
	pc := v.(*pendingCall)
	resp := JSONRPCResponse{
		JSONRPC: msg.JSONRPC,
		ID:      id,
		Result:  msg.Result,
		Error:   msg.Error,
	}
	// Non-blocking send: if Call already timed out and closed its context,
	// nobody is listening. The channel has capacity 1.
	select {
	case pc.ch <- resp:
	default:
	}
}

// dispatchNotification sends a raw notification line to the notifications channel.
// Drops silently on overflow — the read loop must not block.
func (c *JSONLClient) dispatchNotification(line []byte) {
	// Make a copy: scanner reuses its internal buffer.
	buf := make([]byte, len(line))
	copy(buf, line)
	select {
	case c.notifications <- buf:
	default:
		// Overflow: drop. High-volume delta notifications hit this path most.
	}
}

// rejectServerRequest sends a -32601 MethodNotFound response.
// The codex app-server may send sampling/createMessage requests; we reject them.
func (c *JSONLClient) rejectServerRequest(ctx context.Context, msg inboundMessage) {
	errResp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      *msg.ID,
		Error: &JSONRPCError{
			Code:    -32601,
			Message: "method not found: server-initiated requests are not supported by this client",
		},
	}
	b, err := json.Marshal(errResp)
	if err != nil {
		return
	}
	_ = c.writeLine(ctx, b)
}

// Call makes a JSON-RPC request and waits for the response.
// params must be JSON-serializable; result is JSON-deserialized from the response.
// Returns an error if ctx is cancelled, the connection closes, or an RPC error is received.
func (c *JSONLClient) Call(ctx context.Context, method string, params, result any) error {
	id := c.nextID.Add(1)

	var rawParams json.RawMessage
	if params != nil {
		var err error
		rawParams, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("jsonl_client: marshal params for %s: %w", method, err)
		}
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("jsonl_client: marshal request %s: %w", method, err)
	}

	// Register pending channel before writing — avoids a race where the response
	// arrives before we register.
	pc := &pendingCall{ch: make(chan JSONRPCResponse, 1)}
	c.pending.Store(id, pc)

	if err := c.writeLine(ctx, b); err != nil {
		c.pending.Delete(id)
		return fmt.Errorf("jsonl_client: write %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.pending.Delete(id)
		return fmt.Errorf("jsonl_client: %s: %w", method, ctx.Err())
	case <-c.done:
		c.pending.Delete(id)
		return fmt.Errorf("jsonl_client: %s: connection closed", method)
	case resp := <-pc.ch:
		if resp.Error != nil {
			return &JSONRPCError{
				Code:    resp.Error.Code,
				Message: resp.Error.Message,
				Data:    resp.Error.Data,
			}
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("jsonl_client: unmarshal result for %s: %w", method, err)
			}
		}
		return nil
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (c *JSONLClient) Notify(ctx context.Context, method string, params any) error {
	n := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	b, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("jsonl_client: marshal notification %s: %w", method, err)
	}
	return c.writeLine(ctx, b)
}

// writeLine serializes a JSON line to stdin with newline termination.
// It checks ctx before acquiring the lock to abort early on cancellation.
func (c *JSONLClient) writeLine(ctx context.Context, b []byte) error {
	// Check context before attempting a potentially blocking write.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// Close shuts down the client gracefully. Idempotent.
func (c *JSONLClient) Close() {
	c.close(fmt.Errorf("jsonl_client: closed"))
}

// close drains all pending calls with the given error and closes the done channel.
func (c *JSONLClient) close(cause error) {
	c.closeOnce.Do(func() {
		close(c.done)
		// Drain all pending calls with the cause error — unblocks any waiting Call.
		c.pending.Range(func(key, value any) bool {
			c.pending.Delete(key)
			pc := value.(*pendingCall)
			select {
			case pc.ch <- JSONRPCResponse{
				Error: &JSONRPCError{
					Code:    -32000,
					Message: cause.Error(),
				},
			}:
			default:
			}
			return true
		})
		close(c.notifications)
	})
}
