package harness

import (
	"strings"
	"testing"

	thinkcore "github.com/thebtf/aimux/pkg/think"
)

func TestInputForPatternFailsClosedWhenRequiredFieldCannotBeDerived(t *testing.T) {
	fields := map[string]thinkcore.FieldSchema{
		"topic":    {Type: "string", Required: true},
		"findings": {Type: "array", Required: true},
	}

	_, err := inputForPattern(fields, "visible work product")
	if err == nil {
		t.Fatal("required array field was derived from a synthetic value")
	}
	if !strings.Contains(err.Error(), "findings") {
		t.Fatalf("error = %q, want field name", err)
	}
}

func TestInputForPatternUsesVisibleWorkProductForKnownStringFields(t *testing.T) {
	input, err := inputForPattern(map[string]thinkcore.FieldSchema{
		"issue": {Type: "string", Required: true},
	}, "visible work product")
	if err != nil {
		t.Fatalf("inputForPattern: %v", err)
	}
	if input["issue"] != "visible work product" {
		t.Fatalf("issue = %v", input["issue"])
	}
}

func TestInputForPatternUsesStructuredVisibleWorkProduct(t *testing.T) {
	input, err := inputForPattern(map[string]thinkcore.FieldSchema{
		"topic":    {Type: "string", Required: true},
		"findings": {Type: "array", Required: true},
	}, `{"topic":"cache invalidation","findings":["ttl is configured","writes are serialized"]}`)
	if err != nil {
		t.Fatalf("inputForPattern: %v", err)
	}
	if input["topic"] != "cache invalidation" {
		t.Fatalf("topic = %v", input["topic"])
	}
	findings, ok := input["findings"].([]any)
	if !ok || len(findings) != 2 {
		t.Fatalf("findings = %#v", input["findings"])
	}
}
