package session_test

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/session"
)

// mockPipe creates a connected (reader, writer) pair using io.Pipe and adapts
// them to the io.WriteCloser / io.ReadCloser interfaces expected by session.New.
func mockPipe() (io.WriteCloser, io.ReadCloser) {
	pr, pw := io.Pipe()
	return pw, pr
}

// TestBaseSession_Send verifies that prompt is written to stdin and the
// response written to stdout is collected correctly.
func TestBaseSession_Send(t *testing.T) {
	stdinW, stdinR := mockPipe() // session writes prompts here
	stdoutW, stdoutR := mockPipe()

	// The session's stdin write end is stdinW; we read from stdinR to verify.
	// The session's stdout read end is stdoutR; we write to stdoutW to simulate output.
	sess := session.New("test-id", stdinW, stdoutR, 100*time.Millisecond, nil, nil, "")

	// In a goroutine: read the prompt the session writes, then echo a response.
	go func() {
		buf := make([]byte, 256)
		n, _ := stdinR.Read(buf)
		prompt := strings.TrimSpace(string(buf[:n]))
		response := "response to: " + prompt + "\n"
		stdoutW.Write([]byte(response)) //nolint:errcheck
		// Let inactivity timer fire naturally; no explicit close.
	}()

	result, err := sess.Send(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(result.Content, "response to: hello") {
		t.Errorf("unexpected content: %q", result.Content)
	}

	sess.Close()
}

// TestBaseSession_InactivityTimeout verifies that Send returns after the
// inactivity period even when stdout is not closed.
func TestBaseSession_InactivityTimeout(t *testing.T) {
	stdinW, stdinR := mockPipe()
	_, stdoutR := mockPipe() // writer intentionally not used — simulates silence

	sess := session.New("timeout-id", stdinW, stdoutR, 50*time.Millisecond, nil, nil, "")

	// Drain stdinR in background so Write doesn't block.
	go io.Copy(io.Discard, stdinR) //nolint:errcheck

	start := time.Now()
	result, err := sess.Send(context.Background(), "ping")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Should return with Partial=true after the inactivity timeout.
	if !result.Partial {
		t.Error("expected Partial=true on inactivity timeout")
	}
	// Should not have taken much longer than the timeout.
	if elapsed > 500*time.Millisecond {
		t.Errorf("Send took too long: %v", elapsed)
	}

	sess.Close()
}

// TestBaseSession_Close verifies that Close is idempotent and cleans up
// without blocking.
func TestBaseSession_Close(t *testing.T) {
	stdinW, stdinR := mockPipe()
	_, stdoutR := mockPipe()

	sess := session.New("close-id", stdinW, stdoutR, 50*time.Millisecond, nil, nil, "")

	// Drain stdinR so Write doesn't block.
	go io.Copy(io.Discard, stdinR) //nolint:errcheck

	done := make(chan struct{})
	go func() {
		defer close(done)
		sess.Close()
		sess.Close() // second call must be a no-op
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return within 2s")
	}
}

// TestBaseSession_ConcurrentSend verifies that mu serialises Send calls —
// concurrent callers must not interleave their stdin writes or response reads.
func TestBaseSession_ConcurrentSend(t *testing.T) {
	stdinW, stdinR := mockPipe()
	stdoutW, stdoutR := mockPipe()

	const timeout = 50 * time.Millisecond
	sess := session.New("concurrent-id", stdinW, stdoutR, timeout, nil, nil, "")

	// Echo goroutine: for each line received on stdinR, write a response to stdoutW.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdinR.Read(buf)
			if err != nil {
				return
			}
			line := strings.TrimSpace(string(buf[:n]))
			if line == "" {
				continue
			}
			stdoutW.Write([]byte("ok:" + line + "\n")) //nolint:errcheck
		}
	}()

	var wg sync.WaitGroup
	errors := make([]error, 3)
	for i := range 3 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := sess.Send(context.Background(), "msg")
			errors[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errors {
		if err != nil {
			t.Errorf("goroutine %d Send error: %v", i, err)
		}
	}

	sess.Close()
}

