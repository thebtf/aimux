package audit

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
)

// defaultBufferSize is the number of AuditEvents the internal channel can hold
// before Emit starts dropping events. 4096 gives ~4s headroom at 1000 events/s
// before the background writer would need to stall.
const defaultBufferSize = 4096

// AuditLog is the interface that any audit-log backend must satisfy. The single
// method Emit must be non-blocking: if the backend cannot accept the event
// immediately it must drop it and increment an internal counter rather than
// blocking the caller.
//
// Phase 5 (DispatchMiddleware) will inject a concrete AuditLog implementation
// via constructor injection. Packages that need to emit audit events in tests
// should use a fake that records events in a slice — do not depend on
// FileAuditLog directly.
type AuditLog interface {
	// Emit records an audit event. Emit must never block the caller.
	Emit(event AuditEvent)

	// Close flushes all buffered events to the backing store and releases
	// resources. After Close returns, further Emit calls have no effect.
	Close() error
}

// FileAuditLog is the production implementation of AuditLog. It writes one
// JSON line per event to a file opened with O_APPEND|O_CREATE and mode 0600.
// A background goroutine drains the internal channel; the caller-facing Emit
// method is always non-blocking.
//
// Use NewFileAuditLog for production. Use NewFileAuditLogWithBuffer in tests
// that need to control buffer size. Use PauseDrain / ResumeDrain in tests that
// need to exercise the drop path.
type FileAuditLog struct {
	ch      chan AuditEvent
	dropped atomic.Int64
	file    *os.File
	once    sync.Once
	done    chan struct{} // closed by the drain goroutine when it exits

	// drainMu is held by PauseDrain and acquired (RLock) inside the drain
	// goroutine before each write. This causes the drain goroutine to block
	// while PauseDrain holds the write lock, giving tests a way to fill the
	// channel and verify drop behaviour.
	drainMu sync.RWMutex
}

// NewFileAuditLog creates a FileAuditLog that writes to path.
// The file is opened with O_APPEND|O_CREATE and mode 0600 (NFR-11).
func NewFileAuditLog(path string) (*FileAuditLog, error) {
	return NewFileAuditLogWithBuffer(path, defaultBufferSize)
}

// NewFileAuditLogWithBuffer is like NewFileAuditLog but with a caller-specified
// channel capacity. Use in tests to exercise the drop-on-full-buffer path.
func NewFileAuditLogWithBuffer(path string, bufSize int) (*FileAuditLog, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("audit: open %s: %w", path, err)
	}

	al := &FileAuditLog{
		ch:   make(chan AuditEvent, bufSize),
		file: f,
		done: make(chan struct{}),
	}

	go al.drain()
	return al, nil
}

// Emit enqueues event for async write. If the internal channel is full the
// event is dropped, the dropped counter is incremented, and a warning is
// logged. Emit is safe for concurrent use and never blocks the caller.
func (al *FileAuditLog) Emit(event AuditEvent) {
	select {
	case al.ch <- event:
	default:
		al.dropped.Add(1)
		log.Printf("audit: channel full — event dropped (total dropped: %d)", al.dropped.Load())
	}
}

// Close signals the drain goroutine to stop, waits for it to flush all
// buffered events, then closes the underlying file. After Close returns no
// further events are written.
func (al *FileAuditLog) Close() error {
	var closeErr error
	al.once.Do(func() {
		close(al.ch)  // signal drain goroutine: no more events
		<-al.done     // wait for drain goroutine to finish
		closeErr = al.file.Close()
	})
	return closeErr
}

// DroppedCount returns the number of events dropped because the channel was
// full. Exposed for tests; callers in production should prefer metrics.
func (al *FileAuditLog) DroppedCount() int64 {
	return al.dropped.Load()
}

// PauseDrain blocks the background drain goroutine until ResumeDrain is called.
// For test use only — never call in production code paths.
//
// PauseDrain acquires drainMu as a writer. The drain goroutine acquires it as a
// reader before each write, so it blocks until ResumeDrain releases the writer lock.
func (al *FileAuditLog) PauseDrain() {
	al.drainMu.Lock()
}

// ResumeDrain unblocks a previously paused drain goroutine. Must be called
// exactly once after PauseDrain. For test use only.
func (al *FileAuditLog) ResumeDrain() {
	al.drainMu.Unlock()
}

// drain is the background goroutine that reads from ch and writes JSONL lines.
// It exits when ch is closed and empty. It acquires drainMu as a reader before
// each write so that PauseDrain (which holds the write lock) can stall it.
func (al *FileAuditLog) drain() {
	defer close(al.done)

	enc := json.NewEncoder(al.file)

	for ev := range al.ch {
		// RLock: blocks if PauseDrain holds the write lock.
		al.drainMu.RLock()
		err := enc.Encode(ev)
		al.drainMu.RUnlock()

		if err != nil {
			log.Printf("audit: encode event: %v", err)
		}
	}
}
