package executor

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/thebtf/aimux/pkg/executor/redact"
	"github.com/thebtf/aimux/pkg/types"
)

// cooldownKey is a structured map key for (cli, model) pairs.
// Using a struct avoids key collisions from models containing colons.
type cooldownKey struct {
	cli   string
	model string
}

// package-level env-var driven overrides, parsed once at init.
var (
	globalCooldownOverride time.Duration          // 0 = no override
	perKeyOverrides        = map[cooldownKey]time.Duration{}
	overridesMu            sync.RWMutex
)

func init() {
	// AIMUX_COOLDOWN_SECONDS: global default override for all CLIs and models.
	if v := os.Getenv("AIMUX_COOLDOWN_SECONDS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			log.Printf("WARN module=executor.cooldown AIMUX_COOLDOWN_SECONDS=%q invalid (want positive integer): %v", v, err)
		} else {
			globalCooldownOverride = time.Duration(n) * time.Second
		}
	}

	// AIMUX_COOLDOWN_OVERRIDES: comma-separated cli:model:seconds entries.
	// Example: AIMUX_COOLDOWN_OVERRIDES=codex:gpt-5.3-codex-spark:60,gemini:gemini-2.5-flash:120
	if v := os.Getenv("AIMUX_COOLDOWN_OVERRIDES"); v != "" {
		for _, entry := range strings.Split(v, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			parts := strings.SplitN(entry, ":", 3)
			if len(parts) != 3 {
				log.Printf("WARN module=executor.cooldown AIMUX_COOLDOWN_OVERRIDES entry %q malformed (want cli:model:seconds)", entry)
				continue
			}
			n, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil || n <= 0 {
				log.Printf("WARN module=executor.cooldown AIMUX_COOLDOWN_OVERRIDES entry %q seconds invalid: %v", entry, err)
				continue
			}
			key := cooldownKey{cli: parts[0], model: parts[1]}
			perKeyOverrides[key] = time.Duration(n) * time.Second
		}
	}
}

// ModelCooldownTracker tracks rate-limited models to prevent re-trying them
// before the cooldown expires. Thread-safe via sync.Map.
// Only quota errors and model-unavailability errors trigger cooldown.
type ModelCooldownTracker struct {
	entries sync.Map // key: cooldownKey → value: types.CooldownEntry
}

// NewModelCooldownTracker creates a new ModelCooldownTracker.
func NewModelCooldownTracker() *ModelCooldownTracker {
	return &ModelCooldownTracker{}
}

// MarkCooledDown records that a (cli, model) pair should not be used until
// duration expires. Re-marking an already cooled-down model extends its cooldown.
// triggerStderr is the redacted stderr excerpt that triggered the cooldown;
// it is stored for observability and returned by List.
//
// Override priority (highest first):
//  1. Per-key override from AIMUX_COOLDOWN_OVERRIDES
//  2. Global override from AIMUX_COOLDOWN_SECONDS
//  3. Caller-provided duration
func (t *ModelCooldownTracker) MarkCooledDown(cli, model string, duration time.Duration, triggerStderr string) {
	key := cooldownKey{cli: cli, model: model}

	// Apply env-var overrides.
	overridesMu.RLock()
	if d, ok := perKeyOverrides[key]; ok {
		duration = d
	} else if globalCooldownOverride > 0 {
		duration = globalCooldownOverride
	}
	overridesMu.RUnlock()

	redacted := truncateCooldownStr(redact.RedactSecrets(triggerStderr), 200)
	expiry := time.Now().Add(duration)
	entry := types.CooldownEntry{
		CLI:           cli,
		Model:         model,
		ExpiresAt:     expiry,
		TriggerStderr: redacted,
	}
	t.entries.Store(key, entry)

	log.Printf("INFO module=executor.cooldown cli=%s model=%s duration=%s trigger_stderr=%q",
		cli, model, duration, redacted)
}

// IsAvailable returns true if the model is NOT on cooldown (or cooldown has expired).
// Expired entries are cleaned up lazily on access.
func (t *ModelCooldownTracker) IsAvailable(cli, model string) bool {
	key := cooldownKey{cli: cli, model: model}
	val, ok := t.entries.Load(key)
	if !ok {
		return true
	}
	entry := val.(types.CooldownEntry)
	if time.Now().After(entry.ExpiresAt) {
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

// SetDuration stores a per-key duration override that will be applied on the
// next MarkCooledDown call for this (cli, model) pair.
// Not retroactive: existing cooldown entries are not modified.
func (t *ModelCooldownTracker) SetDuration(cli, model string, duration time.Duration) {
	key := cooldownKey{cli: cli, model: model}
	overridesMu.Lock()
	perKeyOverrides[key] = duration
	overridesMu.Unlock()
}

// Flush removes the cooldown entry for (cli, model) immediately.
// Returns nil on success, a non-nil error if no entry was found.
func (t *ModelCooldownTracker) Flush(cli, model string) error {
	key := cooldownKey{cli: cli, model: model}
	if _, loaded := t.entries.LoadAndDelete(key); !loaded {
		return fmt.Errorf("cooldown: no entry for cli=%s model=%s", cli, model)
	}
	return nil
}

// List returns a snapshot of all currently active (non-expired) cooldown entries.
// Goroutine-safe; expired entries are excluded from the result.
func (t *ModelCooldownTracker) List() []types.CooldownEntry {
	var result []types.CooldownEntry
	now := time.Now()
	t.entries.Range(func(_, v any) bool {
		entry := v.(types.CooldownEntry)
		if now.Before(entry.ExpiresAt) {
			result = append(result, entry)
		}
		return true
	})
	return result
}

// truncateCooldownStr truncates s to at most n bytes for log/storage fields,
// respecting UTF-8 rune boundaries so no multi-byte sequence is split.
func truncateCooldownStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Walk back from byte n until we land on a valid rune boundary.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
