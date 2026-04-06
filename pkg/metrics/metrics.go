// Package metrics provides a thread-safe collector for request/latency/error counters.
package metrics

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// CLIMetrics holds per-CLI counters. All fields use atomic operations for lock-free reads.
type CLIMetrics struct {
	Requests       atomic.Int64
	Errors         atomic.Int64
	TotalLatencyMs atomic.Int64
	LastUsed       atomic.Int64 // unix timestamp (seconds)
}

// Collector is the top-level thread-safe metrics store.
type Collector struct {
	totalRequests  atomic.Int64
	totalErrors    atomic.Int64
	totalLatencyMs atomic.Int64

	mu        sync.RWMutex
	perCLI    map[string]*CLIMetrics
	startTime time.Time
}

// New creates a Collector with startTime set to now.
func New() *Collector {
	return &Collector{
		perCLI:    make(map[string]*CLIMetrics),
		startTime: time.Now(),
	}
}

// cliMetrics returns the CLIMetrics for the given CLI name, creating it if needed.
func (c *Collector) cliMetrics(cli string) *CLIMetrics {
	c.mu.RLock()
	m := c.perCLI[cli]
	c.mu.RUnlock()
	if m != nil {
		return m
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Double-checked locking — another goroutine may have inserted it.
	if m = c.perCLI[cli]; m == nil {
		m = &CLIMetrics{}
		c.perCLI[cli] = m
	}
	return m
}

// RecordRequest increments global and per-CLI counters.
// latencyMs is the observed round-trip time; for errors where no latency is known, pass 0.
func (c *Collector) RecordRequest(cli string, latencyMs int64, isError bool) {
	c.totalRequests.Add(1)
	c.totalLatencyMs.Add(latencyMs)
	if isError {
		c.totalErrors.Add(1)
	}

	m := c.cliMetrics(cli)
	m.Requests.Add(1)
	m.TotalLatencyMs.Add(latencyMs)
	if isError {
		m.Errors.Add(1)
	}
	m.LastUsed.Store(time.Now().Unix())
}

// Snapshot captures the current state of all counters.
func (c *Collector) Snapshot() *Snapshot {
	totalReqs := c.totalRequests.Load()
	totalErrs := c.totalErrors.Load()
	totalLat := c.totalLatencyMs.Load()

	snap := &Snapshot{
		UptimeSeconds: int64(time.Since(c.startTime).Seconds()),
		TotalRequests: totalReqs,
		TotalErrors:   totalErrs,
		ErrorRate:     safeRate(totalErrs, totalReqs),
		AvgLatencyMs:  safeAvg(totalLat, totalReqs),
		PerCLI:        make(map[string]*CLISnapshot),
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	for name, m := range c.perCLI {
		reqs := m.Requests.Load()
		errs := m.Errors.Load()
		lat := m.TotalLatencyMs.Load()
		lastUsed := m.LastUsed.Load()

		cs := &CLISnapshot{
			Requests:     reqs,
			Errors:       errs,
			ErrorRate:    safeRate(errs, reqs),
			AvgLatencyMs: safeAvg(lat, reqs),
		}
		if lastUsed > 0 {
			cs.LastUsed = time.Unix(lastUsed, 0).UTC().Format(time.RFC3339)
		}
		snap.PerCLI[name] = cs
	}

	return snap
}

// Reset zeroes all counters and clears per-CLI data.
func (c *Collector) Reset() {
	c.totalRequests.Store(0)
	c.totalErrors.Store(0)
	c.totalLatencyMs.Store(0)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.perCLI = make(map[string]*CLIMetrics)
}

// --- Snapshot types ---

// Snapshot is a point-in-time read of the Collector state.
type Snapshot struct {
	UptimeSeconds int64                 `json:"uptime_seconds"`
	TotalRequests int64                 `json:"total_requests"`
	TotalErrors   int64                 `json:"total_errors"`
	ErrorRate     float64               `json:"error_rate"`
	AvgLatencyMs  float64               `json:"avg_latency_ms"`
	PerCLI        map[string]*CLISnapshot `json:"per_cli"`
}

// CLISnapshot holds per-CLI aggregated metrics.
type CLISnapshot struct {
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	ErrorRate    float64 `json:"error_rate"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	LastUsed     string  `json:"last_used"` // ISO 8601 timestamp; empty if never used
}

// JSON serialises the snapshot to a JSON string.
// Marshalling errors are impossible for this struct shape; the empty string is
// returned only as a defensive fallback.
func (s *Snapshot) JSON() string {
	b, err := json.Marshal(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// --- helpers ---

func safeRate(num, den int64) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func safeAvg(sum, count int64) float64 {
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count)
}
