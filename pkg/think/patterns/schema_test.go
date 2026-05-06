package patterns_test

import (
	"sync"
	"testing"

	think "github.com/thebtf/aimux/pkg/think"
	"github.com/thebtf/aimux/pkg/think/patterns"
)

var registerPatternsOnce sync.Once

func ensurePatternsRegistered() {
	registerPatternsOnce.Do(func() {
		patterns.RegisterAll()
	})
}

func TestSchemaFieldsNonEmpty(t *testing.T) {
	ensurePatternsRegistered()

	for _, name := range think.GetAllPatterns() {
		p := think.GetPattern(name)
		if p == nil {
			t.Fatalf("pattern %q not registered", name)
		}
		if len(p.SchemaFields()) == 0 {
			t.Errorf("pattern %q returned empty SchemaFields()", p.Name())
		}
	}
}

func TestSchemaFieldsDebuggingApproach(t *testing.T) {
	ensurePatternsRegistered()

	p := think.GetPattern("debugging_approach")
	if p == nil {
		t.Fatal("debugging_approach pattern not registered")
	}

	issue, ok := p.SchemaFields()["issue"]
	if !ok {
		t.Fatal("debugging_approach SchemaFields missing 'issue' field")
	}
	if !issue.Required {
		t.Error("debugging_approach 'issue' field should be Required=true")
	}
}

func TestSchemaFieldsDecisionFramework(t *testing.T) {
	ensurePatternsRegistered()

	p := think.GetPattern("decision_framework")
	if p == nil {
		t.Fatal("decision_framework pattern not registered")
	}

	sf := p.SchemaFields()
	for _, required := range []string{"decision"} {
		f, ok := sf[required]
		if !ok {
			t.Errorf("decision_framework SchemaFields missing %q field", required)
			continue
		}
		if !f.Required {
			t.Errorf("decision_framework %q field should be Required=true", required)
		}
	}

	for _, optional := range []string{"criteria", "options"} {
		f, ok := sf[optional]
		if !ok {
			t.Errorf("decision_framework SchemaFields missing optional %q field", optional)
			continue
		}
		if f.Required {
			t.Errorf("decision_framework %q field should be Required=false", optional)
		}
	}
}

func TestSchemaFieldsBaseThinkNotRegistered(t *testing.T) {
	ensurePatternsRegistered()

	if p := think.GetPattern("think"); p != nil {
		t.Fatal("base think keyword-router must not be registered as a low-level cognitive move")
	}
}

// TestSchemaFieldsRequiredFields verifies that each pattern declares at least one
// required field and that all fields have non-empty Type and Description.
func TestSchemaFieldsRequiredFields(t *testing.T) {
	ensurePatternsRegistered()

	for _, name := range think.GetAllPatterns() {
		p := think.GetPattern(name)
		if p == nil {
			t.Fatalf("pattern %q not found", name)
		}
		fields := p.SchemaFields()
		hasRequired := false
		for fieldName, schema := range fields {
			if schema.Type == "" {
				t.Errorf("pattern %q field %q has empty Type", name, fieldName)
			}
			if schema.Description == "" {
				t.Errorf("pattern %q field %q has empty Description", name, fieldName)
			}
			if schema.Required {
				hasRequired = true
			}
		}
		if !hasRequired {
			if name == "scientific_method" {
				stage, hasStage := fields["stage"]
				entryType, hasEntryType := fields["entry_type"]
				if !hasStage || !hasEntryType {
					t.Errorf("pattern %q must declare both stage and entry_type for conditional requiredness", name)
					continue
				}
				if stage.Type != "enum" || entryType.Type != "enum" {
					t.Errorf("pattern %q conditional fields must remain enum-typed", name)
				}
				continue
			}
			t.Errorf("pattern %q has no required fields", name)
		}
	}
}

func TestCategoryNonEmpty(t *testing.T) {
	ensurePatternsRegistered()

	for _, name := range think.GetAllPatterns() {
		p := think.GetPattern(name)
		if p == nil {
			t.Fatalf("pattern %q not found", name)
		}
		if p.Category() == "" {
			t.Errorf("pattern %q returned empty Category()", name)
		}
	}
}

func TestCategoryAllSolo(t *testing.T) {
	ensurePatternsRegistered()

	for _, name := range think.GetAllPatterns() {
		p := think.GetPattern(name)
		if p == nil {
			t.Fatalf("pattern %q not found", name)
		}
		if got := p.Category(); got != "solo" {
			t.Errorf("pattern %q returned Category()=%q, want 'solo'", name, got)
		}
	}
}
