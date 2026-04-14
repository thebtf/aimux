package metrics

import (
	"sync"
	"testing"
)

func TestNewCollectorHasZeroCounters(t *testing.T) {
	c := New()
	snap := c.Snapshot()

	if snap.TotalRequests != 0 {
		t.Errorf("TotalRequests: want 0, got %d", snap.TotalRequests)
	}
	if snap.TotalErrors != 0 {
		t.Errorf("TotalErrors: want 0, got %d", snap.TotalErrors)
	}
	if snap.ErrorRate != 0 {
		t.Errorf("ErrorRate: want 0, got %f", snap.ErrorRate)
	}
	if snap.AvgLatencyMs != 0 {
		t.Errorf("AvgLatencyMs: want 0, got %f", snap.AvgLatencyMs)
	}
	if len(snap.PerCLI) != 0 {
		t.Errorf("PerCLI: want empty, got %d entries", len(snap.PerCLI))
	}
}

func TestRecordRequestIncrementsCounters(t *testing.T) {
	c := New()
	c.RecordRequest("codex", "", 100, false)
	c.RecordRequest("codex", "", 200, false)

	snap := c.Snapshot()

	if snap.TotalRequests != 2 {
		t.Errorf("TotalRequests: want 2, got %d", snap.TotalRequests)
	}
	if snap.TotalErrors != 0 {
		t.Errorf("TotalErrors: want 0, got %d", snap.TotalErrors)
	}

	cli, ok := snap.PerCLI["codex"]
	if !ok {
		t.Fatal("expected per-CLI entry for codex")
	}
	if cli.Requests != 2 {
		t.Errorf("codex Requests: want 2, got %d", cli.Requests)
	}
}

func TestRecordRequestErrorIncrements(t *testing.T) {
	c := New()
	c.RecordRequest("gemini", "", 50, false)
	c.RecordRequest("gemini", "", 0, true)

	snap := c.Snapshot()

	if snap.TotalRequests != 2 {
		t.Errorf("TotalRequests: want 2, got %d", snap.TotalRequests)
	}
	if snap.TotalErrors != 1 {
		t.Errorf("TotalErrors: want 1, got %d", snap.TotalErrors)
	}

	cli := snap.PerCLI["gemini"]
	if cli.Errors != 1 {
		t.Errorf("gemini Errors: want 1, got %d", cli.Errors)
	}
}

func TestSnapshotErrorRateAndAvgLatency(t *testing.T) {
	c := New()
	c.RecordRequest("claude", "", 100, false)
	c.RecordRequest("claude", "", 300, false)
	c.RecordRequest("claude", "", 0, true) // error — latency 0

	snap := c.Snapshot()

	// 1 error out of 3 requests
	wantRate := 1.0 / 3.0
	if diff := snap.ErrorRate - wantRate; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ErrorRate: want %f, got %f", wantRate, snap.ErrorRate)
	}

	// avg latency = (100+300+0)/3 = 133.333...
	wantAvg := 400.0 / 3.0
	if diff := snap.AvgLatencyMs - wantAvg; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("AvgLatencyMs: want %f, got %f", wantAvg, snap.AvgLatencyMs)
	}
}

func TestPerCLIBreakdown(t *testing.T) {
	c := New()
	c.RecordRequest("codex", "", 100, false)
	c.RecordRequest("gemini", "", 200, false)
	c.RecordRequest("gemini", "", 0, true)

	snap := c.Snapshot()

	if snap.TotalRequests != 3 {
		t.Errorf("TotalRequests: want 3, got %d", snap.TotalRequests)
	}

	codex, ok := snap.PerCLI["codex"]
	if !ok {
		t.Fatal("missing codex entry")
	}
	if codex.Requests != 1 || codex.Errors != 0 {
		t.Errorf("codex: want 1 req 0 err, got %d req %d err", codex.Requests, codex.Errors)
	}

	gemini, ok := snap.PerCLI["gemini"]
	if !ok {
		t.Fatal("missing gemini entry")
	}
	if gemini.Requests != 2 || gemini.Errors != 1 {
		t.Errorf("gemini: want 2 req 1 err, got %d req %d err", gemini.Requests, gemini.Errors)
	}
	if gemini.ErrorRate != 0.5 {
		t.Errorf("gemini ErrorRate: want 0.5, got %f", gemini.ErrorRate)
	}
	if gemini.AvgLatencyMs != 100 {
		t.Errorf("gemini AvgLatencyMs: want 100, got %f", gemini.AvgLatencyMs)
	}
	if gemini.LastUsed == "" {
		t.Error("gemini LastUsed: want non-empty ISO timestamp")
	}
}

func TestResetZerosEverything(t *testing.T) {
	c := New()
	c.RecordRequest("codex", "", 500, false)
	c.RecordRequest("gemini", "", 0, true)

	c.Reset()
	snap := c.Snapshot()

	if snap.TotalRequests != 0 {
		t.Errorf("after Reset TotalRequests: want 0, got %d", snap.TotalRequests)
	}
	if snap.TotalErrors != 0 {
		t.Errorf("after Reset TotalErrors: want 0, got %d", snap.TotalErrors)
	}
	if len(snap.PerCLI) != 0 {
		t.Errorf("after Reset PerCLI: want empty, got %d entries", len(snap.PerCLI))
	}
}

