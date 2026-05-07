package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// TestAppServerProcess_Compact_SendsCorrectRPC verifies that Compact sends
// thread/compact/start with {threadId} and waits for turn/completed (FR-11 / ADR-013).
func TestAppServerProcess_Compact_SendsCorrectRPC(t *testing.T) {
	var capturedCompactParams string

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
			case "thread/compact/start":
				capturedCompactParams = string(msg.Params)
				// Return {} immediately (per probe-2026-05-07 OQ-7).
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
				// Emit the turn/completed notification asynchronously.
				go func() {
					time.Sleep(5 * time.Millisecond)
					completed, _ := json.Marshal(TurnCompletedNotification{
						ThreadID: "thread-compact-1",
						Turn:     Turn{ID: "turn-compact-1", Status: TurnStatusCompleted},
					})
					notif, _ := json.Marshal(JSONRPCNotification{
						JSONRPC: "2.0",
						Method:  MethodTurnCompleted,
						Params:  completed,
					})
					fmt.Fprintln(serverWrite, string(notif))
				}()
			default:
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			}
		}
	}()

	proc := buildProcessFromPipePair(t, clientWrite, clientRead)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := proc.Compact(ctx, "thread-compact-1"); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Verify the RPC params contain threadId.
	if !strings.Contains(capturedCompactParams, `"threadId":"thread-compact-1"`) {
		t.Errorf("compact params missing threadId: %s", capturedCompactParams)
	}
}

// TestAppServerProcess_Compact_WaitsForTurnCompleted verifies that Compact does not
// return before turn/completed arrives (not item/completed — per probe OQ-7).
func TestAppServerProcess_Compact_WaitsForTurnCompleted(t *testing.T) {
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	turnCompletedAt := make(chan time.Time, 1)
	compactReturnedAt := make(chan time.Time, 1)

	go func() {
		dec := json.NewDecoder(serverRead)
		for {
			var msg struct {
				JSONRPC string  `json:"jsonrpc"`
				ID      *int64  `json:"id,omitempty"`
				Method  string  `json:"method"`
			}
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.ID == nil {
				continue
			}
			switch msg.Method {
			case "thread/compact/start":
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
				// Delay turn/completed by 30ms to verify Compact blocks.
				go func() {
					time.Sleep(30 * time.Millisecond)
					turnCompletedAt <- time.Now()
					completed, _ := json.Marshal(TurnCompletedNotification{
						ThreadID: "thread-wait",
						Turn:     Turn{ID: "turn-wait", Status: TurnStatusCompleted},
					})
					notif, _ := json.Marshal(JSONRPCNotification{
						JSONRPC: "2.0",
						Method:  MethodTurnCompleted,
						Params:  completed,
					})
					fmt.Fprintln(serverWrite, string(notif))
				}()
			default:
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			}
		}
	}()

	proc := buildProcessFromPipePair(t, clientWrite, clientRead)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	compactErrCh := make(chan error, 1)
	go func() {
		compactErrCh <- proc.Compact(ctx, "thread-wait")
		compactReturnedAt <- time.Now()
	}()

	var tcAt, crAt time.Time
	select {
	case tcAt = <-turnCompletedAt:
	case <-ctx.Done():
		t.Fatal("timeout waiting for turn/completed signal")
	}
	select {
	case crAt = <-compactReturnedAt:
	case <-ctx.Done():
		t.Fatal("timeout waiting for Compact to return")
	}

	// Compact must return AFTER turn/completed was emitted.
	if crAt.Before(tcAt) {
		t.Errorf("Compact returned before turn/completed: Compact=%v turnCompleted=%v", crAt, tcAt)
	}
	if err := <-compactErrCh; err != nil {
		t.Fatalf("Compact returned unexpected error: %v", err)
	}
}

