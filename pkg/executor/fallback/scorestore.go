package fallback

import (
	"math"
	"sync"
	"time"

	"github.com/thebtf/aimux/pkg/executor/types"
)

// RunStats holds the per-CLI runtime evidence collected by ScoreStore.
// All fields are immutable after Snapshot returns — callers may read safely without locking.
type RunStats struct {
	// SuccessCount is the total number of successful dispatches recorded.
	SuccessCount int64
	// FailCount is the total number of failed dispatches recorded (all error codes combined).
	FailCount int64
	// LastSuccessAt is the wall-clock time of the most recent recorded success.
	// Zero value means no successes have been recorded yet.
	LastSuccessAt time.Time
	// P50LatencyMS is the exponential moving average of per-attempt latency in milliseconds.
	// Zero means no successful latency samples recorded yet.
	P50LatencyMS int64
}

// SuccessRate returns the rolling success rate as a value in [0.0, 1.0].
// Returns 0.5 (neutral) when neither successes nor failures have been recorded (cold start).
func (s RunStats) SuccessRate() float64 {
	total := s.SuccessCount + s.FailCount
	if total == 0 {
		// Cold start: return 0.5 so neither this CLI nor its peers are unfairly penalized.
		return 0.5
	}
	return float64(s.SuccessCount) / float64(total)
}

// ScoreStore is the interface for per-CLI runtime evidence storage (spec FR-4, ADR-003).
// All methods must be safe for concurrent use from multiple goroutines.
type ScoreStore interface {
	// RecordSuccess records a successful dispatch with the given latency.
	RecordSuccess(cli string, latencyMS int64)
	// RecordFailure records a failed dispatch for the given CLI and error code.
	RecordFailure(cli string, code types.CLIErrorCode)
	// Snapshot returns a point-in-time copy of the RunStats for the given CLI.
	// Returns zero RunStats if no evidence has been recorded yet.
	Snapshot(cli string) RunStats
}

// emaAlpha is the smoothing factor for the exponential moving average of latency.
// Lower = smoother (less responsive to recent spikes); 0.3 is a typical choice.
const emaAlpha = 0.3

// statsEntry is the mutable per-CLI stats kept in the sync.Map.
// Protected by mu for atomic read-modify-write sequences.
type statsEntry struct {
	mu            sync.Mutex
	successCount  int64
	failCount     int64
	lastSuccessAt time.Time
	p50LatencyMS  int64 // EMA; 0 = uninitialized
}

// InMemoryScoreStore is the v1 implementation of ScoreStore backed by a sync.Map.
// Data is lost on daemon restart. v2 will add Loom SQLite persistence (ADR-003).
//
// Concurrent safety: each statsEntry has its own mutex; the outer sync.Map provides
// lock-free reads of already-stored entries. RecordSuccess/RecordFailure hold the
// entry-level mutex only — different CLIs never block each other.
type InMemoryScoreStore struct {
	// entries maps CLI name (string) → *statsEntry.
	entries sync.Map
}

// NewInMemoryScoreStore constructs an empty InMemoryScoreStore.
func NewInMemoryScoreStore() *InMemoryScoreStore {
	return &InMemoryScoreStore{}
}

// entry returns or creates the *statsEntry for the given CLI.
func (s *InMemoryScoreStore) entry(cli string) *statsEntry {
	v, _ := s.entries.LoadOrStore(cli, &statsEntry{})
	return v.(*statsEntry)
}

// RecordSuccess records a successful dispatch. Updates success count, EMA latency, and
// last-success timestamp. Safe for concurrent calls on the same CLI.
func (s *InMemoryScoreStore) RecordSuccess(cli string, latencyMS int64) {
	e := s.entry(cli)
	e.mu.Lock()
	defer e.mu.Unlock()

	e.successCount++
	e.lastSuccessAt = time.Now()

	if e.p50LatencyMS == 0 {
		// First sample: seed the EMA with the actual value.
		e.p50LatencyMS = latencyMS
	} else {
		// EMA update: new = alpha*sample + (1-alpha)*old
		e.p50LatencyMS = int64(emaAlpha*float64(latencyMS) + (1-emaAlpha)*float64(e.p50LatencyMS))
	}
}

// RecordFailure records a failed dispatch. Updates fail count only — we do not record
// latency for failed attempts because they may have timed out and would skew the EMA.
func (s *InMemoryScoreStore) RecordFailure(cli string, _ types.CLIErrorCode) {
	e := s.entry(cli)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failCount++
}

// Snapshot returns an immutable copy of the RunStats for the given CLI.
// Returns zero-value RunStats if no evidence exists yet (cold start).
func (s *InMemoryScoreStore) Snapshot(cli string) RunStats {
	v, ok := s.entries.Load(cli)
	if !ok {
		return RunStats{}
	}
	e := v.(*statsEntry)
	e.mu.Lock()
	defer e.mu.Unlock()
	return RunStats{
		SuccessCount:  e.successCount,
		FailCount:     e.failCount,
		LastSuccessAt: e.lastSuccessAt,
		P50LatencyMS:  e.p50LatencyMS,
	}
}

// latencyScore converts a p50 latency value into a [0, 1] score.
// latency_score = 1 - clamp(p50 / budget, 0, 1).
// A p50 of 0 (no data) returns 1.0 (neutral — not penalized for lack of data).
func latencyScore(p50MS int64, budgetDuration time.Duration) float64 {
	if p50MS <= 0 || budgetDuration <= 0 {
		return 1.0 // cold start: no penalty
	}
	ratio := float64(p50MS) / float64(budgetDuration.Milliseconds())
	if ratio > 1.0 {
		ratio = 1.0
	}
	return 1.0 - ratio
}

// recencyWeight computes the exponential decay weight for how recently the CLI succeeded.
// recency_weight = exp(-(now - last_success) / decay_window).
// If last_success is zero (never succeeded), returns 0.5 (neutral — cold start).
func recencyWeight(lastSuccess time.Time, decayWindow time.Duration) float64 {
	if lastSuccess.IsZero() || decayWindow <= 0 {
		// Cold start: 0.5 keeps the CLI competitive without unfair boost.
		return 0.5
	}
	elapsed := time.Since(lastSuccess)
	if elapsed < 0 {
		elapsed = 0
	}
	return math.Exp(-elapsed.Seconds() / decayWindow.Seconds())
}
