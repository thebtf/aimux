package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/loom"
	"github.com/thebtf/aimux/pkg/executor/runtime"
)

// compactTracker wraps AppServerProcess and records whether Compact was called.
// Used to verify the worker calls (or skips) compaction at the right boundary.
type compactTracker struct {
	mu           sync.Mutex
	compactCalls []string // threadIDs passed to Compact
}

func (ct *compactTracker) record(threadID string) {
	ct.mu.Lock()
	ct.compactCalls = append(ct.compactCalls, threadID)
	ct.mu.Unlock()
}

func (ct *compactTracker) calls() []string {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	out := make([]string, len(ct.compactCalls))
	copy(out, ct.compactCalls)
	return out
}

// seedTokens injects token usage directly into proc.tokenUsage via handleNotification.
// This bypasses the async notification path so tokens are visible before the next Execute.
func seedTokens(proc *AppServerProcess, threadID string, inputTokens int64) {
	usageParams, _ := json.Marshal(TokenUsageNotification{
		ThreadID: threadID,
		Usage:    TokenUsage{InputTokens: inputTokens},
	})
	notifBytes, _ := json.Marshal(JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  MethodTokenUsageUpdated,
		Params:  usageParams,
	})
	proc.handleNotification(notifBytes, make(chan<- TurnCompletedNotification, 1), make(chan<- string, 1))
}