// TestAppServerProcess_Compact_RPCError verifies that an RPC error from
// thread/compact/start is propagated as a wrapped error (FR-11).
func TestAppServerProcess_Compact_RPCError(t *testing.T) {
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	go func() {
		dec := json.NewDecoder(serverRead)
		for {
			var msg struct {
				JSONRPC string  `json:"jsonrpc"`
				ID      *int64  `json:"id,omitempty"`
				Method  string  `json:"method"`
			}
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.ID == nil {
				continue
			}
			switch msg.Method {
			case "thread/compact/start":
				reply := fmt.Sprintf(
					`{"jsonrpc":"2.0","id":%d,"error":{"code":-32001,"message":"compaction not supported"}}`,
					*msg.ID,
				)
				fmt.Fprintln(serverWrite, reply)
			default:
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			}
		}
	}()

	proc := buildProcessFromPipePair(t, clientWrite, clientRead)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := proc.Compact(ctx, "thread-rpcerr")
	if err == nil {
		t.Fatal("expected error from Compact when RPC fails, got nil")
	}
	if !strings.Contains(err.Error(), "compact") {
		t.Errorf("error should mention compact; got: %v", err)
	}
}

// TestAppServerProcess_Compact_ContextCancelled verifies that Compact honours context
// cancellation while waiting for turn/completed.
func TestAppServerProcess_Compact_ContextCancelled(t *testing.T) {
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	go func() {
		dec := json.NewDecoder(serverRead)
		for {
			var msg struct {
				JSONRPC string  `json:"jsonrpc"`
				ID      *int64  `json:"id,omitempty"`
				Method  string  `json:"method"`
			}
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.ID == nil {
				continue
			}
			switch msg.Method {
			case "thread/compact/start":
				// Return {} but never emit turn/completed → Compact blocks until ctx.
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			default:
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			}
		}
	}()

	proc := buildProcessFromPipePair(t, clientWrite, clientRead)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := proc.Compact(ctx, "thread-ctx-cancel")
	if err == nil {
		t.Fatal("expected context error from Compact, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("expected context cancellation/deadline error, got: %v", err)
	}
}

// TestAppServerProcess_Compact_TokenUsageUpdatedDuringCompaction verifies that
// thread/tokenUsage/updated notifications received during compaction are applied to
// the token map (ADR-015: pass-through, do not suppress).
func TestAppServerProcess_Compact_TokenUsageUpdatedDuringCompaction(t *testing.T) {
	serverRead, clientWrite := io.Pipe()
	clientRead, serverWrite := io.Pipe()

	go func() {
		dec := json.NewDecoder(serverRead)
		for {
			var msg struct {
				JSONRPC string  `json:"jsonrpc"`
				ID      *int64  `json:"id,omitempty"`
				Method  string  `json:"method"`
			}
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.ID == nil {
				continue
			}
			switch msg.Method {
			case "thread/compact/start":
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
				go func() {
					time.Sleep(5 * time.Millisecond)
					// Emit a tokenUsage notification before turn/completed.
					usageParams, _ := json.Marshal(TokenUsageNotification{
						ThreadID: "thread-compact-tok",
						Usage:    TokenUsage{InputTokens: 99_999},
					})
					usageNotif, _ := json.Marshal(JSONRPCNotification{
						JSONRPC: "2.0",
						Method:  MethodTokenUsageUpdated,
						Params:  usageParams,
					})
					fmt.Fprintln(serverWrite, string(usageNotif))

					time.Sleep(5 * time.Millisecond)
					// Then emit turn/completed to unblock Compact.
					completed, _ := json.Marshal(TurnCompletedNotification{
						ThreadID: "thread-compact-tok",
						Turn:     Turn{ID: "turn-ct", Status: TurnStatusCompleted},
					})
					notif, _ := json.Marshal(JSONRPCNotification{
						JSONRPC: "2.0",
						Method:  MethodTurnCompleted,
						Params:  completed,
					})
					fmt.Fprintln(serverWrite, string(notif))
				}()
			default:
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
			}
		}
	}()

	proc := buildProcessFromPipePair(t, clientWrite, clientRead)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := proc.Compact(ctx, "thread-compact-tok"); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// The token usage update emitted during compaction must have been applied.
	got, ok := proc.TokenUsage("thread-compact-tok")
	if !ok {
		t.Fatal("TokenUsage: expected ok=true after compaction token notification")
	}
	if got.InputTokens != 99_999 {
		t.Errorf("InputTokens after compaction: got %d, want 99_999", got.InputTokens)
	}
}
