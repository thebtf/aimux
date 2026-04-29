package tenant_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/tenant"
)

// --- T005: TenantConfig invariant tests ---

func TestTenantConfig_WithDefaults_MaxConcurrentSessions(t *testing.T) {
	cfg := tenant.TenantConfig{}
	got := cfg.WithDefaults()
	if got.MaxConcurrentSessions != 10 {
		t.Fatalf("expected MaxConcurrentSessions=10, got %d", got.MaxConcurrentSessions)
	}
}

func TestTenantConfig_WithDefaults_RateLimitPerSec(t *testing.T) {
	cfg := tenant.TenantConfig{}
	got := cfg.WithDefaults()
	if got.RateLimitPerSec != 1000 {
		t.Fatalf("expected RateLimitPerSec=1000, got %d", got.RateLimitPerSec)
	}
}

func TestTenantConfig_WithDefaults_DoesNotMutateOriginal(t *testing.T) {
	cfg := tenant.TenantConfig{}
	_ = cfg.WithDefaults()
	// Original must remain zero-valued for fields not explicitly set.
	if cfg.MaxConcurrentSessions != 0 {
		t.Fatal("WithDefaults must return a new copy, not mutate the original")
	}
}

func TestTenantConfig_WithDefaults_PreservesExistingValues(t *testing.T) {
	cfg := tenant.TenantConfig{
		MaxConcurrentSessions: 50,
		RateLimitPerSec:       5000,
	}
	got := cfg.WithDefaults()
	if got.MaxConcurrentSessions != 50 {
		t.Fatalf("WithDefaults must not overwrite non-zero MaxConcurrentSessions, got %d", got.MaxConcurrentSessions)
	}
	if got.RateLimitPerSec != 5000 {
		t.Fatalf("WithDefaults must not overwrite non-zero RateLimitPerSec, got %d", got.RateLimitPerSec)
	}
}

func TestTenantConfig_RoleConstants(t *testing.T) {
	if tenant.RoleOperator == tenant.RolePlain {
		t.Fatal("RoleOperator and RolePlain must have distinct values")
	}
	if tenant.RoleOperator == "" {
		t.Fatal("RoleOperator must not be empty")
	}
	if tenant.RolePlain == "" {
		t.Fatal("RolePlain must not be empty")
	}
}

func TestTenantConfig_WithDefaults_RefillRateDefaultsToRateLimit(t *testing.T) {
	cfg := tenant.TenantConfig{RateLimitPerSec: 500}
	got := cfg.WithDefaults()
	// When RefillRatePerSec is zero, it should default to RateLimitPerSec.
	if got.RefillRatePerSec != 500 {
		t.Fatalf("expected RefillRatePerSec=500 (matches RateLimitPerSec), got %d", got.RefillRatePerSec)
	}
}

func TestTenantConfig_WithDefaults_LogRetentionDays(t *testing.T) {
	cfg := tenant.TenantConfig{}
	got := cfg.WithDefaults()
	if got.LogRetentionDays <= 0 {
		t.Fatalf("expected positive LogRetentionDays default, got %d", got.LogRetentionDays)
	}
}

func TestTenantConfig_WithDefaults_RemovalDrainSeconds(t *testing.T) {
	cfg := tenant.TenantConfig{}
	got := cfg.WithDefaults()
	if got.RemovalDrainSeconds <= 0 {
		t.Fatalf("expected positive RemovalDrainSeconds default, got %d", got.RemovalDrainSeconds)
	}
}
