package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/runtime"
)

// fakeAppServerDialer wires up a fake codex app-server over in-process pipes.
// It handles JSON-RPC messages from the JSONLClient side and responds according
// to the programmed response table.
type fakeAppServerDialer struct {
	t          *testing.T
	mu         sync.Mutex
	responses  map[string]json.RawMessage // method -> result JSON
	errors     map[string]*JSONRPCError   // method -> error response
	notifQueue []JSONRPCNotification      // push notifications to emit after turn/start
	notifDelay time.Duration
}

func newFakeDialer(t *testing.T) *fakeAppServerDialer {
	t.Helper()
	return &fakeAppServerDialer{
		t:         t,
		responses: make(map[string]json.RawMessage),
		errors:    make(map[string]*JSONRPCError),
	}
}

// respondWith programs a successful result for a given method.
func (d *fakeAppServerDialer) respondWith(method string, result interface{}) {
	b, err := json.Marshal(result)
	if err != nil {
		d.t.Fatalf("fakeDialer: respondWith marshal: %v", err)
	}
	d.mu.Lock()
	d.responses[method] = b
	d.mu.Unlock()
}

// respondError programs an error response for a given method.
func (d *fakeAppServerDialer) respondError(method string, code int, msg string) {
	d.mu.Lock()
	d.errors[method] = &JSONRPCError{Code: code, Message: msg}
	d.mu.Unlock()
}

// queueNotification adds a notification to send after turn/start response.
func (d *fakeAppServerDialer) queueNotification(notif JSONRPCNotification) {
	d.mu.Lock()
	d.notifQueue = append(d.notifQueue, notif)
	d.mu.Unlock()
}

// dial returns (serverStdin, serverStdout) as io.WriteCloser / io.Reader pair
// that the JSONLClient sees as its (stdin, stdout). Runs the fake server loop in background.
func (d *fakeAppServerDialer) dial(t *testing.T) (io.WriteCloser, io.Reader) {
	t.Helper()
	// Pipes: client writes → clientWrite → serverRead (server reads client requests)
	//        server writes → serverWrite → clientRead (client reads server responses)
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	go d.serveLoop(serverRead, serverWrite)

	return clientWrite, clientRead
}

func (d *fakeAppServerDialer) serveLoop(r io.Reader, w io.Writer) {
	dec := json.NewDecoder(r)
	for {
		var msg inboundMessage
		if err := dec.Decode(&msg); err != nil {
			// EOF = client closed stdin — normal shutdown
			return
		}

		if msg.ID == nil {
			// Notification from client (e.g. "initialized") — no response needed.
			continue
		}

		d.mu.Lock()
		method := msg.Method
		resp, hasResp := d.responses[method]
		rpcErr, hasErr := d.errors[method]
		notifs := d.notifQueue
		if method == "turn/start" {
			d.notifQueue = nil // consume notifications
		}
		d.mu.Unlock()

		id := *msg.ID

		if hasErr {
			reply := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      id,
				"error":   rpcErr,
			}
			b, _ := json.Marshal(reply)
			_, _ = fmt.Fprintf(w, "%s\n", b)
			continue
		}

		// Default: null result if method not programmed.
		if !hasResp {
			resp = json.RawMessage(`null`)
		}

		reply := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  json.RawMessage(resp),
		}
		b, _ := json.Marshal(reply)
		_, _ = fmt.Fprintf(w, "%s\n", b)

		// After turn/start response, push queued notifications.
		if method == "turn/start" {
			delay := d.notifDelay
			if delay == 0 {
				delay = time.Millisecond
			}
			for _, notif := range notifs {
				time.Sleep(delay)
				nb, _ := json.Marshal(notif)
				_, _ = fmt.Fprintf(w, "%s\n", nb)
			}
		}
	}
}

