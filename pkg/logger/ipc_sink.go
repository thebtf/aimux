// Package logger: IPCSink forwards log entries from shim processes to the daemon
// via JSON-RPC notifications over the existing muxcore session connection.
// Sole-writer invariant (ADR-6): the shim never opens the log file directly —
// every entry either reaches the daemon (via this sink) or stderr (via fallback).
// (T015, T016 — AIMUX-11 Phase 3, FR-3 + FR-4 + FR-7 + FR-8)
package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// SendFunc is the transport-level callback the IPCSink uses to deliver a single
// JSON-RPC notification frame to the daemon. The bytes already include the full
// {"jsonrpc":"2.0","method":"notifications/aimux/log_forward","params":...} envelope
// — the SendFunc just writes them to the wire.
//
// Implementations must:
//   - Block until the bytes are written, OR until the configured timeout expires.
//   - Return a non-nil error on write failure (connection closed, daemon down, etc).
//   - Be safe to call concurrently (the IPCSink serializes via its drain goroutine).
type SendFunc func(notification []byte) error

// IPCSinkOpts configures IPCSink behavior.
type IPCSinkOpts struct {
	// BufferSize is the ring-buffer capacity for entries pending delivery.
	// Default 100 (FR-7). When full, oldest entry is dropped.
	BufferSize int
	// TimeoutMs is the per-send timeout in milliseconds.
	// Default 100 (FR-9). Exceeding the timeout routes the entry to fallback.
	TimeoutMs int
	// ReconnectInitialMs is the initial reconnect backoff after a send failure.
	// Default 1000 (1s). Doubles on each failure up to ReconnectMaxMs.
	ReconnectInitialMs int
	// ReconnectMaxMs caps the reconnect backoff. Default 5000 (5s).
	ReconnectMaxMs int
}

// IPCSink state values.
const (
	ipcStateIdle       int32 = 0
	ipcStateReady      int32 = 1
	ipcStateDegraded   int32 = 2
)

// IPCSink is the shim-mode log destination. It enqueues entries non-blocking,
// serialises them off-thread, and delivers via the supplied SendFunc. On failure
// it routes entries to the StderrFallback so no data is lost.
//
// Lifecycle:
//   - NewIPCSink starts a background goroutine.
//   - Send() enqueues an entry (non-blocking).
//   - Close() drains remaining buffer to fallback and stops the goroutine.
type IPCSink struct {
	sendMu   sync.RWMutex // protects send swap
	send     SendFunc
	fallback *StderrFallback
	opts     IPCSinkOpts

	ringBuf chan LogEntry
	closeCh chan struct{}
	wg      sync.WaitGroup

	closeOnce sync.Once   // ensures Close() body executes exactly once (CR-002 BUG-002)
	closed    atomic.Bool // visible to Send() so post-Close emits route to fallback (CR-002 BUG-003)

	state         atomic.Int32
	dropped       atomic.Uint64 // ring buffer overflow drops
	sendFailures  atomic.Uint64 // send call returned error
	fallbackUsed  atomic.Uint64 // entries routed to StderrFallback

	lastWarnNanos atomic.Int64 // for rate-limited overflow warnings
}

// SetSendFunc atomically swaps the transport callback. Used to wire the muxcore
// inject closure post-handshake (engine.Config.OnInject path). Pass nil to
// disable transport — all entries route to fallback until the next non-nil set.
func (s *IPCSink) SetSendFunc(send SendFunc) {
	s.sendMu.Lock()
	s.send = send
	s.sendMu.Unlock()
	if send != nil {
		s.state.Store(ipcStateReady)
	} else {
		s.state.Store(ipcStateIdle)
	}
}

// getSendFunc returns the current send function (nil-safe).
func (s *IPCSink) getSendFunc() SendFunc {
	s.sendMu.RLock()
	defer s.sendMu.RUnlock()
	return s.send
}

