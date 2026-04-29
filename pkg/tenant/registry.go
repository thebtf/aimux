package tenant

import "sync/atomic"

// Snapshot is an immutable point-in-time view of all enrolled tenants,
// keyed by OS UID. Snapshots are replaced atomically on SIGHUP hot-reload;
// they are never mutated after construction.
//
// The byUID map uses int as the key type (matching the OS uid_t range on all
// supported platforms). ResolveByUID accepts uint32 (the wire format from
// muxcore ConnInfo.PeerUid) and casts it to int for the lookup, which is safe
// on all 32-bit and 64-bit platforms where uid_t fits in int.
type Snapshot struct {
	byUID map[int]TenantConfig
}

// NewSnapshot constructs a Snapshot from the provided UID→config map.
// The map is copied into the snapshot so the caller may discard it safely.
// WithDefaults() is applied to each entry during construction.
func NewSnapshot(entries map[int]TenantConfig) *Snapshot {
	m := make(map[int]TenantConfig, len(entries))
	for uid, cfg := range entries {
		m[uid] = cfg.WithDefaults()
	}
	return &Snapshot{byUID: m}
}

// TenantRegistry is a concurrent-safe, zero-lock registry for enrolled tenants.
// Reads (Resolve, IsMultiTenant) never acquire a lock; they perform a single
// atomic pointer load, achieving O(1) latency on the hot path.
//
// The registry must be constructed with NewRegistry(). The zero value is not safe.
type TenantRegistry struct {
	// snapshot holds a *Snapshot; may be nil before the first Swap.
	snapshot atomic.Pointer[Snapshot]
}

// NewRegistry returns an empty, ready-to-use TenantRegistry.
// Resolve will return (TenantConfig{}, false) until Swap is called at least once.
func NewRegistry() *TenantRegistry {
	return &TenantRegistry{}
}

// Swap atomically installs a new snapshot. After Swap returns, all subsequent
// Resolve and IsMultiTenant calls observe the new snapshot. Callers must not
// modify the snapshot after passing it to Swap.
//
// Swap is safe to call concurrently with Resolve and IsMultiTenant.
func (r *TenantRegistry) Swap(snap *Snapshot) {
	r.snapshot.Store(snap)
}

// Resolve looks up a tenant by OS UID. It returns the TenantConfig and true on
// success, or (TenantConfig{}, false) when the UID is not enrolled.
//
// Resolve never acquires a lock; it performs one atomic load on the hot path.
func (r *TenantRegistry) Resolve(uid int) (TenantConfig, bool) {
	snap := r.snapshot.Load()
	if snap == nil {
		return TenantConfig{}, false
	}
	cfg, ok := snap.byUID[uid]
	return cfg, ok
}

// ResolveByName looks up a tenant by its human-readable name. It returns the
// TenantConfig and true on success, or (TenantConfig{}, false) when no tenant
// with that name is enrolled.
//
// ResolveByName performs a linear scan over the snapshot (O(n) where n is the
// number of enrolled tenants). Tenant counts are expected to be small (≤1000),
// so this is acceptable. For UID-based hot-path lookups use Resolve instead.
func (r *TenantRegistry) ResolveByName(name string) (TenantConfig, bool) {
	snap := r.snapshot.Load()
	if snap == nil {
		return TenantConfig{}, false
	}
	for _, cfg := range snap.byUID {
		if cfg.Name == name {
			return cfg, true
		}
	}
	return TenantConfig{}, false
}

// ResolveByUID looks up a tenant by OS UID (uint32 wire format from muxcore ConnInfo).
// It returns the TenantConfig and true on success, or (TenantConfig{}, false) when the
// UID is not enrolled.
//
// ResolveByUID is O(1) — it performs one atomic pointer load on the snapshot, then a
// direct map lookup by the cast int key. This is the hot-path method called by
// AuthorizeSession on every session open.
func (r *TenantRegistry) ResolveByUID(uid uint32) (TenantConfig, bool) {
	snap := r.snapshot.Load()
	if snap == nil {
		return TenantConfig{}, false
	}
	cfg, ok := snap.byUID[int(uid)]
	return cfg, ok
}

// IsMultiTenant returns true when the registry contains at least one enrolled tenant.
// It returns false when operating in legacy single-tenant mode (no tenants.yaml).
func (r *TenantRegistry) IsMultiTenant() bool {
	snap := r.snapshot.Load()
	return snap != nil && len(snap.byUID) > 0
}
