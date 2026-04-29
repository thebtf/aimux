package tenant

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// maxTenantCount is the maximum number of tenant entries allowed in tenants.yaml.
const maxTenantCount = 10_000

// yamlTenantEntry is the on-disk representation of a single tenant in tenants.yaml.
type yamlTenantEntry struct {
	Name                  string `yaml:"name"`
	UID                   int    `yaml:"uid"`
	Role                  string `yaml:"role"`
	RateLimitPerSec       int64  `yaml:"rate_limit_per_sec"`
	RefillRatePerSec      int64  `yaml:"refill_rate_per_sec"`
	MaxConcurrentSessions int    `yaml:"max_concurrent_sessions"`
	LogRetentionDays      int    `yaml:"log_retention_days"`
	RemovalDrainSeconds   int    `yaml:"removal_drain_seconds"`
}

// yamlTenantsFile is the top-level structure of tenants.yaml.
type yamlTenantsFile struct {
	Tenants []yamlTenantEntry `yaml:"tenants"`
}

// LoadFromFile parses tenants.yaml at path and returns an immutable Snapshot.
//
// Validation rules:
//   - No duplicate UIDs or names
//   - Required fields: name, uid, role
//   - Role must be RoleOperator or RolePlain
//   - Entry count must not exceed maxTenantCount
//
// WithDefaults() is applied to each entry after parsing.
// Returns a typed error on any validation failure; never panics.
// An empty (zero-byte or zero-tenant) file returns an empty Snapshot, not an error.
func LoadFromFile(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("LoadFromFile: %w", err)
		}
		return nil, fmt.Errorf("LoadFromFile: read %q: %w", path, err)
	}

	// Empty file → empty snapshot (legacy-default mode, file was created by bootstrap
	// but not yet populated, or operator intentionally cleared it).
	if len(data) == 0 {
		return NewSnapshot(nil), nil
	}

	var f yamlTenantsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("LoadFromFile: parse %q: %w", path, err)
	}

	if len(f.Tenants) > maxTenantCount {
		return nil, fmt.Errorf("LoadFromFile: tenant count %d exceeds maximum %d", len(f.Tenants), maxTenantCount)
	}

	seenUIDs := make(map[int]string, len(f.Tenants))
	seenNames := make(map[string]bool, len(f.Tenants))
	entries := make(map[int]TenantConfig, len(f.Tenants))

	for i, e := range f.Tenants {
		if e.Name == "" {
			return nil, fmt.Errorf("LoadFromFile: entry[%d]: missing required field: name", i)
		}
		if e.UID == 0 {
			return nil, fmt.Errorf("LoadFromFile: entry[%d] %q: missing required field: uid", i, e.Name)
		}
		if e.Role == "" {
			return nil, fmt.Errorf("LoadFromFile: entry[%d] %q: missing required field: role", i, e.Name)
		}
		if e.Role != RoleOperator && e.Role != RolePlain {
			return nil, fmt.Errorf("LoadFromFile: entry[%d] %q: unknown role %q (must be %q or %q)", i, e.Name, e.Role, RoleOperator, RolePlain)
		}
		// Reserved sentinel name. canonicalTenantID in pkg/swarm collapses
		// "legacy-default" to "" for partition unity (PRC v7 F2 S3); allowing
		// a tenant literally named "legacy-default" would alias them into the
		// legacy partition and silently break cross-tenant isolation.
		if e.Name == LegacyDefault {
			return nil, fmt.Errorf("LoadFromFile: entry[%d]: tenant name %q is reserved (collides with legacy-default sentinel)", i, e.Name)
		}

		if prev, dup := seenUIDs[e.UID]; dup {
			return nil, fmt.Errorf("LoadFromFile: duplicate uid %d (tenants %q and %q)", e.UID, prev, e.Name)
		}
		seenUIDs[e.UID] = e.Name

		if seenNames[e.Name] {
			return nil, fmt.Errorf("LoadFromFile: duplicate name %q", e.Name)
		}
		seenNames[e.Name] = true

		cfg := TenantConfig{
			Name:                  e.Name,
			UID:                   e.UID,
			Role:                  e.Role,
			RateLimitPerSec:       e.RateLimitPerSec,
			RefillRatePerSec:      e.RefillRatePerSec,
			MaxConcurrentSessions: e.MaxConcurrentSessions,
			LogRetentionDays:      e.LogRetentionDays,
			RemovalDrainSeconds:   e.RemovalDrainSeconds,
		}
		entries[e.UID] = cfg.WithDefaults()
	}

	return NewSnapshot(entries), nil
}
