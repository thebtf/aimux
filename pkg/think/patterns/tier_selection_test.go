package patterns

import (
	"strings"
	"testing"
)

// TestSelectTier_KnownDomainLow: short auth text matches the "auth" domain template
// and has low complexity → SelectTier returns "basic".
func TestSelectTier_KnownDomainLow(t *testing.T) {
	text := "Design auth"
	got := SelectTier(text, false, "")
	if got != "basic" {
		t.Errorf("SelectTier(%q): want basic, got %q", text, got)
	}
}

// TestSelectTier_UnknownDomainLow: short text with no domain template match
// and low complexity → SelectTier returns "analysis".
func TestSelectTier_UnknownDomainLow(t *testing.T) {
	text := "Design quantum circuit"
	got := SelectTier(text, false, "")
	if got != "analysis" {
		t.Errorf("SelectTier(%q): want analysis, got %q", text, got)
	}
}

// TestSelectTier_HighWithSampling: long complex text with hasSampling=true
// results in "deep" tier.
func TestSelectTier_HighWithSampling(t *testing.T) {
	// Build text with >10 sentences to force complexity=="epic".
	sentences := []string{
		"We need to design a distributed system with multiple services.",
		"Each service must be independently deployable and scalable.",
		"The data layer requires sharding and replication strategies.",
		"Network partition tolerance must be considered in every design decision.",
		"We need to evaluate CAP theorem tradeoffs for each component.",
		"Consistency models must be chosen based on business requirements.",
		"The system should support blue-green deployments for zero downtime.",
		"Monitoring and observability need to be built in from the start.",
		"Security controls must span authentication, authorization, and encryption.",
		"Load balancing strategies differ between stateful and stateless services.",
		"We also need to handle backpressure and circuit breaker patterns.",
	}
	text := strings.Join(sentences, " ")
	got := SelectTier(text, true, "")
	if got != "deep" {
		t.Errorf("SelectTier(long text, hasSampling=true): want deep, got %q (complexity=%q)", got, estimateComplexity(text))
	}
}

// TestSelectTier_HighWithoutSampling: same long complex text but hasSampling=false
// falls back to "analysis" tier.
func TestSelectTier_HighWithoutSampling(t *testing.T) {
	sentences := []string{
		"We need to design a distributed system with multiple services.",
		"Each service must be independently deployable and scalable.",
		"The data layer requires sharding and replication strategies.",
		"Network partition tolerance must be considered in every design decision.",
		"We need to evaluate CAP theorem tradeoffs for each component.",
		"Consistency models must be chosen based on business requirements.",
		"The system should support blue-green deployments for zero downtime.",
		"Monitoring and observability need to be built in from the start.",
		"Security controls must span authentication, authorization, and encryption.",
		"Load balancing strategies differ between stateful and stateless services.",
		"We also need to handle backpressure and circuit breaker patterns.",
	}
	text := strings.Join(sentences, " ")
	got := SelectTier(text, false, "")
	if got != "analysis" {
		t.Errorf("SelectTier(long text, hasSampling=false): want analysis, got %q (complexity=%q)", got, estimateComplexity(text))
	}
}

// TestSelectTier_ExplicitOverride: explicit depth parameter overrides complexity analysis.
func TestSelectTier_ExplicitOverride(t *testing.T) {
	// Simple text that would normally resolve to "basic", but explicit="deep".
	text := "Design auth"
	got := SelectTier(text, false, "deep")
	if got != "deep" {
		t.Errorf("SelectTier(%q, explicitDepth=deep): want deep, got %q", text, got)
	}
}
