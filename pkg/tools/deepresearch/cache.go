package deepresearch

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// CacheEntry stores a cached research result.
type CacheEntry struct {
	Topic        string    `json:"topic"`
	OutputFormat string    `json:"output_format"`
	Model        string    `json:"model"`
	FilesHash    string    `json:"files_hash"`
	Content      string    `json:"content"`
	CreatedAt    time.Time `json:"created_at"`
}

// Cache provides exact-match caching for deep research results.
// Key = SHA-256(topic + output_format + model + files_hash).
// TTL = 30 days.
type Cache struct {
	entries map[string]*CacheEntry
	ttl     time.Duration
	mu      sync.RWMutex
}

// NewCache creates a cache with 30-day TTL.
func NewCache() *Cache {
	return &Cache{
		entries: make(map[string]*CacheEntry),
		ttl:     30 * 24 * time.Hour,
	}
}

// Get returns a cached result if it exists and hasn't expired.
func (c *Cache) Get(topic, outputFormat, model string, files []string) (*CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := cacheKey(topic, outputFormat, model, files)
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	if time.Since(entry.CreatedAt) > c.ttl {
		return nil, false // expired
	}

	return entry, true
}

// Put stores a result in the cache.
func (c *Cache) Put(topic, outputFormat, model string, files []string, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := cacheKey(topic, outputFormat, model, files)
	c.entries[key] = &CacheEntry{
		Topic:        topic,
		OutputFormat: outputFormat,
		Model:        model,
		FilesHash:    filesHash(files),
		Content:      content,
		CreatedAt:    time.Now(),
	}
}

// Search returns entries matching a keyword in topic.
func (c *Cache) Search(query string, limit int) []*CacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	query = strings.ToLower(query)
	var results []*CacheEntry

	for _, entry := range c.entries {
		if time.Since(entry.CreatedAt) > c.ttl {
			continue
		}
		if strings.Contains(strings.ToLower(entry.Topic), query) {
			results = append(results, entry)
			if limit > 0 && len(results) >= limit {
				break
			}
		}
	}

	return results
}

// Cleanup removes expired entries.
func (c *Cache) Cleanup() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0
	for key, entry := range c.entries {
		if time.Since(entry.CreatedAt) > c.ttl {
			delete(c.entries, key)
			removed++
		}
	}
	return removed
}

func cacheKey(topic, outputFormat, model string, files []string) string {
	h := sha256.New()
	h.Write([]byte(topic))
	h.Write([]byte(outputFormat))
	h.Write([]byte(model))
	h.Write([]byte(filesHash(files)))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func filesHash(files []string) string {
	if len(files) == 0 {
		return ""
	}
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)

	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
