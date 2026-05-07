package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/executor/runtime"
)

// AppServerState is the process lifecycle state machine.
// Transitions: Idle → Initializing → Ready → TurnInFlight → Closing → Closed
// See architecture.md §12 for the state machine invariant.
type AppServerState int

const (
	AppServerStateIdle        AppServerState = iota
	AppServerStateInitializing
	AppServerStateReady
	AppServerStateTurnInFlight
	AppServerStateClosing
	AppServerStateClosed
)

func (s AppServerState) String() string {
	switch s {
	case AppServerStateIdle:
		return "Idle"
	case AppServerStateInitializing:
		return "Initializing"
	case AppServerStateReady:
		return "Ready"
	case AppServerStateTurnInFlight:
		return "TurnInFlight"
	case AppServerStateClosing:
		return "Closing"
	case AppServerStateClosed:
		return "Closed"
	default:
		return fmt.Sprintf("Unknown(%d)", int(s))
	}
}

// ErrThreadNotFound is returned when thread/resume fails because the thread
// does not exist in ~/.codex/sessions/ (or any configured state dir).
// Callers may fall back to starting a fresh thread on this sentinel.
var ErrThreadNotFound = errors.New("codex: thread not found")

// AppServerProcess manages a single `codex app-server` subprocess.
//
// Invariant: only one turn is in-flight at any time. The turnMu mutex serializes
// concurrent StartTurn calls — concurrent callers queue implicitly.
//
// The state machine is protected by mu. State transitions:
//   Idle → Initializing  (Start called)
//   Initializing → Ready  (initialize handshake completed)
//   Ready → TurnInFlight  (StartTurn called)
//   TurnInFlight → Ready  (turn completed)
//   Any → Closing → Closed  (Shutdown called)
type AppServerProcess struct {
	codexPath string
	profile   runtime.CLIRuntimeProfile

	mu             sync.Mutex
	state          AppServerState
	cmd            *exec.Cmd
	client         *JSONLClient
	cancelReadLoop context.CancelFunc

	// turnMu serializes concurrent StartTurn calls — one turn at a time per process.
	turnMu sync.Mutex

	// activeTurnID and activeThreadID track the in-flight turn for Interrupt.
	activeThreadID string
	activeTurnID   string
}

// NewAppServerProcess constructs an AppServerProcess.
// codexPath must be the absolute path to the codex binary (from exec.LookPath).
func NewAppServerProcess(codexPath string, profile runtime.CLIRuntimeProfile) *AppServerProcess {
	return &AppServerProcess{
		codexPath: codexPath,
		profile:   profile,
		state:     AppServerStateIdle,
	}
}

// Start spawns the codex app-server process and completes the initialize handshake.
// Must be called once before StartThread. Returns an error on auth failure or binary issues.
func (p *AppServerProcess) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.state != AppServerStateIdle {
		p.mu.Unlock()
		return fmt.Errorf("codex AppServerProcess: Start called in state %s", p.state)
	}
	p.state = AppServerStateInitializing
	p.mu.Unlock()

	if err := p.spawn(ctx); err != nil {
		p.setState(AppServerStateClosed)
		return err
	}
	if err := p.initialize(ctx); err != nil {
		p.setState(AppServerStateClosed)
		_ = p.kill()
		return err
	}
	p.setState(AppServerStateReady)
	return nil
}

// spawn forks the `codex app-server` process and wires up the JSONLClient.
func (p *AppServerProcess) spawn(ctx context.Context) error {
	args := []string{"app-server"}

	cmd := exec.CommandContext(ctx, p.codexPath, args...)

	// Build environment from profile.
	if p.profile.VirtualHomeDir != "" {
		// Ensure VirtualHomeDir exists before setting CODEX_HOME.
		if err := os.MkdirAll(p.profile.VirtualHomeDir, 0o700); err != nil {
			return fmt.Errorf("codex: create virtual home dir %q: %w", p.profile.VirtualHomeDir, err)
		}
	}

	env := os.Environ()
	if p.profile.CLIHomeEnvVar != "" && p.profile.VirtualHomeDir != "" {
		// Inject CLI-specific home redirect (e.g., CODEX_HOME).
		env = appendOrReplace(env, p.profile.CLIHomeEnvVar, p.profile.VirtualHomeDir)
	}
	for k, v := range p.profile.EnvOverrides {
		env = appendOrReplace(env, k, v)
	}
	cmd.Env = env

	if p.profile.WorkDir != "" {
		cmd.Dir = p.profile.WorkDir
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("codex: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("codex: stdout pipe: %w", err)
	}

	// Discard stderr to avoid blocking. Codex may write diagnostic output there.
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("codex: start app-server: %w", err)
	}

	client := NewJSONLClient(stdin, stdout)
	readCtx, cancelReadLoop := context.WithCancel(context.Background())
	go client.Start(readCtx)

	p.mu.Lock()
	p.cmd = cmd
	p.client = client
	p.cancelReadLoop = cancelReadLoop
	p.mu.Unlock()

	return nil
}

