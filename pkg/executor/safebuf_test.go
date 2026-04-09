package executor_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/thebtf/aimux/pkg/executor"
)

// TestSafeBuffer_ConcurrentWrites launches 10 goroutines that each write a distinct
// string concurrently. After all writes complete, each string must appear in the output.
// This test fails if Write is not goroutine-safe (data race) or if any write is lost.
func TestSafeBuffer_ConcurrentWrites(t *testing.T) {
	var sb executor.SafeBuffer
	const workers = 10
	const token = "worker-%d\n"

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			msg := fmt.Sprintf(token, i)
			n, err := sb.Write([]byte(msg))
			if err != nil {
				t.Errorf("Write error from worker %d: %v", i, err)
			}
			if n != len(msg) {
				t.Errorf("worker %d: wrote %d bytes, want %d", i, n, len(msg))
			}
		}()
	}
	wg.Wait()

	result := sb.String()
	for i := 0; i < workers; i++ {
		expected := fmt.Sprintf("worker-%d", i)
		if !strings.Contains(result, expected) {
			t.Errorf("output missing %q; full output: %q", expected, result)
		}
	}
}

// TestSafeBuffer_StringThreadSafe writes from one goroutine while another calls String()
// repeatedly. The test verifies that the final String() result equals the full written
// content, proving that concurrent String() calls do not corrupt or miss data.
func TestSafeBuffer_StringThreadSafe(t *testing.T) {
	var sb executor.SafeBuffer
	const payload = "hello-concurrent-world"

	written := make(chan struct{})
	go func() {
		_, _ = sb.Write([]byte(payload))
		close(written)
	}()

	// Read concurrently until write is confirmed done, then check final value.
	for {
		select {
		case <-written:
			got := sb.String()
			if got != payload {
				t.Errorf("String() = %q, want %q", got, payload)
			}
			return
		default:
			_ = sb.String() // exercise concurrent reads, must not panic
		}
	}
}

// TestSafeBuffer_ResetClears writes data, calls Reset, then verifies the buffer is empty.
// This test fails if Reset is a no-op or if the function body returns default values
// without actually clearing (anti-stub: Len and String must return 0 and "" respectively).
func TestSafeBuffer_ResetClears(t *testing.T) {
	var sb executor.SafeBuffer

	_, err := sb.Write([]byte("some data"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if sb.Len() == 0 {
		t.Fatal("Len() == 0 before Reset — Write had no effect")
	}

	sb.Reset()

	if l := sb.Len(); l != 0 {
		t.Errorf("Len() = %d after Reset, want 0", l)
	}
	if s := sb.String(); s != "" {
		t.Errorf("String() = %q after Reset, want empty string", s)
	}
}
