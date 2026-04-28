package tenant

import (
	"os"
	"strings"
	"testing"
)

// buildYAML is a helper that constructs a tenants.yaml bytes slice from entries
// expressed as a YAML string fragment. The caller provides the full YAML body.
func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "tenants-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return f.Name()
}

func TestLoader_ValidConfig(t *testing.T) {
	yaml := `
tenants:
  - name: alice
    uid: 1001
    role: operator
    rate_limit_per_sec: 500
  - name: bob
    uid: 1002
    role: plain
`
	path := writeTempYAML(t, yaml)
	snap, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: unexpected error: %v", err)
	}

	alice, ok := snap.byUID[1001]
	if !ok {
		t.Fatal("expected uid 1001 (alice) in snapshot")
	}
	if alice.Name != "alice" {
		t.Errorf("alice.Name = %q, want %q", alice.Name, "alice")
	}
	if alice.Role != RoleOperator {
		t.Errorf("alice.Role = %q, want %q", alice.Role, RoleOperator)
	}
	if alice.RateLimitPerSec != 500 {
		t.Errorf("alice.RateLimitPerSec = %d, want 500", alice.RateLimitPerSec)
	}

	bob, ok := snap.byUID[1002]
	if !ok {
		t.Fatal("expected uid 1002 (bob) in snapshot")
	}
	if bob.Role != RolePlain {
		t.Errorf("bob.Role = %q, want %q", bob.Role, RolePlain)
	}
}

func TestLoader_DuplicateUIDs(t *testing.T) {
	yaml := `
tenants:
  - name: alice
    uid: 1001
    role: operator
  - name: alice2
    uid: 1001
    role: plain
`
	path := writeTempYAML(t, yaml)
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected error for duplicate uid, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate uid") {
		t.Errorf("error %q should contain 'duplicate uid'", err.Error())
	}
}

func TestLoader_DuplicateNames(t *testing.T) {
	yaml := `
tenants:
  - name: alice
    uid: 1001
    role: operator
  - name: alice
    uid: 1002
    role: plain
`
	path := writeTempYAML(t, yaml)
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate name") {
		t.Errorf("error %q should contain 'duplicate name'", err.Error())
	}
}

func TestLoader_MissingFields(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing name",
			yaml: `
tenants:
  - uid: 1001
    role: operator
`,
			wantErr: "missing required field: name",
		},
		{
			name: "missing uid",
			yaml: `
tenants:
  - name: alice
    role: operator
`,
			wantErr: "missing required field: uid",
		},
		{
			name: "missing role",
			yaml: `
tenants:
  - name: alice
    uid: 1001
`,
			wantErr: "missing required field: role",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempYAML(t, tc.yaml)
			_, err := LoadFromFile(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q should contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestLoader_UnknownRole(t *testing.T) {
	yaml := `
tenants:
  - name: alice
    uid: 1001
    role: superuser
`
	path := writeTempYAML(t, yaml)
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected error for unknown role, got nil")
	}
	if !strings.Contains(err.Error(), "unknown role") {
		t.Errorf("error %q should contain 'unknown role'", err.Error())
	}
}

func TestLoader_TenantCountLimit(t *testing.T) {
	// Build a YAML with 10001 entries.
	var sb strings.Builder
	sb.WriteString("tenants:\n")
	for i := 1; i <= maxTenantCount+1; i++ {
		sb.WriteString("  - name: tenant")
		sb.WriteString(itoa(i))
		sb.WriteString("\n    uid: ")
		sb.WriteString(itoa(i))
		sb.WriteString("\n    role: plain\n")
	}
	path := writeTempYAML(t, sb.String())
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected error for tenant count exceeding limit, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("error %q should contain 'exceeds maximum'", err.Error())
	}
}

func TestLoader_DefaultsApplied(t *testing.T) {
	// Zero refill_rate_per_sec should default to rate_limit_per_sec.
	yaml := `
tenants:
  - name: alice
    uid: 1001
    role: operator
    rate_limit_per_sec: 750
`
	path := writeTempYAML(t, yaml)
	snap, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	alice, ok := snap.byUID[1001]
	if !ok {
		t.Fatal("alice not found in snapshot")
	}
	if alice.RefillRatePerSec != 750 {
		t.Errorf("RefillRatePerSec = %d, want 750 (should default to rate_limit_per_sec)", alice.RefillRatePerSec)
	}
	if alice.MaxConcurrentSessions != defaultMaxConcurrentSessions {
		t.Errorf("MaxConcurrentSessions = %d, want %d", alice.MaxConcurrentSessions, defaultMaxConcurrentSessions)
	}
}

func TestLoader_NonexistentFile(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/path/tenants.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestLoader_MalformedYAML(t *testing.T) {
	path := writeTempYAML(t, "tenants: [{{invalid yaml")
	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestLoader_EmptyFile(t *testing.T) {
	path := writeTempYAML(t, "")
	snap, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("empty file should produce empty snapshot, got error: %v", err)
	}
	// Empty file → zero tenants in snapshot → registry reports non-multi-tenant.
	reg := NewRegistry()
	reg.Swap(snap)
	if reg.IsMultiTenant() {
		t.Error("empty file should produce non-multi-tenant snapshot")
	}
}

// itoa is a minimal int-to-string helper to avoid importing strconv in tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
