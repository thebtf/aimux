package executor_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
)

// TestIOManager_StreamLinesReadsAll verifies that all lines written to the reader
// are accumulated in the buffer after the reader reaches EOF.
func TestIOManager_StreamLinesReadsAll(t *testing.T) {
	lines := "line1\nline2\nline3\nline4\nline5\n"
	iom := executor.NewIOManager(strings.NewReader(lines), "")
	iom.StreamLines()

	select {
	case <-iom.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Done()")
	}

	got := iom.Collect()
	for _, want := range []string{"line1", "line2", "line3", "line4", "line5"} {
		if !strings.Contains(got, want) {
			t.Errorf("Collect() missing %q; got:\n%s", want, got)
		}
	}
}

// TestIOManager_PatternMatchSignals verifies that PatternMatched fires when the
// completion pattern appears in accumulated output.
func TestIOManager_PatternMatchSignals(t *testing.T) {
	input := "line1\nline2\ncompleted\nline4\n"
	iom := executor.NewIOManager(strings.NewReader(input), "completed")
	iom.StreamLines()

	select {
	case <-iom.PatternMatched():
		// success: pattern was detected
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PatternMatched(); pattern was never detected")
	}

	// Ensure the content that triggered the match is actually there.
	<-iom.Done()
	if !strings.Contains(iom.Collect(), "completed") {
		t.Error("expected 'completed' in Collect() output")
	}
}

// TestIOManager_CollectReturnsAccumulated verifies Collect() returns all fed lines
// joined with newlines.
func TestIOManager_CollectReturnsAccumulated(t *testing.T) {
	input := "alpha\nbeta\ngamma\n"
	iom := executor.NewIOManager(strings.NewReader(input), "")
	iom.StreamLines()

	select {
	case <-iom.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Done()")
	}

	got := iom.Collect()
	for _, line := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(got, line) {
			t.Errorf("Collect() missing line %q; got:\n%s", line, got)
		}
	}

	// Verify the lines are separated by newlines (not concatenated).
	if !strings.Contains(got, "alpha\n") || !strings.Contains(got, "beta\n") || !strings.Contains(got, "gamma\n") {
		t.Errorf("lines should be newline-terminated in Collect(); got:\n%s", got)
	}
}

// TestIOManager_DrainWaitsForEOF verifies that Drain returns after the writer closes
// and that content written before close is captured.
func TestIOManager_DrainWaitsForEOF(t *testing.T) {
	pr, pw := io.Pipe()
	iom := executor.NewIOManager(pr, "")
	iom.StreamLines()

	go func() {
		time.Sleep(100 * time.Millisecond)
		pw.Write([]byte("slow line\n")) //nolint:errcheck
		pw.Close()
	}()

	start := time.Now()
	iom.Drain(1 * time.Second)
	elapsed := time.Since(start)

	if elapsed > 900*time.Millisecond {
		t.Errorf("Drain took too long (%v); expected ~100ms", elapsed)
	}

	if !strings.Contains(iom.Collect(), "slow line") {
		t.Errorf("expected 'slow line' in Collect() after Drain; got:\n%s", iom.Collect())
	}
}

// TestIOManager_ANSIStripped verifies that ANSI escape codes are stripped per line
// before accumulation.
func TestIOManager_ANSIStripped(t *testing.T) {
	// \033[31m = red foreground, \033[0m = reset
	input := "\033[31mred\033[0m\nplain\n"
	iom := executor.NewIOManager(strings.NewReader(input), "")
	iom.StreamLines()

	select {
	case <-iom.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Done()")
	}

	got := iom.Collect()
	if strings.Contains(got, "\033[") {
		t.Errorf("Collect() contains ANSI escape codes; got:\n%q", got)
	}
	if !strings.Contains(got, "red") {
		t.Errorf("Collect() should contain 'red' after stripping ANSI; got:\n%q", got)
	}
	if !strings.Contains(got, "plain") {
		t.Errorf("Collect() should contain 'plain'; got:\n%q", got)
	}
}

// TestIOManager_NoPatternNilChannel verifies that PatternMatched returns nil when
// no pattern is configured, which means it will block forever in a select (= ignored).
func TestIOManager_NoPatternNilChannel(t *testing.T) {
	iom := executor.NewIOManager(strings.NewReader("some data\n"), "")
	ch := iom.PatternMatched()
	if ch != nil {
		t.Errorf("PatternMatched() should return nil for empty pattern; got %v", ch)
	}
}
