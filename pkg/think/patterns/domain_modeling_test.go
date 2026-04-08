package patterns

import (
	"testing"
)

func TestDomainModel_Consistent(t *testing.T) {
	p := NewDomainModelingPattern()

	input := map[string]any{
		"domainName": "ECommerce",
		"entities": []any{
			map[string]any{"name": "Order"},
			map[string]any{"name": "Customer"},
		},
		"relationships": []any{
			map[string]any{"from": "Customer", "to": "Order"},
		},
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	result, err := p.Handle(validated, "test-session")
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	data := result.Data

	if consistent, ok := data["consistent"].(bool); !ok || !consistent {
		t.Errorf("expected consistent=true, got %v", data["consistent"])
	}

	orphans, ok := data["orphanEntities"].([]string)
	if !ok {
		t.Fatalf("orphanEntities missing or wrong type: %T", data["orphanEntities"])
	}
	if len(orphans) != 0 {
		t.Errorf("expected no orphan entities, got %v", orphans)
	}

	dangling, ok := data["danglingRelationships"].([]danglingRelationship)
	if !ok {
		t.Fatalf("danglingRelationships missing or wrong type: %T", data["danglingRelationships"])
	}
	if len(dangling) != 0 {
		t.Errorf("expected no dangling relationships, got %v", dangling)
	}
}

func TestDomainModel_OrphanEntity(t *testing.T) {
	p := NewDomainModelingPattern()

	input := map[string]any{
		"domainName": "Inventory",
		"entities": []any{
			map[string]any{"name": "Product"},
			map[string]any{"name": "Warehouse"},
			map[string]any{"name": "C"}, // orphan — not in any relationship
		},
		"relationships": []any{
			map[string]any{"from": "Product", "to": "Warehouse"},
		},
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	result, err := p.Handle(validated, "test-session")
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	data := result.Data

	if consistent, ok := data["consistent"].(bool); !ok || consistent {
		t.Errorf("expected consistent=false due to orphan, got %v", data["consistent"])
	}

	orphans, ok := data["orphanEntities"].([]string)
	if !ok {
		t.Fatalf("orphanEntities missing or wrong type: %T", data["orphanEntities"])
	}
	if len(orphans) != 1 || orphans[0] != "C" {
		t.Errorf("expected orphanEntities=[C], got %v", orphans)
	}
}

func TestDomainModel_DanglingRelationship(t *testing.T) {
	p := NewDomainModelingPattern()

	input := map[string]any{
		"domainName": "Billing",
		"entities": []any{
			map[string]any{"name": "Invoice"},
		},
		"relationships": []any{
			// "Payment" does not exist in entities — dangling
			map[string]any{"from": "Invoice", "to": "Payment"},
		},
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	result, err := p.Handle(validated, "test-session")
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	data := result.Data

	if consistent, ok := data["consistent"].(bool); !ok || consistent {
		t.Errorf("expected consistent=false due to dangling relationship, got %v", data["consistent"])
	}

	dangling, ok := data["danglingRelationships"].([]danglingRelationship)
	if !ok {
		t.Fatalf("danglingRelationships missing or wrong type: %T", data["danglingRelationships"])
	}
	if len(dangling) != 1 {
		t.Fatalf("expected 1 dangling relationship, got %d: %v", len(dangling), dangling)
	}
	if dangling[0].From != "Invoice" || dangling[0].To != "Payment" {
		t.Errorf("unexpected dangling entry: %+v", dangling[0])
	}
	if dangling[0].Reason == "" {
		t.Errorf("dangling relationship should have a non-empty reason")
	}
}

func TestDomainModel_Empty(t *testing.T) {
	p := NewDomainModelingPattern()

	input := map[string]any{
		"domainName": "Empty",
	}

	validated, err := p.Validate(input)
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	result, err := p.Handle(validated, "test-session")
	if err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	data := result.Data

	// No entities/relationships → consistency fields should not be present.
	if _, has := data["consistent"]; has {
		t.Errorf("expected no 'consistent' key for empty input, but found it: %v", data["consistent"])
	}
	if _, has := data["orphanEntities"]; has {
		t.Errorf("expected no 'orphanEntities' key for empty input")
	}
	if _, has := data["danglingRelationships"]; has {
		t.Errorf("expected no 'danglingRelationships' key for empty input")
	}

	if total, ok := data["totalComponents"].(int); !ok || total != 0 {
		t.Errorf("expected totalComponents=0, got %v", data["totalComponents"])
	}
}
