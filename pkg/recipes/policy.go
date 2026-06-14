package recipes

import (
	"strconv"
	"strings"
)

const (
	PolicyReadOnly               = "read_only"
	PolicyStructuredReviewOutput = "structured_review_output"
	PolicyTargetRequired         = "target_required"
)

type ProviderCapabilities struct {
	SelectedCLI      string
	TaskClass        string
	OutputFormat     string
	ReadOnly         bool
	SupportedSandbox []string
	ApprovalModes    []string
	SchemaModes      []string
	MaxTurns         int
	Version          string
}

type PolicyValidationResult struct {
	OK                    bool     `json:"ok"`
	Retryable             bool     `json:"retryable"`
	RecipeID              string   `json:"recipe_id"`
	SelectedCLI           string   `json:"selected_cli"`
	RequestedPolicy       []string `json:"requested_policy"`
	MissingCapabilities   []string `json:"missing_capabilities,omitempty"`
	SupportedCapabilities []string `json:"supported_capabilities"`
}

func ValidatePolicy(recipe Recipe, provider ProviderCapabilities) PolicyValidationResult {
	requested := normalizedPolicyNeeds(recipe.PolicyNeeds)
	supported := supportedCapabilities(provider)
	missing := make([]string, 0)
	for _, policy := range requested {
		if miss, ok := missingCapability(policy, provider, supported); !ok {
			missing = append(missing, miss)
		}
	}
	return PolicyValidationResult{
		OK:                    len(missing) == 0,
		Retryable:             false,
		RecipeID:              recipe.ID,
		SelectedCLI:           strings.TrimSpace(provider.SelectedCLI),
		RequestedPolicy:       cloneStrings(requested),
		MissingCapabilities:   cloneStrings(missing),
		SupportedCapabilities: cloneStrings(supported),
	}
}

func normalizedPolicyNeeds(needs []string) []string {
	out := make([]string, 0, len(needs))
	for _, need := range needs {
		normalized := normalizePolicyID(need)
		if normalized == "" {
			continue
		}
		out = append(out, normalized)
	}
	return out
}

func normalizePolicyID(need string) string {
	normalized := strings.ToLower(strings.TrimSpace(need))
	normalized = strings.ReplaceAll(normalized, " ", "_")
	if normalized == "" {
		return ""
	}
	if strings.Contains(normalized, ":") {
		parts := strings.SplitN(normalized, ":", 2)
		if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return normalized
		}
		return strings.TrimSpace(parts[0]) + "." + strings.TrimSpace(parts[1])
	}
	return normalized
}

func supportedCapabilities(provider ProviderCapabilities) []string {
	caps := make([]string, 0, 8)
	if provider.ReadOnly || containsFold(provider.SupportedSandbox, "read-only") {
		caps = append(caps, PolicyReadOnly)
	}
	for _, sandbox := range normalizedList(provider.SupportedSandbox) {
		caps = append(caps, "sandbox."+sandbox)
	}
	switch format := strings.ToLower(strings.TrimSpace(provider.OutputFormat)); format {
	case "json", "jsonl":
		caps = append(caps, "structured_output."+format)
		caps = append(caps, "schema."+format)
	}
	for _, mode := range normalizedList(provider.ApprovalModes) {
		caps = append(caps, "approval."+mode)
	}
	for _, mode := range normalizedList(provider.SchemaModes) {
		caps = append(caps, "schema."+mode)
	}
	if provider.MaxTurns > 0 {
		caps = append(caps, "max_turns."+strconv.Itoa(provider.MaxTurns))
	}
	if version := strings.TrimSpace(provider.Version); version != "" {
		caps = append(caps, "version."+version)
	}
	caps = append(caps, PolicyTargetRequired)
	return uniqueStrings(caps)
}

func missingCapability(policy string, provider ProviderCapabilities, supported []string) (string, bool) {
	switch {
	case policy == PolicyReadOnly:
		return PolicyReadOnly, containsString(supported, PolicyReadOnly)
	case policy == PolicyStructuredReviewOutput || policy == "structured_output":
		return "structured_output", hasPrefix(supported, "structured_output.")
	case policy == PolicyTargetRequired:
		return PolicyTargetRequired, true
	case strings.HasPrefix(policy, "sandbox."):
		return policy, containsString(supported, policy)
	case strings.HasPrefix(policy, "approval."):
		return policy, containsString(supported, policy)
	case strings.HasPrefix(policy, "schema."):
		return policy, containsString(supported, policy)
	case strings.HasPrefix(policy, "max_turns."):
		required, ok := parsePositiveInt(strings.TrimPrefix(policy, "max_turns."))
		if !ok {
			return policy, false
		}
		return policy, provider.MaxTurns >= required
	case strings.HasPrefix(policy, "version.>="):
		required := strings.TrimPrefix(policy, "version.>=")
		return policy, versionAtLeast(provider.Version, required)
	case strings.HasPrefix(policy, "version."):
		required := strings.TrimPrefix(policy, "version.")
		return policy, strings.TrimSpace(provider.Version) == required
	default:
		return "policy." + policy, false
	}
}

func normalizedList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return uniqueStrings(out)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsFold(values []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == want {
			return true
		}
	}
	return false
}

func hasPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func parsePositiveInt(raw string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func versionAtLeast(current string, required string) bool {
	currentParts, ok := parseVersion(current)
	if !ok {
		return false
	}
	requiredParts, ok := parseVersion(required)
	if !ok {
		return false
	}
	for i := 0; i < len(currentParts) || i < len(requiredParts); i++ {
		currentPart := versionPart(currentParts, i)
		requiredPart := versionPart(requiredParts, i)
		if currentPart > requiredPart {
			return true
		}
		if currentPart < requiredPart {
			return false
		}
	}
	return true
}

func parseVersion(version string) ([]int, bool) {
	version = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(version)), "v")
	if version == "" {
		return nil, false
	}
	fields := strings.Split(version, ".")
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			return nil, false
		}
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return nil, false
		}
		parts = append(parts, n)
	}
	return parts, true
}

func versionPart(parts []int, i int) int {
	if i >= len(parts) {
		return 0
	}
	return parts[i]
}