// NewIPCSink starts a new IPCSink with a background drain goroutine.
//
// send: transport callback. Pass nil only in tests where the goroutine is not exercised.
// fallback: stderr fallback writer. Required (constructor panics if nil).
// opts: behavior config; zero fields are replaced with defaults.
func NewIPCSink(send SendFunc, opts IPCSinkOpts, fallback *StderrFallback) *IPCSink {
	if fallback == nil {
		panic("logger.NewIPCSink: fallback must not be nil")
	}
	if opts.BufferSize <= 0 {
		opts.BufferSize = 100
	}
	if opts.TimeoutMs <= 0 {
		opts.TimeoutMs = 100
	}
	if opts.ReconnectInitialMs <= 0 {
		opts.ReconnectInitialMs = 1000
	}
	if opts.ReconnectMaxMs <= 0 {
		opts.ReconnectMaxMs = 5000
	}

	s := &IPCSink{
		send:     send,
		fallback: fallback,
		opts:     opts,
		ringBuf:  make(chan LogEntry, opts.BufferSize),
		closeCh:  make(chan struct{}),
	}
	s.state.Store(ipcStateIdle)

	s.wg.Add(1)
	go s.drainLoop()
	return s
}

// Send enqueues an entry for forwarding. Non-blocking; on ring-buffer overflow
// the oldest entry is dropped and a rate-limited stderr warning is emitted.
//
// Post-Close behavior (CR-002 BUG-003 fix): once Close() has marked the sink as
// closed, every subsequent Send routes to fallback synchronously instead of
// enqueueing into ringBuf — the drain goroutine has already exited and any new
// entry would otherwise sit in ringBuf until process exit and silently disappear.
func (s *IPCSink) Send(entry LogEntry) {
	if s.closed.Load() {
		s.fallback.WriteEntry(entry)
		s.fallbackUsed.Add(1)
		return
	}
	select {
	case s.ringBuf <- entry:
		return
	default:
	}

	// Buffer full — drop oldest, then enqueue the new one.
	select {
	case <-s.ringBuf:
		s.dropped.Add(1)
		s.warnOverflow()
	default:
		// Reader raced and emptied the slot — fine, re-attempt enqueue.
	}

	select {
	case s.ringBuf <- entry:
	default:
		// Still full — drop the new entry to fallback so we never block.
		s.fallback.WriteEntry(entry)
		s.fallbackUsed.Add(1)
	}
}

// warnOverflow emits at most one stderr warning per second to avoid log floods.
func (s *IPCSink) warnOverflow() {
	now := time.Now().UnixNano()
	last := s.lastWarnNanos.Load()
	if now-last < int64(time.Second) {
		return
	}
	if !s.lastWarnNanos.CompareAndSwap(last, now) {
		return
	}
	dropped := s.dropped.Load()
	_, _ = fmt.Fprintf(os.Stderr,
		"[stderr-fallback] %s [WARN] log channel saturated — total dropped: %d\n",
		time.Now().Format(time.RFC3339Nano), dropped)
}

// Close drains remaining entries to fallback and stops the background goroutine.
// Safe to call concurrently and multiple times — sync.Once guards both the
// closed-flag store and the channel close (CR-002 BUG-002 fix; the prior
// non-atomic select+close pattern panicked under concurrent invocation).
func (s *IPCSink) Close() error {
	s.closeOnce.Do(func() {
		s.closed.Store(true) // visible to Send() before drain starts
		close(s.closeCh)
	})
	s.wg.Wait()

	// Final drain — anything still in the buffer goes to fallback.
	for {
		select {
		case e := <-s.ringBuf:
			s.fallback.WriteEntry(e)
			s.fallbackUsed.Add(1)
		default:
			return nil
		}
	}
}

// State returns the current IPC sink state (idle/ready/degraded). Test/observability hook.
func (s *IPCSink) State() int32 { return s.state.Load() }

// Stats returns the observability counters as a snapshot.
func (s *IPCSink) Stats() (dropped, sendFailures, fallbackUsed uint64) {
	return s.dropped.Load(), s.sendFailures.Load(), s.fallbackUsed.Load()
}

