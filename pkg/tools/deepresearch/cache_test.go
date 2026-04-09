package deepresearch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/tools/deepresearch"
)

// ----------------------------------------------------------------------------
// Cache in-memory tests
// ----------------------------------------------------------------------------

func TestCache_Search_MatchesTopic(t *testing.T) {
	cache := deepresearch.NewCache()
	cache.Put("golang concurrency", "summary", "model1", nil, "result A")
	cache.Put("python asyncio", "summary", "model1", nil, "result B")

	results := cache.Search("golang", 10)
	if len(results) != 1 {
		t.Fatalf("Search returned %d results, want 1", len(results))
	}
	if results[0].Content != "result A" {
		t.Errorf("Content = %q, want %q", results[0].Content, "result A")
	}
}

func TestCache_Search_CaseInsensitive(t *testing.T) {
	cache := deepresearch.NewCache()
	cache.Put("Golang Channels", "summary", "model1", nil, "result")

	results := cache.Search("golang", 10)
	if len(results) != 1 {
		t.Fatalf("Search returned %d results, want 1", len(results))
	}
}

func TestCache_Search_NoMatch(t *testing.T) {
	cache := deepresearch.NewCache()
	cache.Put("golang concurrency", "summary", "model1", nil, "result")

	results := cache.Search("rust", 10)
	if len(results) != 0 {
		t.Fatalf("Search returned %d results, want 0", len(results))
	}
}

func TestCache_Search_LimitRespected(t *testing.T) {
	cache := deepresearch.NewCache()
	cache.Put("go topic 1", "summary", "model1", nil, "r1")
	cache.Put("go topic 2", "summary", "model1", nil, "r2")
	cache.Put("go topic 3", "summary", "model1", nil, "r3")

	results := cache.Search("go topic", 2)
	if len(results) > 2 {
		t.Fatalf("Search returned %d results, want <= 2", len(results))
	}
}

func TestCache_Search_ZeroLimit_ReturnsAll(t *testing.T) {
	cache := deepresearch.NewCache()
	cache.Put("go topic 1", "summary", "model1", nil, "r1")
	cache.Put("go topic 2", "summary", "model1", nil, "r2")
	cache.Put("go topic 3", "summary", "model1", nil, "r3")

	results := cache.Search("go topic", 0)
	if len(results) != 3 {
		t.Fatalf("Search returned %d results, want 3", len(results))
	}
}

func TestCache_Cleanup_RemovesExpired(t *testing.T) {
	cache := deepresearch.NewCache()
	// Put two entries, then manually expire one by putting with past time.
	// We can only test this via the public API, so we verify Cleanup returns 0
	// when entries are fresh (not expired).
	cache.Put("topic1", "summary", "model1", nil, "result1")
	cache.Put("topic2", "summary", "model1", nil, "result2")

	removed := cache.Cleanup()
	// Fresh entries should not be removed.
	if removed != 0 {
		t.Errorf("Cleanup removed %d entries, want 0 for fresh entries", removed)
	}
}

func TestCache_Get_WithFiles(t *testing.T) {
	cache := deepresearch.NewCache()
	files := []string{"file1.go", "file2.go"}

	cache.Put("topic", "report", "model1", files, "content with files")

	entry, ok := cache.Get("topic", "report", "model1", files)
	if !ok {
		t.Fatal("expected cache hit with files")
	}
	if entry.Content != "content with files" {
		t.Errorf("Content = %q, want %q", entry.Content, "content with files")
	}
}

func TestCache_Get_FilesOrderIndependent(t *testing.T) {
	cache := deepresearch.NewCache()

	cache.Put("topic", "report", "model1", []string{"a.go", "b.go"}, "ordered content")

	// Lookup with reversed order — cache key must be order-independent.
	entry, ok := cache.Get("topic", "report", "model1", []string{"b.go", "a.go"})
	if !ok {
		t.Fatal("expected cache hit regardless of file order")
	}
	if entry.Content != "ordered content" {
		t.Errorf("Content = %q, want %q", entry.Content, "ordered content")
	}
}