func TestConcurrentRecordRequest(t *testing.T) {
	c := New()
	const goroutines = 100
	const requestsEach = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			cli := "cli-a"
			if idx%2 == 0 {
				cli = "cli-b"
			}
			for j := 0; j < requestsEach; j++ {
				c.RecordRequest(cli, "", int64(j*10), j%5 == 0)
			}
		}(i)
	}
	wg.Wait()

	snap := c.Snapshot()
	total := int64(goroutines * requestsEach)
	if snap.TotalRequests != total {
		t.Errorf("TotalRequests: want %d, got %d", total, snap.TotalRequests)
	}

	var cliSum int64
	for _, cs := range snap.PerCLI {
		cliSum += cs.Requests
	}
	if cliSum != total {
		t.Errorf("per-CLI sum: want %d, got %d", total, cliSum)
	}
}

// --- T028: per-project metrics tests ---

// TestRecordRequest_PerProject verifies that two different projectIDs produce
// independent per-project counters without cross-contamination.
func TestRecordRequest_PerProject(t *testing.T) {
	c := New()

	c.RecordRequest("codex", "proj-alpha", 100, false)
	c.RecordRequest("codex", "proj-alpha", 200, false)
	c.RecordRequest("codex", "proj-beta", 50, true)

	snap := c.Snapshot()

	alpha, ok := snap.PerProject["proj-alpha/codex"]
	if !ok {
		t.Fatal("expected per-project entry for proj-alpha/codex")
	}
	if alpha.Requests != 2 {
		t.Errorf("proj-alpha/codex Requests: want 2, got %d", alpha.Requests)
	}
	if alpha.Errors != 0 {
		t.Errorf("proj-alpha/codex Errors: want 0, got %d", alpha.Errors)
	}

	beta, ok := snap.PerProject["proj-beta/codex"]
	if !ok {
		t.Fatal("expected per-project entry for proj-beta/codex")
	}
	if beta.Requests != 1 {
		t.Errorf("proj-beta/codex Requests: want 1, got %d", beta.Requests)
	}
	if beta.Errors != 1 {
		t.Errorf("proj-beta/codex Errors: want 1, got %d", beta.Errors)
	}

	// Global CLI counter must include all 3 requests regardless of project.
	cli, ok := snap.PerCLI["codex"]
	if !ok {
		t.Fatal("expected per-CLI entry for codex")
	}
	if cli.Requests != 3 {
		t.Errorf("codex global Requests: want 3, got %d", cli.Requests)
	}
}

// TestRecordRequest_EmptyProject verifies that an empty projectID does not create
// a per-project entry and falls back to CLI-only tracking.
func TestRecordRequest_EmptyProject(t *testing.T) {
	c := New()

	c.RecordRequest("gemini", "", 150, false)

	snap := c.Snapshot()

	if len(snap.PerProject) != 0 {
		t.Errorf("PerProject: want empty for empty projectID, got %d entries", len(snap.PerProject))
	}

	cli, ok := snap.PerCLI["gemini"]
	if !ok {
		t.Fatal("expected per-CLI entry for gemini")
	}
	if cli.Requests != 1 {
		t.Errorf("gemini Requests: want 1, got %d", cli.Requests)
	}
}

// TestSnapshot_IncludesProjectBreakdown verifies that Snapshot.PerProject is
// populated when requests are recorded with a non-empty projectID.
func TestSnapshot_IncludesProjectBreakdown(t *testing.T) {
	c := New()

	c.RecordRequest("claude", "project-1", 300, false)
	c.RecordRequest("codex", "project-1", 100, true)
	c.RecordRequest("codex", "project-2", 200, false)

	snap := c.Snapshot()

	if len(snap.PerProject) != 3 {
		t.Errorf("PerProject: want 3 entries, got %d", len(snap.PerProject))
	}

	// project-1/claude
	p1claude, ok := snap.PerProject["project-1/claude"]
	if !ok {
		t.Fatal("expected entry for project-1/claude")
	}
	if p1claude.Requests != 1 || p1claude.Errors != 0 {
		t.Errorf("project-1/claude: want 1 req 0 err, got %d req %d err", p1claude.Requests, p1claude.Errors)
	}

	// project-1/codex
	p1codex, ok := snap.PerProject["project-1/codex"]
	if !ok {
		t.Fatal("expected entry for project-1/codex")
	}
	if p1codex.Requests != 1 || p1codex.Errors != 1 {
		t.Errorf("project-1/codex: want 1 req 1 err, got %d req %d err", p1codex.Requests, p1codex.Errors)
	}

	// project-2/codex
	p2codex, ok := snap.PerProject["project-2/codex"]
	if !ok {
		t.Fatal("expected entry for project-2/codex")
	}
	if p2codex.Requests != 1 || p2codex.Errors != 0 {
		t.Errorf("project-2/codex: want 1 req 0 err, got %d req %d err", p2codex.Requests, p2codex.Errors)
	}

	// PerProject must be present in the snapshot (not nil).
	if snap.PerProject == nil {
		t.Error("PerProject: want non-nil map in snapshot")
	}
}