// drainLoop is the background goroutine that ships entries from the ring buffer
// to the SendFunc. On send failure, it routes the entry to fallback and waits
// for an exponential-backoff reconnect window before retrying.
func (s *IPCSink) drainLoop() {
	defer s.wg.Done()

	backoff := time.Duration(s.opts.ReconnectInitialMs) * time.Millisecond
	maxBackoff := time.Duration(s.opts.ReconnectMaxMs) * time.Millisecond
	timeout := time.Duration(s.opts.TimeoutMs) * time.Millisecond

	for {
		select {
		case <-s.closeCh:
			return
		case entry := <-s.ringBuf:
			send := s.getSendFunc()
			if send == nil {
				// Pre-handshake (SetSendFunc not yet called) or post-disconnect:
				// route to fallback so no entry is silently dropped.
				s.fallback.WriteEntry(entry)
				s.fallbackUsed.Add(1)
				continue
			}

			// Build envelope and dispatch with timeout.
			notification, err := buildLogForwardNotification(entry)
			if err != nil {
				// Marshal failure — route to fallback (should never happen in practice).
				s.fallback.WriteEntry(entry)
				s.fallbackUsed.Add(1)
				continue
			}

			sendErr := s.sendWithTimeoutVia(send, notification, timeout)
			if sendErr != nil {
				// Send failed — fallback + degraded + backoff.
				s.fallback.WriteEntry(entry)
				s.fallbackUsed.Add(1)
				s.sendFailures.Add(1)
				s.state.Store(ipcStateDegraded)

				// Wait backoff or close, whichever comes first.
				select {
				case <-s.closeCh:
					return
				case <-time.After(backoff):
				}

				// Increase backoff up to max for next retry.
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}

			// Success — reset backoff and mark ready.
			backoff = time.Duration(s.opts.ReconnectInitialMs) * time.Millisecond
			s.state.Store(ipcStateReady)
		}
	}
}

// sendWithTimeoutVia invokes the supplied SendFunc on a separate goroutine and
// waits up to the timeout. Returns the SendFunc's error or a timeout error.
// Caller passes the snapshot to avoid re-acquiring sendMu in the drain loop hot path.
//
// Non-blocking SendFunc contract (CR-002 P0 fix — BUG-001):
//
// SendFunc MUST be non-blocking — it MUST return within a bounded short interval
// (microseconds) regardless of remote-endpoint state. The OnInject inject closure
// produced by muxcore satisfies this: it writes to msgFromCC channel using
// `select { case msgFromCC <- b: default: return ErrInjectFull }`. No blocking
// fallback path exists upstream as of muxcore v0.23.0.
//
// IF a future SendFunc implementation violates the contract and blocks beyond the
// timeout, the inner goroutine here will outlive the function call: it cannot be
// cancelled (Go does not preempt blocking syscalls), but the buffered `done`
// channel (capacity 1) ensures the goroutine's final write `done <- send(...)`
// is non-blocking, so the goroutine completes naturally once the underlying
// send unblocks. No infinite-leak path exists IF send() ever returns at all;
// a permanently-stuck send would leak one goroutine per invocation, which is a
// muxcore-side regression and would be detectable via TestSendWithTimeoutVia_NoLeakUnderTimeout.
func (s *IPCSink) sendWithTimeoutVia(send SendFunc, notification []byte, timeout time.Duration) error {
	if send == nil {
		return fmt.Errorf("ipc_sink: send func is nil")
	}
	done := make(chan error, 1) // buffered=1 — late goroutine write never blocks
	go func() {
		done <- send(notification)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("ipc_sink: send timeout after %v", timeout)
	}
}

// buildLogForwardNotification serialises a LogEntry into the JSON-RPC notification envelope.
// Format: {"jsonrpc":"2.0","method":"notifications/aimux/log_forward","params":<entry>}
func buildLogForwardNotification(entry LogEntry) ([]byte, error) {
	type envelope struct {
		JSONRPC string   `json:"jsonrpc"`
		Method  string   `json:"method"`
		Params  LogEntry `json:"params"`
	}
	return json.Marshal(envelope{
		JSONRPC: "2.0",
		Method:  "notifications/aimux/log_forward",
		Params:  entry,
	})
}
