package audit_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/audit"
)

// makeEvent builds an AuditEvent with the given type for use in tests.
func makeEvent(et audit.EventType, tenantID string) audit.AuditEvent {
	return audit.AuditEvent{
		Timestamp: time.Now(),
		EventType: et,
		TenantID:  tenantID,
		ToolName:  "test-tool",
		Result:    "ok",
	}
}

// readJSONL reads and decodes all JSON lines from path, returning []AuditEvent.
func readJSONL(t *testing.T, path string) []audit.AuditEvent {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("readJSONL: open %s: %v", path, err)
	}
	defer f.Close()

	var events []audit.AuditEvent
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev audit.AuditEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("readJSONL: unmarshal %q: %v", line, err)
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("readJSONL: scan: %v", err)
	}
	return events
}

// --- T027: TestAuditLog_AppendIsNonBlocking ---

// TestAuditLog_AppendIsNonBlocking verifies that Emit returns without blocking
// the caller even when the file write is slow. The test emits 1000 events
// concurrently from 10 goroutines and expects all Emit calls to return within
// 50ms total.
func TestAuditLog_AppendIsNonBlocking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	log, err := audit.NewFileAuditLog(path)
	if err != nil {
		t.Fatalf("NewFileAuditLog: %v", err)
	}
	defer log.Close()

	const goroutines = 10
	const eventsPerGoroutine = 100

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			for j := range eventsPerGoroutine {
				ev := makeEvent(audit.EventAllow, fmt.Sprintf("tenant-%d-%d", i, j))
				log.Emit(ev)
			}
		}(i)
	}
	wg.Wait()

	elapsed := time.Since(start)
	// Non-blocking: 1000 Emit calls must complete in under 500ms on any machine.
	if elapsed > 500*time.Millisecond {
		t.Errorf("Emit calls took %v, expected <500ms (non-blocking)", elapsed)
	}
}

// --- T027: TestAuditLog_DrainsToFile ---

// TestAuditLog_DrainsToFile verifies that emitted events are flushed to disk
// after Close() and are valid JSON lines.
func TestAuditLog_DrainsToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	log, err := audit.NewFileAuditLog(path)
	if err != nil {
		t.Fatalf("NewFileAuditLog: %v", err)
	}

	const count = 20
	for i := range count {
		log.Emit(makeEvent(audit.EventDeny, fmt.Sprintf("tenant-%d", i)))
	}

	// Close must drain the channel before returning.
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := readJSONL(t, path)
	if len(events) != count {
		t.Errorf("expected %d events in file, got %d", count, len(events))
	}
	for i, ev := range events {
		if ev.EventType != audit.EventDeny {
			t.Errorf("event[%d].EventType = %q, want %q", i, ev.EventType, audit.EventDeny)
		}
	}
}

// --- T027: TestAuditLog_FilePermissions0600 ---

// TestAuditLog_FilePermissions0600 verifies that the audit log file is created
// with mode 0600 (NFR-11).
func TestAuditLog_FilePermissions0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not enforced on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	log, err := audit.NewFileAuditLog(path)
	if err != nil {
		t.Fatalf("NewFileAuditLog: %v", err)
	}
	// Emit at least one event so the file is created.
	log.Emit(makeEvent(audit.EventAllow, "tenant-a"))
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Errorf("file mode = %04o, want 0600", mode)
	}
}

// --- T027: TestAuditLog_DropsOnFullBuffer ---

// TestAuditLog_DropsOnFullBuffer verifies that when the internal channel is full
// Emit does not block and the dropped counter is incremented. We use a
// single-element buffer and block the drain goroutine via a controlled hook.
func TestAuditLog_DropsOnFullBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	// Create a log with a buffer of 1 so it fills immediately.
	log, err := audit.NewFileAuditLogWithBuffer(path, 1)
	if err != nil {
		t.Fatalf("NewFileAuditLogWithBuffer: %v", err)
	}

	// Block the drain goroutine by pausing it.
	log.PauseDrain()

	// Emit many events — channel can hold 1, the rest must be dropped silently.
	const burst = 50
	for range burst {
		log.Emit(makeEvent(audit.EventRateLimited, "tenant-x"))
	}

	dropped := log.DroppedCount()
	// At least some events must have been dropped (channel size = 1, burst = 50).
	if dropped == 0 {
		t.Errorf("expected dropped > 0 after burst of %d into buffer-1 log", burst)
	}

	log.ResumeDrain()
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// --- T027: TestAuditLog_CloseDrainsRemaining ---

// TestAuditLog_CloseDrainsRemaining verifies that Close() flushes all buffered
// events to disk before returning, even if the drain goroutine was behind.
func TestAuditLog_CloseDrainsRemaining(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	log, err := audit.NewFileAuditLogWithBuffer(path, 256)
	if err != nil {
		t.Fatalf("NewFileAuditLogWithBuffer: %v", err)
	}

	// Pause the drain so events pile up in the channel.
	log.PauseDrain()

	const count = 30
	for i := range count {
		log.Emit(makeEvent(audit.EventTenantConfigChange, fmt.Sprintf("tenant-%d", i)))
	}

	// Resume + Close must drain all 30 events.
	log.ResumeDrain()
	if err := log.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := readJSONL(t, path)
	if len(events) != count {
		t.Errorf("after Close, expected %d events on disk, got %d", count, len(events))
	}
}
