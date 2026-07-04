package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/types"
)

type stubExecutor struct {
	name   string
	alive  types.HealthStatus
	closed bool
}

func (s *stubExecutor) Info() types.ExecutorInfo {
	return types.ExecutorInfo{Name: s.name, Type: types.ExecutorTypeCLI}
}

func (s *stubExecutor) Send(_ context.Context, _ types.Message) (*types.Response, error) {
	return &types.Response{Content: "ok", Duration: time.Millisecond}, nil
}

func (s *stubExecutor) SendStream(ctx context.Context, msg types.Message, onChunk func(types.Chunk)) (*types.Response, error) {
	resp, err := s.Send(ctx, msg)
	if err != nil {
		return nil, err
	}
	onChunk(types.Chunk{Content: resp.Content, Done: true})
	return resp, nil
}

func (s *stubExecutor) IsAlive() types.HealthStatus {
	if s.closed {
		return types.HealthDead
	}
	return s.alive
}

func (s *stubExecutor) Close() error {
	s.closed = true
	return nil
}

func TestSwarmFactoryCreate_ValidProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		executorName string
		provider     string
		model        string
	}{
		{name: "openai", executorName: "api:openai:gpt-4o", provider: "openai", model: "gpt-4o"},
		{name: "anthropic", executorName: "api:anthropic:claude-sonnet-4-5-20250929", provider: "anthropic", model: "claude-sonnet-4-5-20250929"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotProvider string
			factory := NewSwarmFactory(func(provider string) (string, error) {
				gotProvider = provider
				return "test-key", nil
			}, WithTimeout(2*time.Second), WithMaxRetries(3))

			exec, err := factory.Create(tt.executorName)
			if err != nil {
				t.Fatalf("Create(%q): %v", tt.executorName, err)
			}
			if gotProvider != tt.provider {
				t.Fatalf("resolver provider = %q, want %q", gotProvider, tt.provider)
			}

			info := exec.Info()
			if info.Name != tt.provider {
				t.Fatalf("Info().Name = %q, want %q", info.Name, tt.provider)
			}
			if got := exec.IsAlive(); got != types.HealthAlive {
				t.Fatalf("IsAlive() = %v, want %v", got, types.HealthAlive)
			}
			if err := exec.Close(); err != nil {
				t.Fatalf("Close(): %v", err)
			}
			if got := exec.IsAlive(); got != types.HealthDead {
				t.Fatalf("IsAlive() after Close = %v, want %v", got, types.HealthDead)
			}
		})
	}
}

func TestSwarmFactoryCreate_RejectsMalformedName(t *testing.T) {
	t.Parallel()

	factory := NewSwarmFactory(func(provider string) (string, error) {
		return "test-key", nil
	})

	_, err := factory.Create("not-api-prefix")
	if err == nil {
		t.Fatal("Create(not-api-prefix): expected error, got nil")
	}
}

func TestSwarmFactoryCreate_UnknownProvider(t *testing.T) {
	t.Parallel()

	factory := NewSwarmFactory(func(provider string) (string, error) {
		return "test-key", nil
	})

	_, err := factory.Create("api:unknown:model")
	if err == nil {
		t.Fatal("Create(api:unknown:model): expected error, got nil")
	}
}

func TestSwarmFactoryCreate_ResolverErrorPropagates(t *testing.T) {
	t.Parallel()

	factory := NewSwarmFactory(func(provider string) (string, error) {
		return "", errors.New("resolver exploded")
	})

	_, err := factory.Create("api:openai:gpt-4o")
	if err == nil {
		t.Fatal("Create(api:openai:gpt-4o): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "resolver exploded") {
		t.Fatalf("resolver error = %v", err)
	}
}

func TestCompositeFactory_RoutesAPIAndCLI(t *testing.T) {
	t.Parallel()

	cliCalls := 0
	cliExec := &stubExecutor{name: "codex", alive: types.HealthAlive}
	cliFactory := func(name string) (types.ExecutorV2, error) {
		cliCalls++
		if name != "codex" {
			t.Fatalf("cli factory name = %q, want codex", name)
		}
		return cliExec, nil
	}

	apiCalls := 0
	apiFactory := NewSwarmFactory(func(provider string) (string, error) {
		apiCalls++
		if provider != "openai" {
			t.Fatalf("resolver provider = %q, want openai", provider)
		}
		return "test-key", nil
	})

	composite := CompositeFactory(cliFactory, apiFactory)

	apiExec, err := composite("api:openai:gpt-4o")
	if err != nil {
		t.Fatalf("composite(api): %v", err)
	}
	if apiExec.Info().Name != "openai" {
		t.Fatalf("api Info().Name = %q, want openai", apiExec.Info().Name)
	}
	if cliCalls != 0 {
		t.Fatalf("cliCalls after api route = %d, want 0", cliCalls)
	}
	if apiCalls != 1 {
		t.Fatalf("apiCalls after api route = %d, want 1", apiCalls)
	}

	plainExec, err := composite("codex")
	if err != nil {
		t.Fatalf("composite(cli): %v", err)
	}
	if plainExec.Info().Name != cliExec.name {
		t.Fatalf("plain Info().Name = %q, want %q", plainExec.Info().Name, cliExec.name)
	}
	if cliCalls != 1 {
		t.Fatalf("cliCalls after cli route = %d, want 1", cliCalls)
	}
}

func TestCompositeFactory_NilAPIFactoryRejectsAPIName(t *testing.T) {
	t.Parallel()

	composite := CompositeFactory(func(name string) (types.ExecutorV2, error) {
		return &stubExecutor{name: name, alive: types.HealthAlive}, nil
	}, nil)

	_, err := composite("api:openai:gpt-4o")
	if err == nil {
		t.Fatal("composite(api) with nil apiFactory: expected error, got nil")
	}
}