// buildCompactionWorker builds a CodexWorker with a custom CompactionConfig,
// backed by a server that supports thread/compact/start.
// The server records compact calls via compactTracker and emits turn/completed.
// Returns the worker, the AppServerProcess (for token pre-seeding), and the tracker.
func buildCompactionWorker(
	t *testing.T,
	cfg CompactionConfig,
) (*CodexWorker, *AppServerProcess, *compactTracker) {
	t.Helper()
	tracker := &compactTracker{}

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
				continue // notification from client
			}
			switch msg.Method {
			case "thread/start":
				var p ThreadStartParams
				_ = json.Unmarshal(msg.Params, &p)
				resp, _ := json.Marshal(ThreadStartResponse{
					Thread: Thread{ID: "thread-compaction", CWD: "/work"},
				})
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, *msg.ID, resp)
				fmt.Fprintln(serverWrite, reply)

			case "turn/start":
				var p TurnStartParams
				_ = json.Unmarshal(msg.Params, &p)
				resp, _ := json.Marshal(TurnStartResponse{
					Turn: Turn{ID: "turn-compaction", Status: TurnStatusRunning},
				})
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, *msg.ID, resp)
				fmt.Fprintln(serverWrite, reply)
				// Emit turn/completed asynchronously.
				tid := p.ThreadID
				go func() {
					time.Sleep(5 * time.Millisecond)
					completed, _ := json.Marshal(TurnCompletedNotification{
						ThreadID: tid,
						Turn:     Turn{ID: "turn-compaction", Status: TurnStatusCompleted},
					})
					notif, _ := json.Marshal(JSONRPCNotification{
						JSONRPC: "2.0",
						Method:  MethodTurnCompleted,
						Params:  completed,
					})
					fmt.Fprintln(serverWrite, string(notif))
				}()

			case "thread/compact/start":
				var p ThreadCompactStartParams
				_ = json.Unmarshal(msg.Params, &p)
				tracker.record(p.ThreadID)
				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, *msg.ID)
				fmt.Fprintln(serverWrite, reply)
				// Emit turn/completed for the compaction turn.
				tid := p.ThreadID
				go func() {
					time.Sleep(5 * time.Millisecond)
					completed, _ := json.Marshal(TurnCompletedNotification{
						ThreadID: tid,
						Turn:     Turn{ID: "turn-compact-resp", Status: TurnStatusCompleted},
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

	proc := &AppServerProcess{
		codexPath: "/fake/codex",
		profile:   runtime.CLIRuntimeProfile{},
		state:     AppServerStateReady,
	}
	client := NewJSONLClient(clientWrite, clientRead)
	readCtx, cancel := context.WithCancel(context.Background())
	go client.Start(readCtx)
	proc.mu.Lock()
	proc.client = client
	proc.cancelReadLoop = cancel
	proc.mu.Unlock()
	t.Cleanup(func() {
		cancel()
		clientWrite.Close()
	})

	pool := newTestPool(t, nil)
	pool.mu.Lock()
	pool.entries["proj-compact"] = readyEntry(proc)
	pool.mu.Unlock()

	sink := &fakeProgressSink{}
	getter := &fakeTaskGetter{tasks: make(map[string]*loom.Task)}

	w, err := NewCodexWorker(CodexWorkerConfig{
		Pool:       pool.CodexPool,
		Loom:       sink,
		LoomGet:    getter,
		Model:      "gpt-5-test",
		Compaction: cfg,
	})
	if err != nil {
		t.Fatalf("NewCodexWorker: %v", err)
	}

	return w, proc, tracker
}

// --- Threshold boundary tests ---

// TestMaybeCompact_BelowThreshold verifies that 181_879 input tokens (threshold - 1)
// does NOT trigger compaction (FR-11).
func TestMaybeCompact_BelowThreshold(t *testing.T) {
	cfg := CompactionConfig{Threshold: 181_880, MinTurnsBetween: 5}
	w, proc, tracker := buildCompactionWorker(t, cfg)

	// Pre-seed tokens below threshold so maybeCompact sees them on first Execute.
	seedTokens(proc, "thread-compaction", 181_879)

	task := buildTask(JobClassTask, "below threshold", "proj-compact", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := w.Execute(ctx, task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if calls := tracker.calls(); len(calls) != 0 {
		t.Errorf("Compact must not be called at 181_879 tokens; got %d call(s)", len(calls))
	}
}

// TestMaybeCompact_AtThreshold verifies that 181_880 input tokens (exactly threshold)
// does NOT trigger compaction — condition is strictly greater-than (FR-11).
func TestMaybeCompact_AtThreshold(t *testing.T) {
	cfg := CompactionConfig{Threshold: 181_880, MinTurnsBetween: 5}
	w, proc, tracker := buildCompactionWorker(t, cfg)

	seedTokens(proc, "thread-compaction", 181_880)

	task := buildTask(JobClassTask, "at threshold", "proj-compact", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := w.Execute(ctx, task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if calls := tracker.calls(); len(calls) != 0 {
		t.Errorf("Compact must not be called at exactly 181_880 tokens; got %d call(s)", len(calls))
	}
}

// TestMaybeCompact_AboveThreshold verifies that 181_881 input tokens (threshold + 1)
// triggers compaction (FR-11).
func TestMaybeCompact_AboveThreshold(t *testing.T) {
	cfg := CompactionConfig{Threshold: 181_880, MinTurnsBetween: 5}
	w, proc, tracker := buildCompactionWorker(t, cfg)

	// Pre-seed high token count so maybeCompact sees it on first Execute.
	seedTokens(proc, "thread-compaction", 181_881)

	task := buildTask(JobClassTask, "above threshold", "proj-compact", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := w.Execute(ctx, task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if calls := tracker.calls(); len(calls) != 1 {
		t.Errorf("Compact must be called once at 181_881 tokens; got %d call(s)", len(calls))
	}
}

// TestMaybeCompact_CompactionCountIncrement verifies that Execute increments
// CompactionCount in the result metadata when compaction is triggered (FR-12).
func TestMaybeCompact_CompactionCountIncrement(t *testing.T) {
	cfg := CompactionConfig{Threshold: 181_880, MinTurnsBetween: 5}
	w, proc, _ := buildCompactionWorker(t, cfg)

	seedTokens(proc, "thread-compaction", 200_000)

	task := buildTask(JobClassTask, "compact and count", "proj-compact", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := w.Execute(ctx, task)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// CompactionCount must be reflected in the returned metadata.
	// codeTaskMetaToMap JSON-round-trips the struct so numbers arrive as float64.
	countRaw, ok := result.Metadata["compaction_count"]
	if !ok {
		t.Fatal("compaction_count missing from result metadata")
	}
	count, _ := countRaw.(float64)
	if int(count) != 1 {
		t.Errorf("compaction_count: got %v, want 1", countRaw)
	}
}

// TestMaybeCompact_ThrottlePreventsBackToBack verifies that compaction is not triggered
// again within MinTurnsBetween turns of the previous compaction (FR-11).
func TestMaybeCompact_ThrottlePreventsBackToBack(t *testing.T) {
	// MinTurnsBetween=5: second Execute at turn 1 must not compact.
	cfg := CompactionConfig{Threshold: 181_880, MinTurnsBetween: 5}
	w, proc, tracker := buildCompactionWorker(t, cfg)

	// Pre-seed tokens above threshold.
	seedTokens(proc, "thread-compaction", 200_000)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Turn 0: above threshold → compact fires.
	task := buildTask(JobClassTask, "turn 0", "proj-compact", nil)
	if _, err := w.Execute(ctx, task); err != nil {
		t.Fatalf("Execute (turn 0): %v", err)
	}
	if calls := tracker.calls(); len(calls) != 1 {
		t.Fatalf("expected 1 compact after turn 0, got %d", len(calls))
	}

	// Turns 1-3: still above threshold but within throttle window — no compact.
	for i := 1; i <= 3; i++ {
		task2 := buildTask(JobClassTask, fmt.Sprintf("turn %d", i), "proj-compact", nil)
		if _, err := w.Execute(ctx, task2); err != nil {
			t.Fatalf("Execute (turn %d): %v", i, err)
		}
	}

	if calls := tracker.calls(); len(calls) != 1 {
		t.Errorf("Compact must not fire within MinTurnsBetween; got %d total call(s)", len(calls))
	}
}

// TestMaybeCompact_ThrottleLiftsAfterMinTurns verifies that compaction fires again
// once MinTurnsBetween turns have elapsed since the last compaction (FR-11).
func TestMaybeCompact_ThrottleLiftsAfterMinTurns(t *testing.T) {
	cfg := CompactionConfig{Threshold: 181_880, MinTurnsBetween: 3}
	w, proc, tracker := buildCompactionWorker(t, cfg)

	seedTokens(proc, "thread-compaction", 200_000)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run 5 turns (turns 0-4). Compact fires at turn 0.
	// At turn 3 (3 turns after turn 0), throttle lifts → compact fires again.
	for i := 0; i < 5; i++ {
		task := buildTask(JobClassTask, fmt.Sprintf("turn %d", i), "proj-compact", nil)
		if _, err := w.Execute(ctx, task); err != nil {
			t.Fatalf("Execute (turn %d): %v", i, err)
		}
	}

	calls := tracker.calls()
	// Expected: compact at turn 0 and again at turn 3 (0 + MinTurnsBetween).
	if len(calls) != 2 {
		t.Errorf("expected 2 compaction calls (turn 0 + turn 3); got %d: %v", len(calls), calls)
	}
}

// TestMaybeCompact_NoDataNoCompact verifies that when no tokenUsage data is available
// (proc.TokenUsage returns false), no compaction is triggered (FR-11).
func TestMaybeCompact_NoDataNoCompact(t *testing.T) {
	cfg := CompactionConfig{Threshold: 181_880, MinTurnsBetween: 5}
	// Do NOT pre-seed any token data — proc.TokenUsage returns false.
	w, _, tracker := buildCompactionWorker(t, cfg)

	task := buildTask(JobClassTask, "no token data", "proj-compact", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := w.Execute(ctx, task); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if calls := tracker.calls(); len(calls) != 0 {
		t.Errorf("Compact must not be called when no token data exists; got %d call(s)", len(calls))
	}
}

// TestDefaultCompactionConfig verifies the production defaults are correct (FR-11).
func TestDefaultCompactionConfig(t *testing.T) {
	cfg := defaultCompactionConfig()
	if cfg.Threshold != 181_880 {
		t.Errorf("Threshold: got %d, want 181_880", cfg.Threshold)
	}
	if cfg.MinTurnsBetween != 5 {
		t.Errorf("MinTurnsBetween: got %d, want 5", cfg.MinTurnsBetween)
	}
}
