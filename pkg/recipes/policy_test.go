package recipes

import "testing"

func TestValidatePolicyPassesInitialReadOnlyRecipes(t *testing.T) {
	profile := ProviderCapabilities{
		SelectedCLI:      "codex",
		TaskClass:        TaskClassReview,
		OutputFormat:     "jsonl",
		ReadOnly:         true,
		SupportedSandbox: []string{"read-only"},
	}

	for _, recipe := range List() {
		result := ValidatePolicy(recipe, profile)
		if !result.OK {
			t.Fatalf("ValidatePolicy(%s) OK=false; missing=%v supported=%v", recipe.ID, result.MissingCapabilities, result.SupportedCapabilities)
		}
		if result.RecipeID != recipe.ID {
			t.Fatalf("RecipeID = %q, want %q", result.RecipeID, recipe.ID)
		}
		if result.SelectedCLI != "codex" {
			t.Fatalf("SelectedCLI = %q, want codex", result.SelectedCLI)
		}
		if !containsString(result.RequestedPolicy, "read_only") {
			t.Fatalf("requested policy lacks read_only: %#v", result.RequestedPolicy)
		}
		if !containsString(result.SupportedCapabilities, "structured_output.jsonl") {
			t.Fatalf("supported capabilities lacks structured_output.jsonl: %#v", result.SupportedCapabilities)
		}
	}
}

func TestValidatePolicyFailsUnsupportedPolicyFamilies(t *testing.T) {
	base := Recipe{
		ID:        "test-policy",
		TaskClass: TaskClassReview,
		ReadOnly:  true,
	}
	profile := ProviderCapabilities{
		SelectedCLI:  "limited",
		TaskClass:    TaskClassReview,
		OutputFormat: "text",
		ReadOnly:     false,
		Version:      "5.15.0",
	}

	tests := []struct {
		name    string
		need    string
		missing string
	}{
		{name: "read only", need: "read_only", missing: "read_only"},
		{name: "structured schema", need: "schema:json", missing: "schema.json"},
		{name: "sandbox", need: "sandbox:workspace-write", missing: "sandbox.workspace-write"},
		{name: "approval", need: "approval:on-request", missing: "approval.on-request"},
		{name: "max turns", need: "max_turns:3", missing: "max_turns.3"},
		{name: "version", need: "version:>=5.16.0", missing: "version.>=5.16.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recipe := base
			recipe.PolicyNeeds = []string{tt.need}
			result := ValidatePolicy(recipe, profile)
			if result.OK {
				t.Fatalf("ValidatePolicy(%q) OK=true, want false", tt.need)
			}
			if !containsString(result.MissingCapabilities, tt.missing) {
				t.Fatalf("missing capabilities = %#v, want %q", result.MissingCapabilities, tt.missing)
			}
			if result.Retryable {
				t.Fatalf("Retryable = true, want false")
			}
		})
	}
}

func TestValidatePolicyReturnsCopies(t *testing.T) {
	recipe := Recipe{
		ID:          "copy-check",
		TaskClass:   TaskClassReview,
		PolicyNeeds: []string{"read_only"},
	}
	profile := ProviderCapabilities{
		SelectedCLI:      "codex",
		TaskClass:        TaskClassReview,
		OutputFormat:     "json",
		ReadOnly:         true,
		SupportedSandbox: []string{"read-only"},
	}

	result := ValidatePolicy(recipe, profile)
	result.RequestedPolicy[0] = "mutated"
	result.SupportedCapabilities[0] = "mutated"

	again := ValidatePolicy(recipe, profile)
	if again.RequestedPolicy[0] == "mutated" {
		t.Fatalf("RequestedPolicy returned mutable backing array")
	}
	if again.SupportedCapabilities[0] == "mutated" {
		t.Fatalf("SupportedCapabilities returned mutable backing array")
	}
}
