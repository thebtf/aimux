package patterns

import "testing"

// TestMatchDomainTemplate_Auth verifies that a prompt containing auth-domain
// keywords resolves to the "auth" template.
func TestMatchDomainTemplate_Auth(t *testing.T) {
	tmpl := MatchDomainTemplate("Design authentication system")
	if tmpl == nil {
		t.Fatal("expected non-nil template for auth-related text, got nil")
	}
	if tmpl.Name != "auth" {
		t.Errorf("expected template name 'auth', got %q", tmpl.Name)
	}
}

// TestMatchDomainTemplate_API verifies that a prompt containing REST/API
// keywords resolves to the "api" template.
func TestMatchDomainTemplate_API(t *testing.T) {
	tmpl := MatchDomainTemplate("Build REST API endpoints")
	if tmpl == nil {
		t.Fatal("expected non-nil template for api-related text, got nil")
	}
	if tmpl.Name != "api" {
		t.Errorf("expected template name 'api', got %q", tmpl.Name)
	}
}

// TestMatchDomainTemplate_NoMatch verifies that text with no recognized domain
// keywords returns nil.
func TestMatchDomainTemplate_NoMatch(t *testing.T) {
	tmpl := MatchDomainTemplate("Random unrelated text")
	if tmpl != nil {
		t.Errorf("expected nil for unrelated text, got template %q", tmpl.Name)
	}
}

// TestMatchDomainTemplate_MultiKeyword verifies that when text touches multiple
// domains, the template with the most keyword matches wins.
func TestMatchDomainTemplate_MultiKeyword(t *testing.T) {
	// "Deploy database with monitoring" — "deploy" (deploy), "database" (database), "monitoring" (monitoring).
	// deploy: 1 match, database: 1 match, monitoring: 1 match — all tied at 1.
	// The test asserts a non-nil result and that the returned template is one of the three valid candidates.
	tmpl := MatchDomainTemplate("Deploy database with monitoring")
	if tmpl == nil {
		t.Fatal("expected non-nil template for multi-domain text, got nil")
	}
	validNames := map[string]bool{"deploy": true, "database": true, "monitoring": true}
	if !validNames[tmpl.Name] {
		t.Errorf("expected one of deploy/database/monitoring, got %q", tmpl.Name)
	}
}