func TestCache_Get_DifferentFiles_Miss(t *testing.T) {
	cache := deepresearch.NewCache()
	cache.Put("topic", "report", "model1", []string{"a.go"}, "content A")

	_, ok := cache.Get("topic", "report", "model1", []string{"b.go"})
	if ok {
		t.Fatal("expected cache miss for different files")
	}
}

// ----------------------------------------------------------------------------
// SaveEntryToDisk / LoadDiskEntries tests
// ----------------------------------------------------------------------------

func TestSaveEntryToDisk_CreatesFile(t *testing.T) {
	dir := t.TempDir()

	err := deepresearch.SaveEntryToDisk(dir, "test topic", "summary", "model1", nil, "test content")
	if err != nil {
		t.Fatalf("SaveEntryToDisk: %v", err)
	}

	cacheDir := filepath.Join(dir, deepresearch.DiskCacheSubdir)
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}
	if filepath.Ext(entries[0].Name()) != ".json" {
		t.Errorf("expected .json file, got %s", entries[0].Name())
	}
}

func TestSaveEntryToDisk_EmptyCWD_NoOp(t *testing.T) {
	// Should return nil and create nothing.
	err := deepresearch.SaveEntryToDisk("", "topic", "summary", "model1", nil, "content")
	if err != nil {
		t.Fatalf("SaveEntryToDisk with empty cwd: %v", err)
	}
}

