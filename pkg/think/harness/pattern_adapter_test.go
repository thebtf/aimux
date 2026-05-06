package harness

import (
	"context"
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

func TestInputForPatternStructuredWorkProductFailsClosedWhenRequiredKeyMissing(t *testing.T) {
	fields := map[string]thinkcore.FieldSchema{
		"topic":    {Type: "string", Required: true},
		"findings": {Type: "array", Required: true},
	}

	_, err := inputForPattern(fields, `{"findings":["ttl is configured"]}`)
	if err == nil {
		t.Fatal("structured payload missing required topic was accepted")
	}
	if !strings.Contains(err.Error(), "topic") {
		t.Fatalf("error = %q, want missing topic", err)
	}
}

func TestPatternAdapterSkipsPostCheckArtifactsWithoutPatternSession(t *testing.T) {
	execution, err := (PatternAdapter{}).Execute(context.Background(), CognitiveMove{
		Name:    "critical_thinking",
		Pattern: "critical_thinking",
	}, "visible claim", "harness-session")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, entry := range execution.LedgerAdds.Checkable {
		if entry.ID == "pattern_gate" || entry.ID == "pattern_advisor" {
			t.Fatalf("post-check ledger entry emitted without pattern session: %+v", entry)
		}
	}
	for _, factor := range execution.ConfidenceFactors {
		if factor.Name == "pattern_gate" || factor.Name == "pattern_advisor" {
			t.Fatalf("post-check confidence factor emitted without pattern session: %+v", factor)
		}
	}
}
