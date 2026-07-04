package api

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/types"
)

// KeyResolver returns the API key for a provider. It is injected so Swarm
// wiring can preserve per-tenant/per-session key isolation outside this package.
type KeyResolver func(provider string) (string, error)

// SwarmFactory adapts Swarm executor names of the form
// "api:<provider>:<model>" into API ExecutorV2 instances.
type SwarmFactory struct {
	resolveKey KeyResolver
	opts       []Option
}

// NewSwarmFactory creates a SwarmFactory with the provider-key resolver and
// optional executor defaults (timeout, retries, cooldown, base URL, key injector).
func NewSwarmFactory(resolveKey KeyResolver, opts ...Option) *SwarmFactory {
	copied := make([]Option, len(opts))
	copy(copied, opts)
	return &SwarmFactory{resolveKey: resolveKey, opts: copied}
}

// Create parses an API Swarm executor name, resolves the provider key, and
// constructs the matching API executor.
func (f *SwarmFactory) Create(name string) (types.ExecutorV2, error) {
	if f == nil {
		return nil, fmt.Errorf("api swarm factory: factory is nil")
	}
	if f.resolveKey == nil {
		return nil, fmt.Errorf("api swarm factory: key resolver is nil")
	}

	provider, model, err := parseSwarmExecutorName(name)
	if err != nil {
		return nil, err
	}

	apiKey, err := f.resolveKey(provider)
	if err != nil {
		return nil, fmt.Errorf("api swarm factory: resolve key for %q: %w", provider, err)
	}

	cfg := Config{
		Provider: provider,
		APIKey:   apiKey,
		Model:    model,
	}
	applyFactoryConfigDefaults(&cfg, f.opts)

	exec, err := NewFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	applyExecutorOptions(exec, f.opts)
	return exec, nil
}

// CompositeFactory routes api:* executor names to the API factory and everything
// else to the CLI factory.
func CompositeFactory(cli func(string) (types.ExecutorV2, error), apiFactory *SwarmFactory) func(string) (types.ExecutorV2, error) {
	return func(name string) (types.ExecutorV2, error) {
		if strings.HasPrefix(name, "api:") {
			if apiFactory == nil {
				return nil, fmt.Errorf("api swarm factory: api executor requested for %q but api factory is nil", name)
			}
			return apiFactory.Create(name)
		}
		if cli == nil {
			return nil, fmt.Errorf("api swarm factory: cli factory is nil")
		}
		return cli(name)
	}
}

func parseSwarmExecutorName(name string) (string, string, error) {
	parts := strings.SplitN(strings.TrimSpace(name), ":", 3)
	if len(parts) != 3 || parts[0] != "api" {
		return "", "", fmt.Errorf("api swarm factory: executor name %q must match %q", name, "api:<provider>:<model>")
	}

	provider := strings.ToLower(strings.TrimSpace(parts[1]))
	model := strings.TrimSpace(parts[2])
	if provider == "" || model == "" {
		return "", "", fmt.Errorf("api swarm factory: executor name %q must match %q", name, "api:<provider>:<model>")
	}
	return provider, model, nil
}

func applyFactoryConfigDefaults(cfg *Config, opts []Option) {
	if cfg == nil || len(opts) == 0 {
		return
	}

	probe := &baseExecutor{timeout: DefaultTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt(probe)
		}
	}

	cfg.Timeout = probe.timeout
	cfg.BaseURL = probe.baseURL
	cfg.MaxRetries = probe.maxRetries
	cfg.Cooldown = probe.cooldown
}

func applyExecutorOptions(exec types.ExecutorV2, opts []Option) {
	if exec == nil || len(opts) == 0 {
		return
	}

	var base *baseExecutor
	switch e := exec.(type) {
	case *OpenAIExecutor:
		base = e.base
	case *AnthropicExecutor:
		base = e.base
	case *GoogleAIExecutor:
		base = e.base
	default:
		return
	}

	for _, opt := range opts {
		if opt != nil {
			opt(base)
		}
	}
}
