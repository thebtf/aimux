package review

import "testing"

func TestAggregatorDeduplicatesThreePassOverlap(t *testing.T) {
	line := 12
	results := []PassResult{
		{
			Name:    PassStructural,
			Summary: "structural found duplicate",
			Findings: []Finding{
				{Severity: SeverityWarning, File: "pkg/a.go", Line: &line, Body: "extract helper"},
			},
		},
		{
			Name:    PassBehavioural,
			Summary: "behavioural found duplicate",
			Findings: []Finding{
				{Severity: SeverityWarning, File: "pkg/a.go", Line: &line, Body: "extract helper"},
			},
		},
		{
			Name:    PassAdversarial,
			Summary: "adversarial found unique",
			Findings: []Finding{
				{Severity: SeverityError, File: "pkg/b.go", Body: "path traversal is possible"},
			},
		},
	}

	aggregated := Aggregator{}.Aggregate(results)
	if len(aggregated.Findings) != 2 {
		t.Fatalf("findings count = %d, want 2 unique findings: %#v", len(aggregated.Findings), aggregated.Findings)
	}
	if len(aggregated.PassesCompleted) != 3 {
		t.Fatalf("passes_completed = %#v, want 3 passes", aggregated.PassesCompleted)
	}
	if aggregated.Severity != SeverityError {
		t.Fatalf("severity = %s, want %s", aggregated.Severity, SeverityError)
	}
	if !aggregated.Blocking {
		t.Fatal("Blocking = false, want true when any pass marks error")
	}
}

func TestAggregatorAllowVsBlockThreshold(t *testing.T) {
	allow := Aggregator{}.Aggregate([]PassResult{
		{Name: PassStructural, Summary: "info only", Findings: []Finding{
			{Severity: SeverityInfo, Body: "naming is clear"},
		}},
		{Name: PassBehavioural, Summary: "warning only", Findings: []Finding{
			{Severity: SeverityWarning, Body: "missing edge case assertion"},
		}},
	})
	if allow.Severity != SeverityWarning {
		t.Fatalf("allow severity = %s, want %s", allow.Severity, SeverityWarning)
	}
	if allow.Blocking {
		t.Fatal("Blocking = true, want false without error findings")
	}

	block := Aggregator{}.Aggregate([]PassResult{
		{Name: PassAdversarial, Summary: "error", Findings: []Finding{
			{Severity: SeverityError, Body: "secret may be logged"},
		}},
	})
	if block.Severity != SeverityError {
		t.Fatalf("block severity = %s, want %s", block.Severity, SeverityError)
	}
	if !block.Blocking {
		t.Fatal("Blocking = false, want true with error finding")
	}
}

func TestAggregatorHandlesEmptyPassResult(t *testing.T) {
	aggregated := Aggregator{}.Aggregate([]PassResult{
		{Name: PassStructural},
		{Name: PassBehavioural, Summary: "no behavioural issues"},
	})
	if len(aggregated.Findings) != 0 {
		t.Fatalf("findings count = %d, want 0", len(aggregated.Findings))
	}
	if len(aggregated.PassesCompleted) != 2 {
		t.Fatalf("passes_completed = %#v, want both pass names", aggregated.PassesCompleted)
	}
	if aggregated.Severity != "" {
		t.Fatalf("severity = %s, want empty severity", aggregated.Severity)
	}
	if aggregated.Summary == "" {
		t.Fatal("summary is empty, want fallback summary")
	}
}
