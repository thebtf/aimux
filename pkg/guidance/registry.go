package guidance

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

var guidedTools = []string{
	"investigate",
	"think",
	"consensus",
	"debate",
	"dialog",
	"workflow",
}

// Registry stores guidance policies by tool name.
type Registry struct {
	mu       sync.RWMutex
	policies map[string]ToolPolicy
}

// NewRegistry creates an empty policy registry.
func NewRegistry() *Registry {
	return &Registry{policies: make(map[string]ToolPolicy)}
}

// Register adds a policy. It rejects nil policies and duplicate tool registrations.
func (r *Registry) Register(policy ToolPolicy) error {
	if policy == nil {
		return fmt.Errorf("guidance: policy is nil")
	}

	tool := policy.ToolName()
	if tool == "" {
		return fmt.Errorf("guidance: policy tool name is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.policies[tool]; exists {
		return fmt.Errorf("guidance: duplicate policy registration for tool %q", tool)
	}
	r.policies[tool] = policy
	return nil
}

// Get retrieves a policy by tool name.
func (r *Registry) Get(tool string) (ToolPolicy, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.policies[tool]
	return p, ok
}

// MustGet retrieves a policy by tool name or returns an error.
func (r *Registry) MustGet(tool string) (ToolPolicy, error) {
	p, ok := r.Get(tool)
	if !ok {
		return nil, fmt.Errorf("guidance: policy not found for tool %q", tool)
	}
	return p, nil
}

// Resolve returns a registered policy when available.
// If missing in development mode, it returns a loud error.
// If missing in production mode, it returns a minimal fallback envelope.
func (r *Registry) Resolve(tool string, rawResponse any) (ToolPolicy, *ResponseEnvelope, error) {
	if policy, ok := r.Get(tool); ok {
		return policy, nil, nil
	}

	if IsDevelopmentMode() {
		return nil, nil, missingPolicyError(tool)
	}

	fallback := NewMissingPolicyEnvelope(rawResponse)
	return nil, &fallback, nil
}

// GuidedTools returns the canonical list of tools that should have guidance policies.
func GuidedTools() []string {
	tools := make([]string, len(guidedTools))
	copy(tools, guidedTools)
	return tools
}

// MissingGuidedTools returns canonical guided tools that are not yet registered.
func (r *Registry) MissingGuidedTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	missing := make([]string, 0)
	for _, tool := range guidedTools {
		if _, ok := r.policies[tool]; !ok {
			missing = append(missing, tool)
		}
	}
	return missing
}

// ValidateRequiredPolicies reports missing guided tool policies.
// In development mode this should be called during startup/registration to fail fast.
func (r *Registry) ValidateRequiredPolicies() error {
	missing := r.MissingGuidedTools()
	if len(missing) == 0 {
		return nil
	}
	if !IsDevelopmentMode() {
		return nil
	}
	return fmt.Errorf("guidance: missing policies for tools: %s", strings.Join(missing, ", "))
}

func missingPolicyError(tool string) error {
	return fmt.Errorf("guidance: missing policy for tool %q (implement pkg/guidance/policies/%s.go)", tool, tool)
}

// IsDevelopmentMode returns true when guidance should fail loudly for missing policies.
func IsDevelopmentMode() bool {
	return buildTaggedDevelopment || strings.EqualFold(strings.TrimSpace(os.Getenv("AIMUX_ENV")), "development")
}

// ListTools returns all registered tool names sorted for deterministic behavior.
func (r *Registry) ListTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]string, 0, len(r.policies))
	for tool := range r.policies {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	return tools
}

// Count returns the number of registered policies.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.policies)
}