// TestBaseSession_CompletionPattern verifies that a sentinel pattern causes
// Send() to return as soon as the matching line appears in stdout, without
// waiting for the inactivity timeout (FR-3, Q-CLAR-1 line-anchored semantics).
func TestBaseSession_CompletionPattern(t *testing.T) {
	stdinW, stdinR := mockPipe()
	stdoutW, stdoutR := mockPipe()

	// Use a long inactivity timeout so the test would hang if sentinel detection
	// does not fire — verifying the implementation is non-stub.
	const longTimeout = 10 * time.Second
	sess := session.New("sentinel-id", stdinW, stdoutR, longTimeout, nil, nil, `^DONE$`)

	// Drain stdinR so Write doesn't block.
	go io.Copy(io.Discard, stdinR) //nolint:errcheck

	// Write non-sentinel lines then the sentinel line.
	go func() {
		stdoutW.Write([]byte("line one\n"))  //nolint:errcheck
		stdoutW.Write([]byte("line two\n"))  //nolint:errcheck
		stdoutW.Write([]byte("DONE\n"))       //nolint:errcheck
		// Deliberately do NOT close stdoutW — if Send returns, it's due to the sentinel,
		// not EOF. If sentinel detection is removed, Send would block for longTimeout.
	}()

	start := time.Now()
	result, err := sess.Send(context.Background(), "prompt")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Should return well within the long inactivity timeout.
	if elapsed >= 2*time.Second {
		t.Errorf("Send did not return promptly on sentinel match: elapsed=%v", elapsed)
	}
	// Content should contain the lines written before (and including) the sentinel.
	if !strings.Contains(result.Content, "line one") {
		t.Errorf("missing pre-sentinel content: %q", result.Content)
	}
	if !strings.Contains(result.Content, "DONE") {
		t.Errorf("missing sentinel line in content: %q", result.Content)
	}

	sess.Close()
}

// TestBaseSession_CompletionPattern_NoFalsePositive verifies that lines not
// matching the sentinel pattern do NOT prematurely terminate Send() (FR-3 EC-1).
func TestBaseSession_CompletionPattern_NoFalsePositive(t *testing.T) {
	stdinW, stdinR := mockPipe()
	stdoutW, stdoutR := mockPipe()

	const shortTimeout = 80 * time.Millisecond
	// Pattern that matches "SENTINEL" but nothing else.
	sess := session.New("no-fp-id", stdinW, stdoutR, shortTimeout, nil, nil, `^SENTINEL$`)

	go io.Copy(io.Discard, stdinR) //nolint:errcheck

	go func() {
		// Write lines that should NOT trigger the sentinel.
		stdoutW.Write([]byte("not-sentinel\n")) //nolint:errcheck
		stdoutW.Write([]byte("DONE\n"))          //nolint:errcheck
		// No sentinel — Send should return via inactivity timeout.
	}()

	start := time.Now()
	result, err := sess.Send(context.Background(), "probe")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !result.Partial {
		t.Error("expected Partial=true (inactivity timeout, no sentinel match)")
	}
	// Should return near the inactivity timeout, not immediately.
	if elapsed < shortTimeout/2 {
		t.Errorf("Send returned too quickly — possible false-positive sentinel: elapsed=%v", elapsed)
	}
	_ = result

	sess.Close()
}

// TestBaseSession_ContextCancel verifies that a cancelled context causes
// Send to return promptly with Partial=true.
func TestBaseSession_ContextCancel(t *testing.T) {
	stdinW, stdinR := mockPipe()
	_, stdoutR := mockPipe() // no output — simulates long-running silent process

	sess := session.New("cancel-id", stdinW, stdoutR, 10*time.Second, nil, nil, "")

	// Drain stdinR so Write doesn't block.
	go io.Copy(io.Discard, stdinR) //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	result, err := sess.Send(ctx, "slow")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !result.Partial {
		t.Error("expected Partial=true on context cancel")
	}
	// Should return quickly after cancel, not after the 10s inactivity timeout.
	if elapsed > 1*time.Second {
		t.Errorf("Send took too long after cancel: %v", elapsed)
	}

	sess.Close()
}