// initialize performs the JSON-RPC initialize handshake (ADR-011).
// On auth failure, returns an error with an actionable message.
func (p *AppServerProcess) initialize(ctx context.Context) error {
	params := InitializeParams{
		Capabilities: InitializeCapabilities{
			OptOutNotificationMethods: OptOutNotificationMethods,
		},
	}
	var result InitializeResult
	if err := p.client.Call(ctx, "initialize", params, &result); err != nil {
		rpcErr := &JSONRPCError{}
		if errors.As(err, &rpcErr) {
			// Auth failure detection: codex returns auth-related errors with specific messages.
			msg := strings.ToLower(rpcErr.Message)
			if strings.Contains(msg, "auth") || strings.Contains(msg, "unauthorized") ||
				strings.Contains(msg, "unauthenticated") || strings.Contains(msg, "login") {
				return fmt.Errorf(
					"codex auth failure: run 'codex auth login' and restart aimux (detail: %s)",
					rpcErr.Message,
				)
			}
		}
		return fmt.Errorf("codex: initialize RPC: %w", err)
	}

	// Send the `initialized` notification to complete the handshake.
	if err := p.client.Notify(ctx, "initialized", nil); err != nil {
		return fmt.Errorf("codex: send initialized notification: %w", err)
	}
	return nil
}

// StartThread calls thread/start and returns the created Thread.
// The cwd in params controls the working directory for this thread.
// VERIFIED: result.thread.id is the correct field path (architecture.md §10).
func (p *AppServerProcess) StartThread(ctx context.Context, params ThreadStartParams) (Thread, error) {
	p.mu.Lock()
	if p.state != AppServerStateReady {
		p.mu.Unlock()
		return Thread{}, fmt.Errorf("codex AppServerProcess: StartThread in state %s", p.state)
	}
	p.mu.Unlock()

	var resp ThreadStartResponse
	if err := p.client.Call(ctx, "thread/start", params, &resp); err != nil {
		return Thread{}, fmt.Errorf("codex: thread/start: %w", err)
	}
	return resp.Thread, nil
}

// ResumeThread calls thread/resume and returns the resumed Thread.
// Converts -32600 "thread not found" RPC errors to ErrThreadNotFound.
func (p *AppServerProcess) ResumeThread(ctx context.Context, params ThreadResumeParams) (Thread, error) {
	p.mu.Lock()
	if p.state != AppServerStateReady && p.state != AppServerStateTurnInFlight {
		p.mu.Unlock()
		return Thread{}, fmt.Errorf("codex AppServerProcess: ResumeThread in state %s", p.state)
	}
	p.mu.Unlock()

	var resp ThreadResumeResponse
	if err := p.client.Call(ctx, "thread/resume", params, &resp); err != nil {
		var rpcErr *JSONRPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32600 {
			return Thread{}, ErrThreadNotFound
		}
		return Thread{}, fmt.Errorf("codex: thread/resume: %w", err)
	}
	return resp.Thread, nil
}

// StartTurn calls turn/start and drives the notification loop.
// Returns a channel that receives TurnCompletedNotification when the turn finishes,
// and a channel that receives agent message text lines as progress.
//
// Callers MUST drain both channels until they are closed. The channels close
// when the turn completes or when ctx is cancelled.
//
// The turnMu ensures only one turn is in-flight at a time per process (ADR-005).
func (p *AppServerProcess) StartTurn(ctx context.Context, params TurnStartParams) (
	<-chan TurnCompletedNotification,
	<-chan string,
	error,
) {
	p.turnMu.Lock()
	// Note: turnMu is released after we start the fanout goroutine, not deferred.

	p.mu.Lock()
	if p.state != AppServerStateReady {
		p.mu.Unlock()
		p.turnMu.Unlock()
		return nil, nil, fmt.Errorf("codex AppServerProcess: StartTurn in state %s", p.state)
	}
	p.state = AppServerStateTurnInFlight
	p.mu.Unlock()

	var resp TurnStartResponse
	if err := p.client.Call(ctx, "turn/start", params, &resp); err != nil {
		p.setState(AppServerStateReady)
		p.turnMu.Unlock()
		return nil, nil, fmt.Errorf("codex: turn/start: %w", err)
	}

	p.mu.Lock()
	p.activeTurnID = resp.Turn.ID
	p.activeThreadID = params.ThreadID
	p.mu.Unlock()

	completedCh := make(chan TurnCompletedNotification, 1)
	progressCh := make(chan string, 32)

	// Fan-out goroutine: routes notifications until turn completion or ctx cancel.
	go func() {
		defer p.turnMu.Unlock()
		defer close(completedCh)
		defer close(progressCh)

		for {
			select {
			case <-ctx.Done():
				p.setState(AppServerStateReady)
				return
			case raw, ok := <-p.client.Notifications():
				if !ok {
					// Client closed — process exited.
					p.setState(AppServerStateClosed)
					return
				}
				if p.handleNotification(raw, completedCh, progressCh) {
					// turn/completed was emitted — we are done.
					p.setState(AppServerStateReady)
					return
				}
			}
		}
	}()

	return completedCh, progressCh, nil
}

