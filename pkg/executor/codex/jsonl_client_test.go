package codex

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// echoServer simulates a minimal codex app-server over an in-process pipe.
type echoServer struct {
	in  *io.PipeWriter // server writes here, client reads
	out *io.PipeReader // server reads here, client writes
}

// pipeClientServer creates a bidirectional pipe pair: (clientStdin, clientStdout).
// The returned echoServer reads from clientStdin and writes to clientStdout.
func pipeClientServer() (*JSONLClient, *echoServer) {
	// Client writes to serverIn; server reads from serverOut.
	serverOut, serverIn := io.Pipe()
	// Server writes to clientOut; client reads from clientIn.
	clientIn, clientOut := io.Pipe()

	client := NewJSONLClient(serverIn, clientIn)
	srv := &echoServer{in: clientOut, out: serverOut}
	return client, srv
}

// respondTo reads one line from the server's stdout and sends a synthetic response.
func (s *echoServer) respondTo(result any) {
	scanner := newScanner(s.out)
	scanner.Scan()
	line := scanner.Bytes()

	var req inboundMessage
	json.Unmarshal(line, &req)

	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      *req.ID,
		"result":  result,
	}
	b, _ := json.Marshal(resp)
	s.in.Write(append(b, '\n'))
}

func newScanner(r io.Reader) *bufioScanner {
	return &bufioScanner{r: r}
}

// bufioScanner is a thin wrapper so we don't import bufio in test.
type bufioScanner struct {
	r   io.Reader
	buf []byte
}

func (s *bufioScanner) Scan() bool {
	s.buf = s.buf[:0]
	tmp := make([]byte, 1)
	for {
		n, err := s.r.Read(tmp)
		if n > 0 {
			if tmp[0] == '\n' {
				return true
			}
			s.buf = append(s.buf, tmp[0])
		}
		if err != nil {
			return false
		}
	}
}

func (s *bufioScanner) Bytes() []byte { return s.buf }

func TestJSONLClient_Call_BasicHappyPath(t *testing.T) {
	client, srv := pipeClientServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go client.Start(ctx)

	// Server goroutine: respond to one call.
	go srv.respondTo(map[string]string{"sessionId": "sess-1"})

	var result InitializeResult
	err := client.Call(ctx, "initialize", InitializeParams{}, &result)
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "sess-1" {
		t.Errorf("expected sessionId=sess-1, got %q", result.SessionID)
	}
}

func TestJSONLClient_Call_RPCError(t *testing.T) {
	client, srv := pipeClientServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go client.Start(ctx)

	go func() {
		scanner := newScanner(srv.out)
		scanner.Scan()
		line := scanner.Bytes()
		var req inboundMessage
		json.Unmarshal(line, &req)
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      *req.ID,
			"error":   map[string]any{"code": -32600, "message": "thread not found"},
		}
		b, _ := json.Marshal(resp)
		srv.in.Write(append(b, '\n'))
	}()

	err := client.Call(ctx, "thread/resume", nil, nil)
	if err == nil {
		t.Fatal("expected RPC error")
	}
	rpcErr, ok := err.(*JSONRPCError)
	if !ok {
		t.Fatalf("expected *JSONRPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != -32600 {
		t.Errorf("expected code -32600, got %d", rpcErr.Code)
	}
}

func TestJSONLClient_Notification_Dispatch(t *testing.T) {
	client, srv := pipeClientServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go client.Start(ctx)

	// Server sends a notification without being called.
	notif := map[string]any{
		"jsonrpc": "2.0",
		"method":  "item/completed",
		"params":  map[string]any{"threadId": "thr-1", "turnId": "turn-1", "item": map[string]any{"type": "agentMessage", "id": "m1", "text": "hello"}},
	}
	b, _ := json.Marshal(notif)
	srv.in.Write(append(b, '\n'))

	select {
	case raw := <-client.Notifications():
		var n JSONRPCNotification
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatal(err)
		}
		if n.Method != "item/completed" {
			t.Errorf("expected item/completed, got %q", n.Method)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestJSONLClient_ServerRequest_AutoReject(t *testing.T) {
	client, srv := pipeClientServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go client.Start(ctx)

	// Server sends a request (not a notification — has id + method).
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "sampling/createMessage",
		"params":  map[string]any{},
	}
	b, _ := json.Marshal(req)
	srv.in.Write(append(b, '\n'))

	// Client should auto-send -32601 back. Read it from server's perspective.
	scanner := newScanner(srv.out)
	done := make(chan struct{})
	var gotCode int
	go func() {
		defer close(done)
		if !scanner.Scan() {
			return
		}
		var resp JSONRPCResponse
		json.Unmarshal(scanner.Bytes(), &resp)
		if resp.Error != nil {
			gotCode = resp.Error.Code
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for auto-reject response")
	}
	if gotCode != -32601 {
		t.Errorf("expected -32601, got %d", gotCode)
	}
}

func TestJSONLClient_ContextCancellation(t *testing.T) {
	client, srv := pipeClientServer()
	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	go func() {
		close(started)
		client.Start(ctx)
	}()
	<-started
	cancel() // Cancel immediately

	// Also close the server pipe so the read loop unblocks on EOF.
	srv.in.Close()

	// Call should fail quickly with context error.
	err := client.Call(ctx, "initialize", nil, nil)
	if err == nil {
		t.Fatal("expected error after cancel")
	}
}

func TestJSONLClient_NotificationOverflow_NoBlock(t *testing.T) {
	client, srv := pipeClientServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go client.Start(ctx)

	// Send 65+ notifications without consuming — should not deadlock.
	notif := map[string]any{
		"jsonrpc": "2.0",
		"method":  "item/agentMessage/delta",
		"params":  map[string]any{},
	}
	b, _ := json.Marshal(notif)
	line := append(b, '\n')

	done := make(chan struct{})
	go func() {
		for i := 0; i < 80; i++ {
			srv.in.Write(line)
		}
		close(done)
	}()

	select {
	case <-done:
		// Pass: all writes completed without blocking.
	case <-time.After(3 * time.Second):
		t.Fatal("writes blocked — notification overflow deadlock")
	}
}

func TestJSONLClient_ReadLoopExits_OnStdoutClose(t *testing.T) {
	client, srv := pipeClientServer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exited := make(chan struct{})
	go func() {
		client.Start(ctx)
		close(exited)
	}()

	// Close the server's write end — client stdout (clientIn) gets EOF.
	srv.in.Close()

	select {
	case <-exited:
		// Pass: read loop exited cleanly.
	case <-time.After(3 * time.Second):
		t.Fatal("read loop did not exit after stdout close")
	}
}
