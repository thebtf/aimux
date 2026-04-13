package executor

import (
	"sync"
	"time"
)

// cooldownKey is a structured map key for (cli, model) pairs.
// Using a struct avoids key collisions that could arise if cli or model
// contains a colon (e.g. a model name like "provider:model-name").
type cooldownKey struct {
	cli   string
	model string
}

// ModelCooldownTracker tracks rate-limited models to prevent re-trying them
// before the cooldown expires. Thread-safe via sync.Map.
// Only quota errors trigger cooldown — transient/fatal errors do NOT.
type ModelCooldownTracker struct {
	entries sync.Map // key: cooldownKey → value: time.Time (expiry)
}

// NewModelCooldownTracker creates a new ModelCooldownTracker.
func NewModelCooldownTracker() *ModelCooldownTracker {
	return &ModelCooldownTracker{}
}

// MarkCooledDown records that a (cli, model) pair should not be used until
// duration expires. Re-marking an already cooled-down model extends its cooldown.
func (t *ModelCooldownTracker) MarkCooledDown(cli, model string, duration time.Duration) {
	key := cooldownKey{cli: cli, model: model}
	expiry := time.Now().Add(duration)
	t.entries.Store(key, expiry)
}

// IsAvailable returns true if the model is NOT on cooldown (or cooldown has expired).
// Expired entries are cleaned up lazily on access.
func (t *ModelCooldownTracker) IsAvailable(cli, model string) bool {
	key := cooldownKey{cli: cli, model: model}
	val, ok := t.entries.Load(key)
	if !ok {
		return true
	}
	expiry := val.(time.Time)
	if time.Now().After(expiry) {
		t.entries.Delete(key)
		return true
	}
	return false
}

// FilterAvailable returns only the models NOT currently on cooldown for the
// given CLI. The order of the returned slice matches the input order.
// An empty input returns an empty (non-nil) slice.
func (t *ModelCooldownTracker) FilterAvailable(cli string, models []string) []string {
	result := make([]string, 0, len(models))
	for _, model := range models {
		if t.IsAvailable(cli, model) {
			result = append(result, model)
		}
	}
	return result
}