// handleNotification parses a raw notification and routes it to the appropriate channel.
// Returns true when turn/completed was emitted, signalling the fanout goroutine to exit.
func (p *AppServerProcess) handleNotification(
	raw json.RawMessage,
	completedCh chan<- TurnCompletedNotification,
	progressCh chan<- string,
) bool {
	var notif JSONRPCNotification
	if err := json.Unmarshal(raw, &notif); err != nil {
		return false
	}

	switch notif.Method {
	case MethodTurnCompleted:
		var tcn TurnCompletedNotification
		if err := json.Unmarshal(notif.Params, &tcn); err != nil {
			return false
		}
		select {
		case completedCh <- tcn:
		default:
		}
		return true

	case MethodItemCompleted:
		var icn ItemCompletedNotification
		if err := json.Unmarshal(notif.Params, &icn); err != nil {
			return false
		}
		if icn.Item.Type == "agentMessage" && icn.Item.Text != "" {
			select {
			case progressCh <- icn.Item.Text:
			default:
				// Drop on overflow — progress is best-effort.
			}
		}
	}
	return false
}

// Interrupt sends turn/interrupt if a turn is in-flight.
// Has no effect if no turn is active. Per ADR-010, requires both threadId and turnId.
// If turnId is unavailable, callers must kill the process instead.
func (p *AppServerProcess) Interrupt(ctx context.Context) error {
	p.mu.Lock()
	if p.state != AppServerStateTurnInFlight {
		p.mu.Unlock()
		return nil // no-op: not in-flight
	}
	threadID := p.activeThreadID
	turnID := p.activeTurnID
	p.mu.Unlock()

	if threadID == "" || turnID == "" {
		return fmt.Errorf("codex: Interrupt: threadId or turnId unavailable — kill process instead")
	}

	params := TurnInterruptParams{
		ThreadID: threadID,
		TurnID:   turnID,
	}
	var resp TurnInterruptResponse
	if err := p.client.Call(ctx, "turn/interrupt", params, &resp); err != nil {
		return fmt.Errorf("codex: turn/interrupt: %w", err)
	}
	return nil
}

// Shutdown gracefully terminates the process.
// Order per ADR-010: interrupt in-flight turn → wait up to 5s → close stdin → kill.
func (p *AppServerProcess) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if p.state == AppServerStateClosed {
		p.mu.Unlock()
		return nil
	}
	p.state = AppServerStateClosing
	cmd := p.cmd
	client := p.client
	cancelReadLoop := p.cancelReadLoop
	p.mu.Unlock()

	// Interrupt in-flight turn (best effort — ignore errors).
	interruptCtx, interruptCancel := context.WithTimeout(ctx, 3*time.Second)
	defer interruptCancel()
	_ = p.Interrupt(interruptCtx)

	// Stop the read loop.
	if cancelReadLoop != nil {
		cancelReadLoop()
	}

	// Close the client (drains pending calls with error).
	if client != nil {
		client.Close()
	}

	// Close stdin to signal EOF to the subprocess.
	if cmd != nil && cmd.Process != nil {
		// Close stdin via the client's underlying writer — already done by client.Close().
		// Wait for process to exit.
		waitDone := make(chan error, 1)
		go func() { waitDone <- cmd.Wait() }()
		select {
		case <-waitDone:
		case <-time.After(5 * time.Second):
			_ = p.kill()
		case <-ctx.Done():
			_ = p.kill()
		}
	}

	p.setState(AppServerStateClosed)
	return nil
}

// kill sends SIGKILL to the process. Used as last resort after timeout.
func (p *AppServerProcess) kill() error {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// State returns the current state of the process (thread-safe snapshot).
func (p *AppServerProcess) State() AppServerState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *AppServerProcess) setState(s AppServerState) {
	p.mu.Lock()
	p.state = s
	p.mu.Unlock()
}

// appendOrReplace sets key=value in an env slice, replacing any existing entry.
func appendOrReplace(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			out := make([]string, len(env))
			copy(out, env)
			out[i] = prefix + value
			return out
		}
	}
	return append(env, prefix+value)
}