// newTestProcess builds an AppServerProcess wired to a fake dialer.
// It returns the process and the dialer (for adding responses post-construction).
// NOTE: does NOT call Start — tests that need Start must add responses first.
func newTestProcess(t *testing.T, dialer *fakeAppServerDialer) *AppServerProcess {
	t.Helper()
	clientWrite, clientRead := dialer.dial(t)

	// Build a minimal CLIRuntimeProfile (no real binary path needed for fakes).
	profile := runtime.CLIRuntimeProfile{
		WorkDir: t.TempDir(),
	}

	p := &AppServerProcess{
		codexPath: "/fake/codex", // never exec'd in this path
		profile:   profile,
		state:     AppServerStateIdle,
	}

	// Wire the client directly — bypassing spawn().
	client := NewJSONLClient(clientWrite, clientRead)
	readCtx, cancelReadLoop := context.WithCancel(context.Background())
	go client.Start(readCtx)

	p.mu.Lock()
	p.client = client
	p.cancelReadLoop = cancelReadLoop
	p.state = AppServerStateReady
	p.mu.Unlock()

	t.Cleanup(func() {
		cancelReadLoop()
		clientWrite.Close()
	})
	return p
}

// --- State machine tests ---

func TestAppServerProcess_InitialState(t *testing.T) {
	p := NewAppServerProcess("/fake/codex", runtime.CLIRuntimeProfile{})
	if p.State() != AppServerStateIdle {
		t.Errorf("expected Idle, got %s", p.State())
	}
}

func TestAppServerProcess_StateString(t *testing.T) {
	cases := []struct {
		state AppServerState
		want  string
	}{
		{AppServerStateIdle, "Idle"},
		{AppServerStateInitializing, "Initializing"},
		{AppServerStateReady, "Ready"},
		{AppServerStateTurnInFlight, "TurnInFlight"},
		{AppServerStateClosing, "Closing"},
		{AppServerStateClosed, "Closed"},
		{AppServerState(99), "Unknown(99)"},
	}
	for _, tc := range cases {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("state %d: got %q, want %q", tc.state, got, tc.want)
		}
	}
}

// --- Initialize handshake tests ---

func TestAppServerProcess_Initialize_OptOutSent(t *testing.T) {
	// Track what the server receives.
	var mu sync.Mutex
	var capturedParams string

	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	// Custom server: captures initialize params and responds.
	go func() {
		dec := json.NewDecoder(serverRead)
		for {
			var msg struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      *int64          `json:"id,omitempty"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params,omitempty"`
			}
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.ID == nil {
				continue // notification
			}
			if msg.Method == "initialize" {
				mu.Lock()
				capturedParams = string(msg.Params)
				mu.Unlock()
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"sessionId":"test-session"}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			}
		}
	}()

	p := &AppServerProcess{
		codexPath: "/fake/codex",
		profile:   runtime.CLIRuntimeProfile{},
		state:     AppServerStateInitializing,
	}
	client := NewJSONLClient(clientWrite, clientRead)
	readCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Start(readCtx)

	p.mu.Lock()
	p.client = client
	p.cancelReadLoop = cancel
	p.mu.Unlock()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	mu.Lock()
	params := capturedParams
	mu.Unlock()

	// Verify optOut methods were sent.
	for _, method := range OptOutNotificationMethods {
		if !strings.Contains(params, method) {
			t.Errorf("optOut method %q not found in initialize params: %s", method, params)
		}
	}
}

