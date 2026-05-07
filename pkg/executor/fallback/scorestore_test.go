package fallback

import (
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/types"
)

// --- RunStats.SuccessRate ---

func TestRunStats_SuccessRate_ColdStart(t *testing.T) {
	s := RunStats{}
	if got := s.SuccessRate(); got != 0.5 {
		t.Errorf("cold start SuccessRate = %v, want 0.5", got)
	}
}

func TestRunStats_SuccessRate_AllSuccess(t *testing.T) {
	s := RunStats{SuccessCount: 10, FailCount: 0}
	if got := s.SuccessRate(); got != 1.0 {
		t.Errorf("all-success SuccessRate = %v, want 1.0", got)
	}
}

func TestRunStats_SuccessRate_AllFail(t *testing.T) {
	s := RunStats{SuccessCount: 0, FailCount: 10}
	if got := s.SuccessRate(); got != 0.0 {
		t.Errorf("all-fail SuccessRate = %v, want 0.0", got)
	}
}

func TestRunStats_SuccessRate_Mixed(t *testing.T) {
	s := RunStats{SuccessCount: 3, FailCount: 1}
	if got := s.SuccessRate(); got != 0.75 {
		t.Errorf("3/4 SuccessRate = %v, want 0.75", got)
	}
}

// --- InMemoryScoreStore basic ops ---

func TestInMemoryScoreStore_ColdSnapshot(t *testing.T) {
	store := NewInMemoryScoreStore()
	snap := store.Snapshot("nocli")
	if snap.SuccessCount != 0 || snap.FailCount != 0 {
		t.Errorf("cold snapshot non-zero: %+v", snap)
	}
	if !snap.LastSuccessAt.IsZero() {
		t.Errorf("cold LastSuccessAt should be zero")
	}
	if snap.P50LatencyMS != 0 {
		t.Errorf("cold P50LatencyMS should be 0, got %d", snap.P50LatencyMS)
	}
}

func TestInMemoryScoreStore_RecordSuccess(t *testing.T) {
	store := NewInMemoryScoreStore()
	store.RecordSuccess("cli-a", 100)
	store.RecordSuccess("cli-a", 200)

	snap := store.Snapshot("cli-a")
	if snap.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2", snap.SuccessCount)
	}
	if snap.FailCount != 0 {
		t.Errorf("FailCount = %d, want 0", snap.FailCount)
	}
	if snap.LastSuccessAt.IsZero() {
		t.Errorf("LastSuccessAt should be set after RecordSuccess")
	}
	// P50 should be between 100 and 200 after two samples
	if snap.P50LatencyMS < 100 || snap.P50LatencyMS > 200 {
		t.Errorf("P50LatencyMS = %d, want in [100, 200]", snap.P50LatencyMS)
	}
}

func TestInMemoryScoreStore_RecordFailure(t *testing.T) {
	store := NewInMemoryScoreStore()
	store.RecordFailure("cli-b", types.CLIErrorCodeRateLimit)
	store.RecordFailure("cli-b", types.CLIErrorCodeTimeout)

	snap := store.Snapshot("cli-b")
	if snap.FailCount != 2 {
		t.Errorf("FailCount = %d, want 2", snap.FailCount)
	}
	if snap.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0", snap.SuccessCount)
	}
	// No latency recorded for failures
	if snap.P50LatencyMS != 0 {
		t.Errorf("P50LatencyMS should be 0 for failures, got %d", snap.P50LatencyMS)
	}
}

func TestInMemoryScoreStore_EMALatency(t *testing.T) {
	store := NewInMemoryScoreStore()
	// First sample seeds the EMA
	store.RecordSuccess("cli-c", 500)
	snap1 := store.Snapshot("cli-c")
	if snap1.P50LatencyMS != 500 {
		t.Errorf("first sample EMA = %d, want 500 (seed)", snap1.P50LatencyMS)
	}

	// Second sample: EMA = 0.3*100 + 0.7*500 = 30 + 350 = 380
	store.RecordSuccess("cli-c", 100)
	snap2 := store.Snapshot("cli-c")
	want := int64(0.3*100 + 0.7*500)
	if snap2.P50LatencyMS != want {
		t.Errorf("EMA after 2nd sample = %d, want %d", snap2.P50LatencyMS, want)
	}
}

