package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/tenant"
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
			var gotTenant string
			factory := NewSwarmFactory(func(ctx context.Context, provider, tenantID string) (string, error) {
				if ctx == nil {
					t.Fatal("resolver ctx is nil")
				}
				gotProvider = provider
				gotTenant = tenantID
				return "test-key", nil
			}, WithTimeout(2*time.Second), WithMaxRetries(3))

			exec, err := factory.Create(tt.executorName)
			if err != nil {
				t.Fatalf("Create(%q): %v", tt.executorName, err)
			}
			if gotProvider != tt.provider {
				t.Fatalf("resolver provider = %q, want %q", gotProvider, tt.provider)
			}
			if gotTenant != "" {
				t.Fatalf("resolver tenantID = %q, want empty for Create without tenant ctx", gotTenant)
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

func TestSwarmFactoryCreateWithContext_UsesTenantSpecificKey(t *testing.T) {
	t.Parallel()

	type ctxLabelKey struct{}
	type resolverCall struct {
		provider string
		tenantID string
		label    string
	}

	ctxA := tenant.WithContext(
		context.WithValue(context.Background(), ctxLabelKey{}, "session-a"),
		tenant.TenantContext{TenantID: "tenantA"},
	)
	ctxB := tenant.WithContext(
		context.WithValue(context.Background(), ctxLabelKey{}, "session-b"),
		tenant.TenantContext{TenantID: "tenantB"},
	)

	var calls []resolverCall
	factory := NewSwarmFactory(func(ctx context.Context, provider, tenantID string) (string, error) {
		label, _ := ctx.Value(ctxLabelKey{}).(string)
		calls = append(calls, resolverCall{provider: provider, tenantID: tenantID, label: label})
		return "test-key-" + tenantID + "-" + provider, nil
	})

	execA, err := factory.CreateWithContext(ctxA, "api:openai:gpt-4o")
	if err != nil {
		t.Fatalf("CreateWithContext(ctxA): %v", err)
	}
	execB, err := factory.CreateWithContext(ctxB, "api:openai:gpt-4o")
	if err != nil {
		t.Fatalf("CreateWithContext(ctxB): %v", err)
	}

	openA, ok := execA.(*OpenAIExecutor)
	if !ok {
		t.Fatalf("execA type = %T, want *OpenAIExecutor", execA)
	}
	openB, ok := execB.(*OpenAIExecutor)
	if !ok {
		t.Fatalf("execB type = %T, want *OpenAIExecutor", execB)
	}
	if openA.base.apiKey != "test-key-tenantA-openai" {
		t.Fatalf("tenantA apiKey = %q, want %q", openA.base.apiKey, "test-key-tenantA-openai")
	}
	if openB.base.apiKey != "test-key-tenantB-openai" {
		t.Fatalf("tenantB apiKey = %q, want %q", openB.base.apiKey, "test-key-tenantB-openai")
	}
	if openA.base.apiKey == openB.base.apiKey {
		t.Fatal("tenant-specific api keys collapsed to the same value")
	}
	if len(calls) != 2 {
		t.Fatalf("resolver calls = %d, want 2", len(calls))
	}
	if calls[0] != (resolverCall{provider: "openai", tenantID: "tenantA", label: "session-a"}) {
		t.Fatalf("call[0] = %+v", calls[0])
	}
	if calls[1] != (resolverCall{provider: "openai", tenantID: "tenantB", label: "session-b"}) {
		t.Fatalf("call[1] = %+v", calls[1])
	}
}

func TestSwarmFactoryCreate_RejectsMalformedName(t *testing.T) {
	t.Parallel()

	factory := NewSwarmFactory(func(context.Context, string, string) (string, error) {
		return "test-key", nil
	})

	_, err := factory.Create("not-api-prefix")
	if err == nil {
		t.Fatal("Create(not-api-prefix): expected error, got nil")
	}
}

func TestSwarmFactoryCreate_UnknownProvider(t *testing.T) {
	t.Parallel()

	factory := NewSwarmFactory(func(context.Context, string, string) (string, error) {
		return "test-key", nil
	})

	_, err := factory.Create("api:unknown:model")
	if err == nil {
		t.Fatal("Create(api:unknown:model): expected error, got nil")
	}
}

func TestSwarmFactoryCreate_ResolverErrorPropagates(t *testing.T) {
	t.Parallel()

	factory := NewSwarmFactory(func(context.Context, string, string) (string, error) {
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
	apiFactory := NewSwarmFactory(func(_ context.Context, provider, tenantID string) (string, error) {
		apiCalls++
		if provider != "openai" {
			t.Fatalf("resolver provider = %q, want openai", provider)
		}
		if tenantID != "" {
			t.Fatalf("resolver tenantID = %q, want empty", tenantID)
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

func TestContextCompositeFactory_RoutesTenantAwareAPI(t *testing.T) {
	t.Parallel()

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenantA"})
	cliExec := &stubExecutor{name: "codex", alive: types.HealthAlive}
	cliFactory := func(name string) (types.ExecutorV2, error) {
		return cliExec, nil
	}
	apiFactory := NewSwarmFactory(func(_ context.Context, provider, tenantID string) (string, error) {
		if provider != "openai" {
			t.Fatalf("resolver provider = %q, want openai", provider)
		}
		if tenantID != "tenantA" {
			t.Fatalf("resolver tenantID = %q, want tenantA", tenantID)
		}
		return "test-key-" + tenantID, nil
	})

	composite := ContextCompositeFactory(cliFactory, apiFactory)
	apiExec, err := composite(ctx, "api:openai:gpt-4o")
	if err != nil {
		t.Fatalf("context composite(api): %v", err)
	}
	openAI, ok := apiExec.(*OpenAIExecutor)
	if !ok {
		t.Fatalf("apiExec type = %T, want *OpenAIExecutor", apiExec)
	}
	if openAI.base.apiKey != "test-key-tenantA" {
		t.Fatalf("api key = %q, want %q", openAI.base.apiKey, "test-key-tenantA")
	}

	plainExec, err := composite(ctx, "codex")
	if err != nil {
		t.Fatalf("context composite(cli): %v", err)
	}
	if plainExec.Info().Name != cliExec.name {
		t.Fatalf("plain Info().Name = %q, want %q", plainExec.Info().Name, cliExec.name)
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

func TestSwarmFactoryCreateWithContext_CanonicalizesLegacyDefaultTenant(t *testing.T) {
	t.Parallel()

	type resolverCall struct {
		provider string
		tenantID string
	}

	var calls []resolverCall
	factory := NewSwarmFactory(func(_ context.Context, provider, tenantID string) (string, error) {
		calls = append(calls, resolverCall{provider: provider, tenantID: tenantID})
		return "test-key-" + tenantID + "-" + provider, nil
	})

	emptyExec, err := factory.CreateWithContext(context.Background(), "api:openai:gpt-4o")
	if err != nil {
		t.Fatalf("CreateWithContext(empty): %v", err)
	}
	legacyCtx := tenant.WithContext(context.Background(), tenant.NewLegacyDefaultContext("session-legacy"))
	legacyExec, err := factory.CreateWithContext(legacyCtx, "api:openai:gpt-4o")
	if err != nil {
		t.Fatalf("CreateWithContext(legacy): %v", err)
	}

	emptyOpenAI, ok := emptyExec.(*OpenAIExecutor)
	if !ok {
		t.Fatalf("empty exec type = %T, want *OpenAIExecutor", emptyExec)
	}
	legacyOpenAI, ok := legacyExec.(*OpenAIExecutor)
	if !ok {
		t.Fatalf("legacy exec type = %T, want *OpenAIExecutor", legacyExec)
	}
	if len(calls) != 2 {
		t.Fatalf("resolver calls = %d, want 2", len(calls))
	}
	if calls[0] != (resolverCall{provider: "openai", tenantID: ""}) {
		t.Fatalf("call[0] = %+v", calls[0])
	}
	if calls[1] != (resolverCall{provider: "openai", tenantID: ""}) {
		t.Fatalf("call[1] = %+v", calls[1])
	}
	if emptyOpenAI.base.apiKey != legacyOpenAI.base.apiKey {
		t.Fatalf("legacy canonicalization mismatch: empty apiKey %q != legacy apiKey %q", emptyOpenAI.base.apiKey, legacyOpenAI.base.apiKey)
	}
}

func TestApplyExecutorOptions_NilBaseNoop(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("applyExecutorOptions panicked with nil base: %v", r)
		}
	}()

	applyExecutorOptions(&OpenAIExecutor{}, []Option{WithTimeout(time.Second), WithBaseURL("https://example.invalid")})
}
