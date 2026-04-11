package guidance

import (
	"fmt"
	"sort"
	"sync"
)

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
