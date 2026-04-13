package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WALEntry represents a single write-ahead log entry.
type WALEntry struct {
	Timestamp time.Time `json:"ts"`
	Type      string    `json:"type"` // session_create, session_update, job_create, job_update
	ID        string    `json:"id"`
	Data      json.RawMessage `json:"data"`
}

// WAL provides append-only journaling for crash recovery.
// Entries are flushed to disk before state changes are committed to memory.
type WAL struct {
	file *os.File
	mu   sync.Mutex
}

// NewWAL creates or opens a WAL file at the given path.
func NewWAL(path string) (*WAL, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create WAL directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open WAL: %w", err)
	}

	return &WAL{file: f}, nil
}

// Append writes an entry to the WAL. Must be called before committing
// the corresponding state change to memory.
func (w *WAL) Append(entryType, id string, data any) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal WAL data: %w", err)
	}

	entry := WALEntry{
		Timestamp: time.Now(),
		Type:      entryType,
		ID:        id,
		Data:      jsonData,
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal WAL entry: %w", err)
	}

	line = append(line, '\n')
	if _, err := w.file.Write(line); err != nil {
		return fmt.Errorf("write WAL: %w", err)
	}

	return w.file.Sync()
}

// Replay reads all WAL entries for crash recovery.
func Replay(path string) ([]WALEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read WAL: %w", err)
	}

	var entries []WALEntry
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var entry WALEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip corrupt entries
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// Flush syncs the WAL to disk.
func (w *WAL) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Sync()
}

// Close closes the WAL file.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// Truncate clears the WAL after a successful SQLite snapshot.
// Uses a write-then-rename sequence so the WAL is never in a partially-empty
// state if the process dies mid-truncation.
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	name := w.file.Name()
	dir := filepath.Dir(name)

	// Write an empty temp file in the same directory so os.Rename is atomic
	// (same filesystem, POSIX rename guarantee).
	tmp, err := os.CreateTemp(dir, "wal-trunc-*.tmp")
	if err != nil {
		return fmt.Errorf("truncate WAL (create temp): %w", err)
	}
	tmpName := tmp.Name()

	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("truncate WAL (close temp): %w", err)
	}

	// Close the old file handle BEFORE rename — Windows holds file locks.
	w.file.Close()

	// Rename temp over the live WAL — atomic on POSIX, best-effort on Windows.
	if err := os.Rename(tmpName, name); err != nil {
		os.Remove(tmpName)
		// Reopen the original file even on rename failure.
		w.file, _ = os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		return fmt.Errorf("truncate WAL (rename): %w", err)
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("truncate WAL (reopen): %w", err)
	}
	w.file = f
	return nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
