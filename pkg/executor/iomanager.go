package executor

import (
	"bufio"
	"io"
	"regexp"
	"time"

	"github.com/thebtf/aimux/pkg/executor/pipeline"
)

// IOManager handles streaming I/O from a process independently of lifecycle.
// It reads stdout line-by-line, accumulates output, strips ANSI, and matches
// completion patterns.
type IOManager struct {
	reader    io.Reader
	pattern   *regexp.Regexp // nil if no pattern or invalid regex
	buf       SafeBuffer
	patternCh chan struct{}          // signaled when pattern matches
	doneCh    chan struct{}          // closed when reader hits EOF
	onOutput  func(partial string)  // called with accumulated output after each line
}

// NewIOManager creates an IOManager that reads from the given reader.
// pattern is a regex string for completion detection (empty = no pattern matching).
// If pattern is invalid regex, pattern matching is silently disabled.
// onOutput is an optional callback invoked with accumulated content after each line.
func NewIOManager(stdout io.Reader, pattern string, onOutput ...func(string)) *IOManager {
	iom := &IOManager{
		reader:    stdout,
		patternCh: make(chan struct{}, 1),
		doneCh:    make(chan struct{}),
	}
	if len(onOutput) > 0 && onOutput[0] != nil {
		iom.onOutput = onOutput[0]
	}
	if pattern != "" {
		if re, err := regexp.Compile(pattern); err == nil {
			iom.pattern = re
		}
	}
	return iom
}

// StreamLines starts a goroutine that reads line-by-line from the reader.
// Each line is stripped of ANSI escape sequences, then appended to the internal buffer.
// After each line, if a pattern is set, checks if accumulated content matches.
// When pattern matches, signals PatternMatched channel (non-blocking, at most once).
// When reader returns EOF or error, the Done channel is closed.
func (iom *IOManager) StreamLines() {
	go func() {
		scanner := bufio.NewScanner(iom.reader)
		// Increase scanner buffer to 1MB to handle CLIs that produce long lines
		// (e.g. base64 content, large JSON). Default 64KB is too small.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := pipeline.StripANSI(scanner.Text())
			iom.buf.Write([]byte(line + "\n"))
			if iom.onOutput != nil {
				// Send only the new line, not the full buffer — avoids O(n²)
				iom.onOutput(line)
			}
			if iom.pattern != nil && iom.pattern.MatchString(line) {
				// Match against the new line only — avoids O(n²) regex rescanning
				select {
				case iom.patternCh <- struct{}{}:
				default:
				}
			}
		}
		close(iom.doneCh)
	}()
}

// PatternMatched returns a channel that signals when the completion pattern is found.
// Returns nil channel if no pattern was configured (blocks forever in select = ignored).
func (iom *IOManager) PatternMatched() <-chan struct{} {
	if iom.pattern == nil {
		return nil
	}
	return iom.patternCh
}

// Done returns a channel that closes when the reader reaches EOF.
func (iom *IOManager) Done() <-chan struct{} {
	return iom.doneCh
}

// Collect returns all accumulated output as a string.
func (iom *IOManager) Collect() string {
	return iom.buf.String()
}

// Drain waits up to timeout for the reader to finish (EOF).
// Useful after killing a process to capture remaining buffered output.
func (iom *IOManager) Drain(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-iom.doneCh:
	case <-timer.C:
	}
}