// TestAppServerProcess_Initialize_SendsClientInfo verifies that the initialize
// request includes a clientInfo object with a non-empty name and version.
// This is the regression test for the v5.10.0 bug where clientInfo was absent,
// causing codex 0.128.0 to reject the request with "missing field `clientInfo`".
func TestAppServerProcess_Initialize_SendsClientInfo(t *testing.T) {
	var mu sync.Mutex
	var capturedParams string

	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	go func() {
		dec := json.NewDecoder(serverRead)
		for {
			var msg struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      *int64          `json:"id,omitempty"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params,omitempty"`
			}
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.ID == nil {
				continue // notification (e.g. "initialized")
			}
			if msg.Method == "initialize" {
				mu.Lock()
				capturedParams = string(msg.Params)
				mu.Unlock()
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"sessionId":"s1"}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			}
		}
	}()

	p := &AppServerProcess{
		codexPath: "/fake/codex",
		profile:   runtime.CLIRuntimeProfile{},
		state:     AppServerStateInitializing,
	}
	client := NewJSONLClient(clientWrite, clientRead)
	readCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Start(readCtx)

	p.mu.Lock()
	p.client = client
	p.cancelReadLoop = cancel
	p.mu.Unlock()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	mu.Lock()
	params := capturedParams
	mu.Unlock()

	// clientInfo must be present with non-empty name and version.
	if !strings.Contains(params, `"clientInfo"`) {
		t.Errorf("clientInfo field absent from initialize params: %s", params)
	}
	if !strings.Contains(params, `"name":"aimux"`) {
		t.Errorf("clientInfo.name not set to 'aimux' in initialize params: %s", params)
	}
	// Version must be a non-empty string (exact value varies by build).
	if !strings.Contains(params, `"version":"`) {
		t.Errorf("clientInfo.version absent or empty in initialize params: %s", params)
	}
	// experimentalApi must always be present in wire format (no omitempty — plugin always sends it).
	if !strings.Contains(params, `"experimentalApi"`) {
		t.Errorf("experimentalApi field absent from initialize capabilities: %s", params)
	}
}

func TestAppServerProcess_Initialize_AuthFailure(t *testing.T) {
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	go func() {
		dec := json.NewDecoder(serverRead)
		for {
			var msg struct {
				JSONRPC string `json:"jsonrpc"`
				ID      *int64 `json:"id,omitempty"`
				Method  string `json:"method"`
			}
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.ID == nil {
				continue
			}
			if msg.Method == "initialize" {
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"error":{"code":-32000,"message":"Unauthorized: please run codex auth login"}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			}
		}
	}()

	p := &AppServerProcess{
		codexPath: "/fake/codex",
		profile:   runtime.CLIRuntimeProfile{},
		state:     AppServerStateInitializing,
	}
	client := NewJSONLClient(clientWrite, clientRead)
	readCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Start(readCtx)

	p.mu.Lock()
	p.client = client
	p.mu.Unlock()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	err := p.initialize(ctx)
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("expected auth mention in error, got: %v", err)
	}
}

// --- StartThread field path tests ---

