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
		for scanner.Scan() {
			line := pipeline.StripANSI(scanner.Text())
			iom.buf.Write([]byte(line + "\n"))
			if iom.onOutput != nil {
				iom.onOutput(iom.buf.String())
			}
			if iom.pattern != nil && iom.pattern.MatchString(iom.buf.String()) {
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
	select {
	case <-iom.doneCh:
	case <-time.After(timeout):
	}
}