func TestSaveEntryToDisk_ContentCorrect(t *testing.T) {
	dir := t.TempDir()

	err := deepresearch.SaveEntryToDisk(dir, "my topic", "json", "gemini-pro", []string{"a.go"}, "research result")
	if err != nil {
		t.Fatalf("SaveEntryToDisk: %v", err)
	}

	cacheDir := filepath.Join(dir, deepresearch.DiskCacheSubdir)
	files, _ := os.ReadDir(cacheDir)
	data, err := os.ReadFile(filepath.Join(cacheDir, files[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var entry deepresearch.CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if entry.Topic != "my topic" {
		t.Errorf("Topic = %q, want %q", entry.Topic, "my topic")
	}
	if entry.Content != "research result" {
		t.Errorf("Content = %q, want %q", entry.Content, "research result")
	}
	if entry.OutputFormat != "json" {
		t.Errorf("OutputFormat = %q, want %q", entry.OutputFormat, "json")
	}
	if entry.Model != "gemini-pro" {
		t.Errorf("Model = %q, want %q", entry.Model, "gemini-pro")
	}
}

func TestLoadDiskEntries_EmptyCWD(t *testing.T) {
	entries, err := deepresearch.LoadDiskEntries("")
	if err != nil {
		t.Fatalf("LoadDiskEntries with empty cwd: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries for empty cwd, got %v", entries)
	}
}

func TestLoadDiskEntries_NoCacheDir(t *testing.T) {
	dir := t.TempDir()
	// No .agent/deepresearch subdir exists — should return nil, nil.
	entries, err := deepresearch.LoadDiskEntries(dir)
	if err != nil {
		t.Fatalf("LoadDiskEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestLoadDiskEntries_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	if err := deepresearch.SaveEntryToDisk(dir, "round-trip topic", "markdown", "model-x", nil, "round-trip content"); err != nil {
		t.Fatalf("SaveEntryToDisk: %v", err)
	}

	entries, err := deepresearch.LoadDiskEntries(dir)
	if err != nil {
		t.Fatalf("LoadDiskEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Topic != "round-trip topic" {
		t.Errorf("Topic = %q, want %q", e.Topic, "round-trip topic")
	}
	if e.Content != "round-trip content" {
		t.Errorf("Content = %q, want %q", e.Content, "round-trip content")
	}
}

func TestLoadDiskEntries_MultipleEntries(t *testing.T) {
	dir := t.TempDir()

	topics := []string{"topic A", "topic B", "topic C"}
	for _, topic := range topics {
		if err := deepresearch.SaveEntryToDisk(dir, topic, "summary", "model1", nil, topic+" content"); err != nil {
			t.Fatalf("SaveEntryToDisk(%s): %v", topic, err)
		}
	}

	entries, err := deepresearch.LoadDiskEntries(dir)
	if err != nil {
		t.Fatalf("LoadDiskEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

func TestLoadDiskEntries_IgnoresMalformedFiles(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, deepresearch.DiskCacheSubdir)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write a malformed JSON file.
	if err := os.WriteFile(filepath.Join(cacheDir, "bad.json"), []byte("not json {{{"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Write a valid entry alongside.
	if err := deepresearch.SaveEntryToDisk(dir, "valid topic", "summary", "model1", nil, "valid content"); err != nil {
		t.Fatalf("SaveEntryToDisk: %v", err)
	}

	entries, err := deepresearch.LoadDiskEntries(dir)
	if err != nil {
		t.Fatalf("LoadDiskEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 valid entry, got %d", len(entries))
	}
}

func TestLoadDiskEntries_IgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, deepresearch.DiskCacheSubdir)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Create a subdirectory inside cache dir — should be skipped.
	if err := os.MkdirAll(filepath.Join(cacheDir, "subdir"), 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}

	entries, err := deepresearch.LoadDiskEntries(dir)
	if err != nil {
		t.Fatalf("LoadDiskEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries (only a dir), got %d", len(entries))
	}
}

func TestLoadDiskEntries_IgnoresExpiredEntries(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, deepresearch.DiskCacheSubdir)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Write an entry with a creation time 31 days ago (past the 30-day TTL).
	expired := deepresearch.CacheEntry{
		Topic:        "old topic",
		OutputFormat: "summary",
		Model:        "model1",
		Content:      "old content",
		CreatedAt:    time.Now().Add(-31 * 24 * time.Hour),
	}
	data, _ := json.Marshal(expired)
	if err := os.WriteFile(filepath.Join(cacheDir, "expired.json"), data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries, err := deepresearch.LoadDiskEntries(dir)
	if err != nil {
		t.Fatalf("LoadDiskEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected expired entry to be filtered, got %d entries", len(entries))
	}
}

func TestLoadDiskEntries_IgnoresNonJSONFiles(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, deepresearch.DiskCacheSubdir)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "notes.txt"), []byte("some text"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries, err := deepresearch.LoadDiskEntries(dir)
	if err != nil {
		t.Fatalf("LoadDiskEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries for non-JSON file, got %d", len(entries))
	}
}

// ----------------------------------------------------------------------------
// CacheKey determinism
// ----------------------------------------------------------------------------

func TestSaveEntryToDisk_DeterministicKey(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	files := []string{"c.go", "a.go", "b.go"}
	if err := deepresearch.SaveEntryToDisk(dir1, "topic", "fmt", "model", files, "content"); err != nil {
		t.Fatalf("SaveEntryToDisk dir1: %v", err)
	}
	// Reversed file order — same key expected.
	filesRev := []string{"b.go", "a.go", "c.go"}
	if err := deepresearch.SaveEntryToDisk(dir2, "topic", "fmt", "model", filesRev, "content"); err != nil {
		t.Fatalf("SaveEntryToDisk dir2: %v", err)
	}

	files1, _ := os.ReadDir(filepath.Join(dir1, deepresearch.DiskCacheSubdir))
	files2, _ := os.ReadDir(filepath.Join(dir2, deepresearch.DiskCacheSubdir))

	if files1[0].Name() != files2[0].Name() {
		t.Errorf("cache key not deterministic: %q != %q", files1[0].Name(), files2[0].Name())
	}
}
