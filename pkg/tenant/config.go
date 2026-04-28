package tenant

// Role constants for TenantConfig.Role. Operator tenants may access privileged
// MCP tools (audit, tenants). Plain tenants may only access standard tools.
const (
	RoleOperator = "operator"
	RolePlain    = "plain"
)

// Default limits applied by TenantConfig.WithDefaults() when fields are zero-valued.
const (
	defaultMaxConcurrentSessions = 10
	defaultRateLimitPerSec       = 1000
	defaultLogRetentionDays      = 90
	defaultRemovalDrainSeconds   = 30
)

// TenantConfig holds the parsed configuration for a single tenant, sourced from a
// tenants.yaml entry. All fields are populated by the YAML loader; zero values are
// replaced with safe defaults by WithDefaults().
//
// TenantConfig is a value type. Callers must not take pointers to fields or pass
// *TenantConfig to avoid aliasing between hot-reload snapshots.
type TenantConfig struct {
	// Name is the human-readable tenant identifier. Must be unique across all tenants.
	Name string `yaml:"name"`

	// UID is the OS-level user ID of the tenant's owner process.
	// Must be unique; used for SO_PEERCRED-based authentication (Phase 8).
	UID int `yaml:"uid"`

	// Role is either RoleOperator or RolePlain. Operator tenants may use privileged tools.
	Role string `yaml:"role"`

	// RateLimitPerSec is the peak token budget per second for this tenant.
	// Zero means "use default" until WithDefaults() is applied.
	RateLimitPerSec int64 `yaml:"rate_limit_per_sec"`

	// RefillRatePerSec is the sustained refill rate (tokens/second) for the rate-limiter
	// bucket. Defaults to RateLimitPerSec when zero.
	RefillRatePerSec int64 `yaml:"refill_rate_per_sec"`

	// MaxConcurrentSessions caps the number of simultaneous muxcore sessions for this
	// tenant. Session #N+1 is denied with -32000 when this limit is reached.
	MaxConcurrentSessions int `yaml:"max_concurrent_sessions"`

	// LogRetentionDays controls how long per-tenant log partitions are kept on disk.
	LogRetentionDays int `yaml:"log_retention_days"`

	// RemovalDrainSeconds is the grace window after a tenant is removed from
	// tenants.yaml before existing sessions are force-closed.
	RemovalDrainSeconds int `yaml:"removal_drain_seconds"`
}

// WithDefaults returns a new TenantConfig with zero-valued fields replaced by safe
// defaults. The receiver is never mutated — a copy is returned in all cases.
//
// Default values:
//   - MaxConcurrentSessions: 10
//   - RateLimitPerSec:       1000
//   - RefillRatePerSec:      equals RateLimitPerSec (after its default is applied)
//   - LogRetentionDays:      90
//   - RemovalDrainSeconds:   30
func (c TenantConfig) WithDefaults() TenantConfig {
	out := c // copy — do not mutate c

	if out.MaxConcurrentSessions == 0 {
		out.MaxConcurrentSessions = defaultMaxConcurrentSessions
	}
	if out.RateLimitPerSec == 0 {
		out.RateLimitPerSec = defaultRateLimitPerSec
	}
	// RefillRatePerSec defaults to RateLimitPerSec (after its default is applied).
	if out.RefillRatePerSec == 0 {
		out.RefillRatePerSec = out.RateLimitPerSec
	}
	if out.LogRetentionDays == 0 {
		out.LogRetentionDays = defaultLogRetentionDays
	}
	if out.RemovalDrainSeconds == 0 {
		out.RemovalDrainSeconds = defaultRemovalDrainSeconds
	}

	return out
}