// TestAppServerProcess_StartThread_ExtractsThreadID verifies the VERIFIED
// field path result.thread.id (not result.threadId — architecture.md §10).
func TestAppServerProcess_StartThread_ExtractsThreadID(t *testing.T) {
	d := newFakeDialer(t)
	d.respondWith("initialize", InitializeResult{SessionID: "s1"})
	d.respondWith("thread/start", ThreadStartResponse{
		Thread: Thread{ID: "thread-abc-123", CWD: "/tmp/work"},
	})

	p := newTestProcess(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	thread, err := p.StartThread(ctx, ThreadStartParams{
		CWD:       "/tmp/work",
		Ephemeral: false,
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}

	// VERIFIED: result.thread.id
	if thread.ID != "thread-abc-123" {
		t.Errorf("thread.ID: got %q, want %q", thread.ID, "thread-abc-123")
	}
	if thread.CWD != "/tmp/work" {
		t.Errorf("thread.CWD: got %q, want %q", thread.CWD, "/tmp/work")
	}
}

func TestAppServerProcess_StartThread_WrongState(t *testing.T) {
	p := NewAppServerProcess("/fake/codex", runtime.CLIRuntimeProfile{})
	// State is Idle — should fail.
	ctx := context.Background()
	_, err := p.StartThread(ctx, ThreadStartParams{})
	if err == nil {
		t.Error("expected error in Idle state, got nil")
	}
}

// --- StartTurn field path + notification routing tests ---

// TestAppServerProcess_StartTurn_ExtractsTurnID verifies result.turn.id field path.
func TestAppServerProcess_StartTurn_ExtractsTurnID(t *testing.T) {
	d := newFakeDialer(t)

	// Program turn/start response with VERIFIED field path.
	d.respondWith("turn/start", TurnStartResponse{
		Turn: Turn{ID: "turn-xyz-456", Status: TurnStatusRunning},
	})

	// Queue a turn/completed notification to end the turn.
	completedParams, _ := json.Marshal(TurnCompletedNotification{
		ThreadID: "thread-abc",
		Turn:     Turn{ID: "turn-xyz-456", Status: TurnStatusCompleted},
	})
	d.queueNotification(JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  MethodTurnCompleted,
		Params:  completedParams,
	})

	p := newTestProcess(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	completedCh, progressCh, err := p.StartTurn(ctx, TurnStartParams{
		ThreadID: "thread-abc",
		Input:    []UserInput{{Type: "text", Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	// Drain both channels.
	var completed TurnCompletedNotification
	var got bool
	for {
		select {
		case c, ok := <-completedCh:
			if ok {
				completed = c
				got = true
			}
		case _, ok := <-progressCh:
			if !ok {
				goto done
			}
		case <-ctx.Done():
			t.Fatal("timeout waiting for turn completion")
		}
		if got && len(completedCh) == 0 && len(progressCh) == 0 {
			// Give channels time to close.
			time.Sleep(5 * time.Millisecond)
			break
		}
	}
done:

	// VERIFIED: result.turn.id
	if completed.Turn.ID != "turn-xyz-456" {
		t.Errorf("turn.ID: got %q, want %q", completed.Turn.ID, "turn-xyz-456")
	}
	if completed.Turn.Status != TurnStatusCompleted {
		t.Errorf("turn.Status: got %q, want %q", completed.Turn.Status, TurnStatusCompleted)
	}
}

// TestAppServerProcess_StartTurn_ProgressRouting verifies that agentMessage items
// produce text on the progress channel (architecture.md §10 Test 1).
func TestAppServerProcess_StartTurn_ProgressRouting(t *testing.T) {
	d := newFakeDialer(t)
	d.respondWith("turn/start", TurnStartResponse{
		Turn: Turn{ID: "t1", Status: TurnStatusRunning},
	})

	// Queue an item/completed notification with agentMessage.
	itemParams, _ := json.Marshal(ItemCompletedNotification{
		Item:     ThreadItem{Type: "agentMessage", ID: "item-1", Text: "Hello from agent"},
		ThreadID: "thread-1",
		TurnID:   "t1",
	})
	d.queueNotification(JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  MethodItemCompleted,
		Params:  itemParams,
	})

	// Queue turn/completed to end the turn.
	completedParams, _ := json.Marshal(TurnCompletedNotification{
		ThreadID: "thread-1",
		Turn:     Turn{ID: "t1", Status: TurnStatusCompleted},
	})
	d.queueNotification(JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  MethodTurnCompleted,
		Params:  completedParams,
	})

	p := newTestProcess(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, progressCh, err := p.StartTurn(ctx, TurnStartParams{
		ThreadID: "thread-1",
		Input:    []UserInput{{Type: "text", Text: "go"}},
	})
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	var texts []string
	for text := range progressCh {
		texts = append(texts, text)
	}

	if len(texts) == 0 {
		t.Fatal("expected at least one progress text, got none")
	}
	if texts[0] != "Hello from agent" {
		t.Errorf("progress[0]: got %q, want %q", texts[0], "Hello from agent")
	}
}

// --- ResumeThread tests ---

func TestAppServerProcess_ResumeThread_NotFound(t *testing.T) {
	d := newFakeDialer(t)
	// -32600 = thread not found per architecture.md §10.
	d.respondError("thread/resume", -32600, "thread not found")

	p := newTestProcess(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := p.ResumeThread(ctx, ThreadResumeParams{ThreadID: "ghost-thread"})
	if err == nil {
		t.Fatal("expected ErrThreadNotFound, got nil")
	}
	if err != ErrThreadNotFound {
		t.Errorf("expected ErrThreadNotFound, got: %v", err)
	}
}

func TestAppServerProcess_ResumeThread_Success(t *testing.T) {
	d := newFakeDialer(t)
	d.respondWith("thread/resume", ThreadResumeResponse{
		Thread: Thread{ID: "thread-resumed", CWD: "/workspace"},
	})

	p := newTestProcess(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	thread, err := p.ResumeThread(ctx, ThreadResumeParams{ThreadID: "thread-resumed"})
	if err != nil {
		t.Fatalf("ResumeThread: %v", err)
	}
	if thread.ID != "thread-resumed" {
		t.Errorf("thread.ID: got %q, want %q", thread.ID, "thread-resumed")
	}
}

// --- Interrupt tests ---

func TestAppServerProcess_Interrupt_NoOp_WhenNotInFlight(t *testing.T) {
	p := newTestProcess(t, newFakeDialer(t))
	// State is Ready — Interrupt should be a no-op.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := p.Interrupt(ctx); err != nil {
		t.Errorf("Interrupt in Ready state: unexpected error: %v", err)
	}
}

func TestAppServerProcess_Interrupt_SendsCorrectParams(t *testing.T) {
	var mu sync.Mutex
	var capturedInterruptParams string

	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	go func() {
		dec := json.NewDecoder(serverRead)
		for {
			var msg struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      *int64          `json:"id,omitempty"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params,omitempty"`
			}
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.ID == nil {
				continue
			}
			switch msg.Method {
			case "turn/interrupt":
				mu.Lock()
				capturedInterruptParams = string(msg.Params)
				mu.Unlock()
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			default:
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			}
		}
	}()

	p := &AppServerProcess{
		codexPath: "/fake/codex",
		profile:   runtime.CLIRuntimeProfile{},
		state:     AppServerStateTurnInFlight,
	}
	client := NewJSONLClient(clientWrite, clientRead)
	readCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Start(readCtx)

	p.mu.Lock()
	p.client = client
	p.cancelReadLoop = cancel
	p.activeThreadID = "thread-abc"
	p.activeTurnID = "turn-xyz"
	p.mu.Unlock()

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()

	if err := p.Interrupt(ctx); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	mu.Lock()
	params := capturedInterruptParams
	mu.Unlock()

	if !strings.Contains(params, `"threadId":"thread-abc"`) {
		t.Errorf("interrupt params missing threadId: %s", params)
	}
	if !strings.Contains(params, `"turnId":"turn-xyz"`) {
		t.Errorf("interrupt params missing turnId: %s", params)
	}
}

// --- appendOrReplace helper ---

func TestAppendOrReplace_Add(t *testing.T) {
	env := []string{"PATH=/usr/bin", "HOME=/root"}
	result := appendOrReplace(env, "CODEX_HOME", "/codex-home")
	if len(result) != 3 {
		t.Errorf("expected 3 entries, got %d: %v", len(result), result)
	}
	found := false
	for _, kv := range result {
		if kv == "CODEX_HOME=/codex-home" {
			found = true
		}
	}
	if !found {
		t.Errorf("CODEX_HOME not found in result: %v", result)
	}
}

func TestAppendOrReplace_Replace(t *testing.T) {
	env := []string{"PATH=/usr/bin", "CODEX_HOME=/old", "HOME=/root"}
	result := appendOrReplace(env, "CODEX_HOME", "/new")
	if len(result) != 3 {
		t.Errorf("expected 3 entries (replace in-place), got %d: %v", len(result), result)
	}
	for _, kv := range result {
		if kv == "CODEX_HOME=/old" {
			t.Errorf("old value still present: %v", result)
		}
	}
	found := false
	for _, kv := range result {
		if kv == "CODEX_HOME=/new" {
			found = true
		}
	}
	if !found {
		t.Errorf("new value not found: %v", result)
	}
}

func TestAppendOrReplace_Immutable(t *testing.T) {
	// Verify original slice is not mutated.
	env := []string{"A=1", "B=2"}
	original := make([]string, len(env))
	copy(original, env)

	_ = appendOrReplace(env, "C", "3")

	for i, kv := range env {
		if kv != original[i] {
			t.Errorf("original[%d] mutated: got %q, want %q", i, kv, original[i])
		}
	}
}
