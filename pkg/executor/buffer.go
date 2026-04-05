package executor

import (
	"bytes"
	"io"
	"os"
	"sync"
)

// OutputBuffer accumulates CLI output with automatic disk spillover
// for large outputs. Threshold configurable (default 10MB in-memory).
type OutputBuffer struct {
	buf       bytes.Buffer
	file      *os.File
	threshold int
	spilled   bool
	mu        sync.Mutex
}

// DefaultBufferThreshold is 10MB — outputs larger than this spill to disk.
const DefaultBufferThreshold = 10 * 1024 * 1024

// NewOutputBuffer creates a buffer with the given threshold.
// If threshold is 0, uses DefaultBufferThreshold.
func NewOutputBuffer(threshold int) *OutputBuffer {
	if threshold <= 0 {
		threshold = DefaultBufferThreshold
	}
	return &OutputBuffer{threshold: threshold}
}

// Write implements io.Writer. Writes to memory buffer until threshold,
// then spills to a temp file.
func (b *OutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.spilled {
		return b.file.Write(p)
	}

	if b.buf.Len()+len(p) > b.threshold {
		// Spill to disk
		f, err := os.CreateTemp("", "aimux-output-*")
		if err != nil {
			// Can't create temp file — keep in memory
			return b.buf.Write(p)
		}

		// Copy existing buffer to file
		if _, err := io.Copy(f, &b.buf); err != nil {
			f.Close()
			os.Remove(f.Name())
			return b.buf.Write(p)
		}

		b.file = f
		b.spilled = true
		b.buf.Reset()

		return f.Write(p)
	}

	return b.buf.Write(p)
}

// String returns the full buffer content as a string.
func (b *OutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.spilled {
		return b.buf.String()
	}

	// Read from file
	b.file.Seek(0, 0)
	data, err := io.ReadAll(b.file)
	if err != nil {
		return ""
	}
	return string(data)
}

// Len returns the total bytes written.
func (b *OutputBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.spilled {
		info, err := b.file.Stat()
		if err != nil {
			return 0
		}
		return int(info.Size())
	}
	return b.buf.Len()
}

// Close cleans up the temp file if one was created.
func (b *OutputBuffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.file != nil {
		name := b.file.Name()
		b.file.Close()
		os.Remove(name)
	}
	return nil
}

// Spilled returns true if the buffer overflowed to disk.
func (b *OutputBuffer) Spilled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spilled
}
