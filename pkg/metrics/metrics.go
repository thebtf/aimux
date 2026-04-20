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

	mu         sync.RWMutex
	perCLI     map[string]*CLIMetrics
	perProject map[string]*CLIMetrics // keyed by "projectID/cli"
	startTime  time.Time
}

// New creates a Collector with startTime set to now.
func New() *Collector {
	return &Collector{
		perCLI:     make(map[string]*CLIMetrics),
		perProject: make(map[string]*CLIMetrics),
		startTime:  time.Now(),
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

// projectMetrics returns the CLIMetrics for a project+CLI key, creating it if needed.
// The key format is "projectID/cli". Only called when projectID is non-empty.
func (c *Collector) projectMetrics(projectID, cli string) *CLIMetrics {
	key := projectID + "/" + cli
	c.mu.RLock()
	m := c.perProject[key]
	c.mu.RUnlock()
	if m != nil {
		return m
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if m = c.perProject[key]; m == nil {
		m = &CLIMetrics{}
		c.perProject[key] = m
	}
	return m
}

// RecordRequest increments global and per-CLI counters.
// projectID scopes the request to a per-project breakdown; empty string disables project tracking.
// latencyMs is the observed round-trip time; for errors where no latency is known, pass 0.
func (c *Collector) RecordRequest(cli, projectID string, latencyMs int64, isError bool) {
	c.totalRequests.Add(1)
	c.totalLatencyMs.Add(latencyMs)
	if isError {
		c.totalErrors.Add(1)
	}

	now := time.Now().Unix()

	m := c.cliMetrics(cli)
	m.Requests.Add(1)
	m.TotalLatencyMs.Add(latencyMs)
	if isError {
		m.Errors.Add(1)
	}
	m.LastUsed.Store(now)

	// Per-project tracking (only when a projectID is provided).
	if projectID != "" {
		pm := c.projectMetrics(projectID, cli)
		pm.Requests.Add(1)
		pm.TotalLatencyMs.Add(latencyMs)
		if isError {
			pm.Errors.Add(1)
		}
		pm.LastUsed.Store(now)
	}
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
		PerProject:    make(map[string]*CLISnapshot),
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

	for key, m := range c.perProject {
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
		snap.PerProject[key] = cs
	}

	return snap
}

// FailureRate returns the cumulative error rate for a CLI when at least
// minRequests have been recorded. Returns 0.0 when the CLI is unknown or
// fewer than minRequests have been recorded (fail-open: allow candidate).
//
// Used by buildFallbackCandidates to skip CLIs whose error rate exceeds a
// threshold (e.g., 0.95 on N=10 requests). The cumulative rate reflects
// real production signal — it is not a rolling window, but it is sourced
// from live counters and is accurate enough for the 95% skip heuristic.
func (c *Collector) FailureRate(cli string, minRequests int) float64 {
	c.mu.RLock()
	m := c.perCLI[cli]
	c.mu.RUnlock()
	if m == nil {
		return 0.0
	}
	reqs := m.Requests.Load()
	if reqs < int64(minRequests) {
		return 0.0 // not enough data — fail-open
	}
	errs := m.Errors.Load()
	return safeRate(errs, reqs)
}

// Reset zeroes all counters and clears per-CLI and per-project data.
func (c *Collector) Reset() {
	c.totalRequests.Store(0)
	c.totalErrors.Store(0)
	c.totalLatencyMs.Store(0)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.perCLI = make(map[string]*CLIMetrics)
	c.perProject = make(map[string]*CLIMetrics)
}

// --- Snapshot types ---

// Snapshot is a point-in-time read of the Collector state.
type Snapshot struct {
	UptimeSeconds int64                   `json:"uptime_seconds"`
	TotalRequests int64                   `json:"total_requests"`
	TotalErrors   int64                   `json:"total_errors"`
	ErrorRate     float64                 `json:"error_rate"`
	AvgLatencyMs  float64                 `json:"avg_latency_ms"`
	PerCLI        map[string]*CLISnapshot `json:"per_cli"`
	PerProject    map[string]*CLISnapshot `json:"per_project"` // keyed by "projectID/cli"
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
