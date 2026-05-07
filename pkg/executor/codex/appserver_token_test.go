package codex

import (
	"encoding/json"
	"sync"
	"testing"
)

// TestAppServerProcess_TokenUsage_UpdatedByNotification verifies that
// thread/tokenUsage/updated notifications update the per-thread map (FR-12).
func TestAppServerProcess_TokenUsage_UpdatedByNotification(t *testing.T) {
	d := newFakeDialer(t)
	p := newTestProcess(t, d)

	usage := TokenUsage{
		InputTokens:       100_000,
		CachedInputTokens: 20_000,
		OutputTokens:      5_000,
	}
	params, _ := json.Marshal(TokenUsageNotification{
		ThreadID: "thread-tok-1",
		Usage:    usage,
	})
	raw := json.RawMessage(params)

	// Inject notification directly via handleNotification (no turn in-flight needed).
	p.handleNotification(raw, make(chan<- TurnCompletedNotification, 1), make(chan<- string, 1))

	// The notification is for MethodTokenUsageUpdated — wrap in a JSONRPCNotification.
	notifBytes, _ := json.Marshal(JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  MethodTokenUsageUpdated,
		Params:  raw,
	})
	p.handleNotification(notifBytes, make(chan<- TurnCompletedNotification, 1), make(chan<- string, 1))

	got, ok := p.TokenUsage("thread-tok-1")
	if !ok {
		t.Fatal("TokenUsage: expected ok=true, got false")
	}
	if got.InputTokens != 100_000 {
		t.Errorf("InputTokens: got %d, want 100_000", got.InputTokens)
	}
	if got.CachedInputTokens != 20_000 {
		t.Errorf("CachedInputTokens: got %d, want 20_000", got.CachedInputTokens)
	}
	if got.OutputTokens != 5_000 {
		t.Errorf("OutputTokens: got %d, want 5_000", got.OutputTokens)
	}
}

// TestAppServerProcess_TokenUsage_Unknown verifies that TokenUsage returns false
// for a thread that has never emitted a usage notification (FR-12).
func TestAppServerProcess_TokenUsage_Unknown(t *testing.T) {
	d := newFakeDialer(t)
	p := newTestProcess(t, d)

	_, ok := p.TokenUsage("no-such-thread")
	if ok {
		t.Error("TokenUsage: expected ok=false for unknown thread, got true")
	}
}

// TestAppServerProcess_TokenUsage_MultipleThreads verifies that usage is tracked
// per-thread independently — updating one does not affect another.
func TestAppServerProcess_TokenUsage_MultipleThreads(t *testing.T) {
	d := newFakeDialer(t)
	p := newTestProcess(t, d)

	for _, tc := range []struct {
		threadID string
		tokens   int64
	}{
		{"thread-A", 50_000},
		{"thread-B", 150_000},
	} {
		params, _ := json.Marshal(TokenUsageNotification{
			ThreadID: tc.threadID,
			Usage:    TokenUsage{InputTokens: tc.tokens},
		})
		notif, _ := json.Marshal(JSONRPCNotification{
			JSONRPC: "2.0",
			Method:  MethodTokenUsageUpdated,
			Params:  params,
		})
		p.handleNotification(notif, make(chan<- TurnCompletedNotification, 1), make(chan<- string, 1))
	}

	usageA, okA := p.TokenUsage("thread-A")
	if !okA || usageA.InputTokens != 50_000 {
		t.Errorf("thread-A: got (%v, %v), want (50_000, true)", usageA.InputTokens, okA)
	}

	usageB, okB := p.TokenUsage("thread-B")
	if !okB || usageB.InputTokens != 150_000 {
		t.Errorf("thread-B: got (%v, %v), want (150_000, true)", usageB.InputTokens, okB)
	}
}

// TestAppServerProcess_TokenUsage_ConcurrentReads verifies that concurrent reads and
// updates of the tokenUsage map do not race (race detector must be enabled).
func TestAppServerProcess_TokenUsage_ConcurrentReads(t *testing.T) {
	d := newFakeDialer(t)
	p := newTestProcess(t, d)

	const goroutines = 20
	const iterations = 50

	var wg sync.WaitGroup

	// Writers: inject token usage notifications concurrently.
	for g := 0; g < goroutines/2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				params, _ := json.Marshal(TokenUsageNotification{
					ThreadID: "shared-thread",
					Usage:    TokenUsage{InputTokens: int64(g*iterations + i)},
				})
				notif, _ := json.Marshal(JSONRPCNotification{
					JSONRPC: "2.0",
					Method:  MethodTokenUsageUpdated,
					Params:  params,
				})
				p.handleNotification(notif, make(chan<- TurnCompletedNotification, 1), make(chan<- string, 1))
			}
		}(g)
	}

	// Readers: call TokenUsage concurrently.
	for g := 0; g < goroutines/2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				p.TokenUsage("shared-thread")
			}
		}()
	}

	wg.Wait()
	// No race detector failure = test passes.
}

// TestAppServerProcess_TokenUsage_Overwrite verifies that a second notification for the
// same thread replaces the previous value (usage is always the latest snapshot).
func TestAppServerProcess_TokenUsage_Overwrite(t *testing.T) {
	d := newFakeDialer(t)
	p := newTestProcess(t, d)

	injectUsage := func(tokens int64) {
		params, _ := json.Marshal(TokenUsageNotification{
			ThreadID: "thread-overwrite",
			Usage:    TokenUsage{InputTokens: tokens},
		})
		notif, _ := json.Marshal(JSONRPCNotification{
			JSONRPC: "2.0",
			Method:  MethodTokenUsageUpdated,
			Params:  params,
		})
		p.handleNotification(notif, make(chan<- TurnCompletedNotification, 1), make(chan<- string, 1))
	}

	injectUsage(10_000)
	injectUsage(200_000)

	got, ok := p.TokenUsage("thread-overwrite")
	if !ok {
		t.Fatal("TokenUsage: expected ok=true")
	}
	if got.InputTokens != 200_000 {
		t.Errorf("InputTokens after overwrite: got %d, want 200_000", got.InputTokens)
	}
}