func TestInMemoryScoreStore_IsolatedCLIs(t *testing.T) {
	store := NewInMemoryScoreStore()
	store.RecordSuccess("x", 10)
	store.RecordFailure("y", types.CLIErrorCodeTimeout)

	snapX := store.Snapshot("x")
	snapY := store.Snapshot("y")

	if snapX.SuccessCount != 1 || snapX.FailCount != 0 {
		t.Errorf("x: unexpected stats %+v", snapX)
	}
	if snapY.SuccessCount != 0 || snapY.FailCount != 1 {
		t.Errorf("y: unexpected stats %+v", snapY)
	}
}

// --- Concurrent safety (run with -race) ---

func TestInMemoryScoreStore_ConcurrentAccess(t *testing.T) {
	store := NewInMemoryScoreStore()
	const goroutines = 20
	const ops = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			cli := "cli-concurrent"
			for j := 0; j < ops; j++ {
				if j%3 == 0 {
					store.RecordFailure(cli, types.CLIErrorCodeRateLimit)
				} else {
					store.RecordSuccess(cli, int64(50+j))
				}
				_ = store.Snapshot(cli)
			}
		}(i)
	}
	wg.Wait()

	snap := store.Snapshot("cli-concurrent")
	total := snap.SuccessCount + snap.FailCount
	if total != int64(goroutines*ops) {
		t.Errorf("total ops = %d, want %d", total, goroutines*ops)
	}
}

// --- latencyScore helper ---

func TestLatencyScore(t *testing.T) {
	budget := 30 * time.Second
	cases := []struct {
		p50MS int64
		want  float64
		name  string
	}{
		{0, 1.0, "cold start (0)"},
		{0, 1.0, "no samples"},
		{15000, 0.5, "half-budget"},
		{30000, 0.0, "at-budget"},
		{60000, 0.0, "over-budget (clamped)"},
		{1000, 1.0 - 1000.0/30000.0, "1s latency"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := latencyScore(tc.p50MS, budget)
			if abs64(got-tc.want) > 1e-9 {
				t.Errorf("latencyScore(%d, 30s) = %v, want %v", tc.p50MS, got, tc.want)
			}
		})
	}
}

func TestLatencyScore_NoBudget(t *testing.T) {
	got := latencyScore(1000, 0)
	if got != 1.0 {
		t.Errorf("zero budget: got %v, want 1.0", got)
	}
}

// --- recencyWeight helper ---

func TestRecencyWeight_ColdStart(t *testing.T) {
	got := recencyWeight(time.Time{}, time.Hour)
	if got != 0.5 {
		t.Errorf("cold start recencyWeight = %v, want 0.5", got)
	}
}

func TestRecencyWeight_JustNow(t *testing.T) {
	got := recencyWeight(time.Now(), time.Hour)
	// exp(0) = 1.0
	if got < 0.99 {
		t.Errorf("just-now recencyWeight = %v, want ~1.0", got)
	}
}

func TestRecencyWeight_OneDecayWindow(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	got := recencyWeight(past, time.Hour)
	// exp(-1) ≈ 0.368
	if abs64(got-0.368) > 0.01 {
		t.Errorf("1 decay window recencyWeight = %v, want ~0.368", got)
	}
}

func TestRecencyWeight_ZeroDecayWindow(t *testing.T) {
	got := recencyWeight(time.Now(), 0)
	if got != 0.5 {
		t.Errorf("zero decay window: got %v, want 0.5", got)
	}
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
