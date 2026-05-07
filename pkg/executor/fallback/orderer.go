package fallback

import (
	"context"
	"sort"

	"github.com/thebtf/aimux/pkg/executor/picker"
)

// Orderer re-ranks the remaining CLI candidates using the composite score formula
// from architecture.md ADR-001:
//
//	score = 0.40 * capability_norm
//	      + 0.30 * rolling_success_rate
//	      + 0.20 * latency_score
//	      + 0.10 * recency_weight
//
// All four terms are in [0.0, 1.0]. The final score is also in [0.0, 1.0].
// CLIs that are health-failed or already-attempted are excluded from the output.
type Orderer struct {
	score   *picker.CapabilityScore
	health  *picker.HealthChecker
	cfg     *FallbackConfig
}

// NewOrderer constructs an Orderer. All arguments must be non-nil.
func NewOrderer(score *picker.CapabilityScore, health *picker.HealthChecker, cfg *FallbackConfig) *Orderer {
	if score == nil || health == nil || cfg == nil {
		panic("fallback: Orderer: score, health, and cfg must not be nil")
	}
	return &Orderer{score: score, health: health, cfg: cfg}
}

// cliScore bundles a CLI name with its computed composite score.
type cliScore struct {
	cli   string
	score float64
}

// Rank returns the candidate CLIs ordered from highest to lowest composite score.
// It excludes CLIs that:
//   - are in the attempted set (already tried this fallback chain)
//   - fail the HealthChecker (binary not present or cache says unhealthy)
//
// An empty slice is returned when all remaining CLIs are excluded.
// The ctx is reserved for future async health probes; it is not used in v1.
func (o *Orderer) Rank(
	_ context.Context,
	candidates []string,
	taskClass string,
	attempted map[string]struct{},
	store ScoreStore,
) []string {
	weights := NormalizeWeights(o.cfg.ScoreWeights)
	budget := o.cfg.latencyBudget()
	decay := o.cfg.decayWindow()

	var scores []cliScore

	for _, cli := range candidates {
		// Skip CLIs that were already attempted in this fallback chain.
		if _, tried := attempted[cli]; tried {
			continue
		}
		// Skip CLIs that fail the health check.
		if !o.health.IsHealthy(cli) {
			continue
		}

		stats := store.Snapshot(cli)

		capNorm := o.score.Scoref(cli, taskClass)
		srNorm := stats.SuccessRate()
		latNorm := latencyScore(stats.P50LatencyMS, budget)
		recNorm := recencyWeight(stats.LastSuccessAt, decay)

		composite := weights.Capability*capNorm +
			weights.SuccessRate*srNorm +
			weights.Latency*latNorm +
			weights.Recency*recNorm

		scores = append(scores, cliScore{cli: cli, score: composite})
	}

	// Sort descending — highest composite score first.
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	result := make([]string, len(scores))
	for i, cs := range scores {
		result[i] = cs.cli
	}
	return result
}
