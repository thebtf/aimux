package swarm_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/thebtf/aimux/pkg/audit"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/tenant"
	"github.com/thebtf/aimux/pkg/types"
)

type liveBindingExecutor struct {
	mockExecutorV2
	identity types.SessionIdentity
}

func newLiveBindingExecutor(identity types.SessionIdentity, persistent bool) *liveBindingExecutor {
	e := &liveBindingExecutor{identity: identity}
	e.alive = types.HealthAlive
	e.info = types.ExecutorInfo{
		Name: "session-binding-mock",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			PersistentSessions: persistent,
		},
	}
	return e
}

func (e *liveBindingExecutor) StartSession(context.Context, types.SpawnArgs) (types.Session, error) {
	return &mockSession{alive: true}, nil
}

func (e *liveBindingExecutor) WithSession(types.Session) types.ExecutorV2 { return e }

func (e *liveBindingExecutor) SessionIdentity() types.SessionIdentity { return e.identity }

type opaqueLiveBindingExecutor struct {
	mockExecutorV2
}

func newOpaqueLiveBindingExecutor() *opaqueLiveBindingExecutor {
	e := &opaqueLiveBindingExecutor{}
	e.alive = types.HealthAlive
	e.info = types.ExecutorInfo{
		Name: "opaque-session-binding-mock",
		Type: types.ExecutorTypeCLI,
		Capabilities: types.ExecutorCapabilities{
			PersistentSessions: true,
		},
	}
	return e
}

func (e *opaqueLiveBindingExecutor) StartSession(context.Context, types.SpawnArgs) (types.Session, error) {
	return &mockSession{alive: true}, nil
}

func (e *opaqueLiveBindingExecutor) WithSession(types.Session) types.ExecutorV2 { return e }

type forkedIdentitySession struct {
	*mockSession
	identity types.SessionIdentity
}

type forkableSessionBindingExecutor struct {
	*liveBindingExecutor
	childIdentity types.SessionIdentity
	forkCalls     atomic.Int32
}

func (e *forkableSessionBindingExecutor) ForkSession(_ context.Context, _ types.SessionIdentity, _ types.SpawnArgs) (types.Session, error) {
	e.forkCalls.Add(1)
	return &forkedIdentitySession{mockSession: &mockSession{alive: true}, identity: e.childIdentity}, nil
}

func (e *forkableSessionBindingExecutor) WithSession(session types.Session) types.ExecutorV2 {
	forked, ok := session.(*forkedIdentitySession)
	if !ok {
		return e
	}
	return newLiveBindingExecutor(forked.identity, true)
}

type opaqueChildForkableExecutor struct {
	*liveBindingExecutor
	forkCalls atomic.Int32
}

func (e *opaqueChildForkableExecutor) ForkSession(_ context.Context, _ types.SessionIdentity, _ types.SpawnArgs) (types.Session, error) {
	e.forkCalls.Add(1)
	return &forkedIdentitySession{mockSession: &mockSession{alive: true}, identity: types.SessionIdentity{Provider: "neutral", ID: "opaque-child", Generation: 1}}, nil
}

func (e *opaqueChildForkableExecutor) WithSession(session types.Session) types.ExecutorV2 {
	if _, ok := session.(*forkedIdentitySession); ok {
		return newOpaqueLiveBindingExecutor()
	}
	return e
}

func bindingIdentity(t *testing.T, binding swarm.LiveSessionBinding) types.SessionBindingIdentity {
	t.Helper()
	if binding.ProviderSession == nil {
		t.Fatal("live binding missing ProviderSession")
	}
	return types.SessionBindingIdentity{
		HandleID:           binding.HandleID,
		HandleGeneration:   binding.HandleGeneration,
		RegistryGeneration: binding.RegistryGeneration,
		ProviderSession:    *binding.ProviderSession,
	}
}

func assertCompleteBinding(t *testing.T, binding swarm.LiveSessionBinding, scope string) {
	t.Helper()
	if binding.Scope != scope {
		t.Errorf("Scope = %q, want %q", binding.Scope, scope)
	}
	if binding.HandleID == "" {
		t.Error("HandleID is empty")
	}
	if binding.HandleGeneration == 0 {
		t.Error("HandleGeneration is zero")
	}
	if binding.RegistryGeneration == 0 {
		t.Error("RegistryGeneration is zero")
	}
	if binding.ProviderSession == nil {
		t.Fatal("ProviderSession is nil")
	}
	if binding.ProviderSession.Provider == "" || binding.ProviderSession.ID == "" || binding.ProviderSession.Generation == 0 {
		t.Errorf("ProviderSession = %+v, want complete identity", *binding.ProviderSession)
	}
}

func TestAcquireSessionBinding_StatelessAndNewAreDistinct(t *testing.T) {
	factory := func(string) (types.ExecutorV2, error) {
		ex := &noCapabilityExecutor{}
		ex.alive = types.HealthAlive
		return ex, nil
	}
	sw := swarm.New(factory, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	stateless, err := sw.AcquireSessionBinding(context.Background(), "agent", types.SessionBindingRequest{
		Mode: types.SessionBindingModeStateless,
	}, swarm.WithScope("stateless-scope"))
	if err != nil {
		t.Fatalf("Stateless: %v", err)
	}
	if stateless.Scope != "stateless-scope" {
		t.Errorf("stateless Scope = %q, want stateless-scope", stateless.Scope)
	}
	if stateless.ProviderSession != nil {
		t.Errorf("stateless ProviderSession = %+v, want nil", stateless.ProviderSession)
	}

	_, err = sw.AcquireSessionBinding(context.Background(), "agent-new", types.SessionBindingRequest{
		Mode: types.SessionBindingModeNew,
	}, swarm.WithScope("new-scope"))
	if err == nil {
		t.Fatal("New without session capability: want rejection")
	}
}

func TestAcquireSessionBinding_NewReturnsCompleteSnapshot(t *testing.T) {
	var created atomic.Int32
	sw := swarm.New(func(name string) (types.ExecutorV2, error) {
		created.Add(1)
		return newLiveBindingExecutor(types.SessionIdentity{Provider: "neutral", ID: fmt.Sprintf("%s-session", name), Generation: 1}, true), nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	binding, err := sw.AcquireSessionBinding(context.Background(), "agent", types.SessionBindingRequest{
		Mode: types.SessionBindingModeNew,
	}, swarm.WithScope("new-snapshot"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	assertCompleteBinding(t, binding, "new-snapshot")
	if created.Load() != 1 {
		t.Errorf("factory calls = %d, want 1", created.Load())
	}
}

func TestAcquireSessionBinding_NewPermitsMissingProviderSession(t *testing.T) {
	sw := swarm.New(func(string) (types.ExecutorV2, error) {
		return newOpaqueLiveBindingExecutor(), nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	binding, err := sw.AcquireSessionBinding(context.Background(), "agent", types.SessionBindingRequest{
		Mode: types.SessionBindingModeNew,
	}, swarm.WithScope("opaque-scope"))
	if err != nil {
		t.Fatalf("New without SessionIdentityProvider: %v", err)
	}
	if binding.Scope != "opaque-scope" || binding.HandleID == "" || binding.HandleGeneration == 0 || binding.RegistryGeneration == 0 {
		t.Errorf("binding = %+v, want safe complete handle snapshot", binding)
	}
	if binding.ProviderSession != nil {
		t.Errorf("ProviderSession = %+v, want nil", binding.ProviderSession)
	}
}

func TestAcquireSessionBinding_NewNeverReturnsCachedBinding(t *testing.T) {
	var created atomic.Int32
	sw := swarm.New(func(name string) (types.ExecutorV2, error) {
		id := created.Add(1)
		return newLiveBindingExecutor(types.SessionIdentity{Provider: "neutral", ID: fmt.Sprintf("%s-session-%d", name, id), Generation: 1}, true), nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-new"})
	first, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{Mode: types.SessionBindingModeNew}, swarm.WithScope("new-scope"))
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	second, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{Mode: types.SessionBindingModeNew}, swarm.WithScope("new-scope"))
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	if first.HandleID == second.HandleID {
		t.Errorf("New HandleID = %q twice, want distinct bindings", first.HandleID)
	}
	if first.ProviderSession == nil || second.ProviderSession == nil || *first.ProviderSession == *second.ProviderSession {
		t.Errorf("New ProviderSessions = %+v and %+v, want distinct identities", first.ProviderSession, second.ProviderSession)
	}
	if created.Load() != 2 {
		t.Errorf("factory calls = %d, want 2", created.Load())
	}
}

func TestAcquireSessionBinding_ExactResumeRequiresExactIdentity(t *testing.T) {
	var created atomic.Int32
	sw := swarm.New(func(name string) (types.ExecutorV2, error) {
		created.Add(1)
		return newLiveBindingExecutor(types.SessionIdentity{Provider: "neutral", ID: fmt.Sprintf("%s-session", name), Generation: 1}, true), nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	baseCtx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-a"})
	baseline, err := sw.AcquireSessionBinding(baseCtx, "agent", types.SessionBindingRequest{
		Mode: types.SessionBindingModeNew,
	}, swarm.WithScope("resume-scope"))
	if err != nil {
		t.Fatalf("New baseline: %v", err)
	}
	assertCompleteBinding(t, baseline, "resume-scope")
	expected := bindingIdentity(t, baseline)
	created.Store(0)

	otherTenant := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-b"})
	cases := []struct {
		name   string
		ctx    context.Context
		scope  string
		mutate func(*types.SessionBindingIdentity)
	}{
		{name: "tenant", ctx: otherTenant, scope: "resume-scope", mutate: func(*types.SessionBindingIdentity) {}},
		{name: "scope", ctx: baseCtx, scope: "other-scope", mutate: func(*types.SessionBindingIdentity) {}},
		{name: "handle ID", ctx: baseCtx, scope: "resume-scope", mutate: func(id *types.SessionBindingIdentity) { id.HandleID += "-other" }},
		{name: "handle generation", ctx: baseCtx, scope: "resume-scope", mutate: func(id *types.SessionBindingIdentity) { id.HandleGeneration++ }},
		{name: "registry generation", ctx: baseCtx, scope: "resume-scope", mutate: func(id *types.SessionBindingIdentity) { id.RegistryGeneration++ }},
		{name: "provider", ctx: baseCtx, scope: "resume-scope", mutate: func(id *types.SessionBindingIdentity) { id.ProviderSession.Provider += "-other" }},
		{name: "provider ID", ctx: baseCtx, scope: "resume-scope", mutate: func(id *types.SessionBindingIdentity) { id.ProviderSession.ID += "-other" }},
		{name: "provider generation", ctx: baseCtx, scope: "resume-scope", mutate: func(id *types.SessionBindingIdentity) { id.ProviderSession.Generation++ }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mismatch := expected
			tc.mutate(&mismatch)
			_, err := sw.AcquireSessionBinding(tc.ctx, "agent", types.SessionBindingRequest{
				Mode:     types.SessionBindingModeExactResume,
				Expected: &mismatch,
			}, swarm.WithScope(tc.scope))
			if err == nil {
				t.Fatalf("ExactResume with mismatched %s: want rejection", tc.name)
			}
		})
	}
	if created.Load() != 0 {
		t.Errorf("factory calls for mismatches = %d, want 0", created.Load())
	}

	resumed, err := sw.AcquireSessionBinding(baseCtx, "agent", types.SessionBindingRequest{
		Mode:     types.SessionBindingModeExactResume,
		Expected: &expected,
	}, swarm.WithScope("resume-scope"))
	if err != nil {
		t.Fatalf("ExactResume exact identity: %v", err)
	}
	if got := bindingIdentity(t, resumed); got != expected {
		t.Errorf("ExactResume identity = %+v, want %+v", got, expected)
	}
	if resumed.Scope != baseline.Scope {
		t.Errorf("ExactResume Scope = %q, want %q", resumed.Scope, baseline.Scope)
	}
	if created.Load() != 0 {
		t.Errorf("factory calls for exact ExactResume = %d, want 0", created.Load())
	}
}

func TestAcquireSessionBinding_ExactResumeRejectsDeadBindingBeforeFactory(t *testing.T) {
	var created atomic.Int32
	executor := newLiveBindingExecutor(types.SessionIdentity{Provider: "neutral", ID: "dead-session", Generation: 1}, true)
	sw := swarm.New(func(string) (types.ExecutorV2, error) {
		created.Add(1)
		return executor, nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-dead"})
	binding, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{Mode: types.SessionBindingModeNew}, swarm.WithScope("dead-scope"))
	if err != nil {
		t.Fatalf("New baseline: %v", err)
	}
	expected := bindingIdentity(t, binding)
	created.Store(0)
	executor.alive = types.HealthDead

	_, err = sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{
		Mode:     types.SessionBindingModeExactResume,
		Expected: &expected,
	}, swarm.WithScope("dead-scope"))
	if err == nil {
		t.Fatal("ExactResume dead binding: want rejection")
	}
	if created.Load() != 0 {
		t.Errorf("factory calls for dead ExactResume = %d, want 0", created.Load())
	}
}

func TestAcquireSessionBinding_CacheHitCannotIgnoreConflictingRequest(t *testing.T) {
	var created atomic.Int32
	sw := swarm.New(func(name string) (types.ExecutorV2, error) {
		created.Add(1)
		return newLiveBindingExecutor(types.SessionIdentity{Provider: "neutral", ID: fmt.Sprintf("%s-session", name), Generation: 1}, true), nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-cache"})
	binding, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{Mode: types.SessionBindingModeNew}, swarm.WithScope("cache-scope"))
	if err != nil {
		t.Fatalf("New baseline: %v", err)
	}
	conflict := bindingIdentity(t, binding)
	conflict.ProviderSession.ID += "-conflict"
	created.Store(0)

	_, err = sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{
		Mode:     types.SessionBindingModeExactResume,
		Expected: &conflict,
	}, swarm.WithScope("cache-scope"))
	if err == nil {
		t.Fatal("conflicting cache request: want rejection")
	}
	if created.Load() != 0 {
		t.Errorf("factory calls after conflicting cache request = %d, want 0", created.Load())
	}
}

func TestAcquireSessionBinding_ForkRejectsPersistentNonForkableParent(t *testing.T) {
	var created atomic.Int32
	parentExecutor := newLiveBindingExecutor(types.SessionIdentity{Provider: "neutral", ID: "parent", Generation: 1}, true)
	sw := swarm.New(func(string) (types.ExecutorV2, error) {
		created.Add(1)
		return parentExecutor, nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-fork"})
	parent, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{Mode: types.SessionBindingModeNew}, swarm.WithScope("fork-scope"))
	if err != nil {
		t.Fatalf("New parent: %v", err)
	}
	parentID := bindingIdentity(t, parent)
	created.Store(0)

	_, err = sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{
		Mode:   types.SessionBindingModeFork,
		Parent: &parentID,
	}, swarm.WithScope("fork-scope"))
	if err == nil {
		t.Fatal("Fork against persistent non-forkable parent: want rejection")
	}
	if created.Load() != 0 {
		t.Errorf("factory calls = %d, want 0", created.Load())
	}
}

func TestAcquireSessionBinding_ForkRequiresExactParentAndUsesForker(t *testing.T) {
	var created atomic.Int32
	forker := &forkableSessionBindingExecutor{
		liveBindingExecutor: newLiveBindingExecutor(types.SessionIdentity{Provider: "neutral", ID: "parent", Generation: 1}, true),
		childIdentity:       types.SessionIdentity{Provider: "neutral", ID: "child", Generation: 1},
	}
	sw := swarm.New(func(string) (types.ExecutorV2, error) {
		created.Add(1)
		return forker, nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-fork"})
	parent, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{Mode: types.SessionBindingModeNew}, swarm.WithScope("fork-scope"))
	if err != nil {
		t.Fatalf("New parent: %v", err)
	}
	parentID := bindingIdentity(t, parent)
	created.Store(0)

	mismatch := parentID
	mismatch.HandleGeneration++
	_, err = sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{
		Mode:   types.SessionBindingModeFork,
		Parent: &mismatch,
	}, swarm.WithScope("fork-scope"))
	if err == nil {
		t.Fatal("Fork with mismatched Parent: want rejection")
	}
	if forker.forkCalls.Load() != 0 {
		t.Errorf("ForkSession calls after mismatched Parent = %d, want 0", forker.forkCalls.Load())
	}
	if created.Load() != 0 {
		t.Errorf("factory calls after mismatched Parent = %d, want 0", created.Load())
	}

	child, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{
		Mode:   types.SessionBindingModeFork,
		Parent: &parentID,
	}, swarm.WithScope("fork-scope"))
	if err != nil {
		t.Fatalf("Fork exact Parent: %v", err)
	}
	assertCompleteBinding(t, child, "fork-scope")
	if child.HandleID == parent.HandleID {
		t.Errorf("child HandleID = parent HandleID %q, want distinct handle", child.HandleID)
	}
	if forker.forkCalls.Load() != 1 {
		t.Errorf("ForkSession calls = %d, want 1", forker.forkCalls.Load())
	}
	if created.Load() != 0 {
		t.Errorf("factory calls during Fork = %d, want 0", created.Load())
	}

	resumedParent, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{
		Mode:     types.SessionBindingModeExactResume,
		Expected: &parentID,
	}, swarm.WithScope("fork-scope"))
	if err != nil {
		t.Fatalf("ExactResume original parent after Fork: %v", err)
	}
	if got := bindingIdentity(t, resumedParent); got != parentID {
		t.Errorf("parent identity after Fork = %+v, want %+v", got, parentID)
	}
	if created.Load() != 0 {
		t.Errorf("factory calls while resuming parent after Fork = %d, want 0", created.Load())
	}
}

func TestAcquireSessionBinding_ForkPermitsMissingChildProviderSession(t *testing.T) {
	var created atomic.Int32
	forker := &opaqueChildForkableExecutor{
		liveBindingExecutor: newLiveBindingExecutor(types.SessionIdentity{Provider: "neutral", ID: "parent", Generation: 1}, true),
	}
	sw := swarm.New(func(string) (types.ExecutorV2, error) {
		created.Add(1)
		return forker, nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	ctx := tenant.WithContext(context.Background(), tenant.TenantContext{TenantID: "tenant-opaque-fork"})
	parent, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{Mode: types.SessionBindingModeNew}, swarm.WithScope("opaque-fork-scope"))
	if err != nil {
		t.Fatalf("New parent: %v", err)
	}
	parentID := bindingIdentity(t, parent)
	created.Store(0)

	child, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{
		Mode:   types.SessionBindingModeFork,
		Parent: &parentID,
	}, swarm.WithScope("opaque-fork-scope"))
	if err != nil {
		t.Fatalf("Fork opaque child: %v", err)
	}
	if child.HandleID == parent.HandleID || child.HandleID == "" || child.HandleGeneration == 0 || child.RegistryGeneration == 0 {
		t.Errorf("child binding = %+v, want distinct safe handle snapshot", child)
	}
	if child.ProviderSession != nil {
		t.Errorf("child ProviderSession = %+v, want nil", child.ProviderSession)
	}
	if forker.forkCalls.Load() != 1 {
		t.Errorf("ForkSession calls = %d, want 1", forker.forkCalls.Load())
	}
	if created.Load() != 0 {
		t.Errorf("factory calls during opaque Fork = %d, want 0", created.Load())
	}

	resumedParent, err := sw.AcquireSessionBinding(ctx, "agent", types.SessionBindingRequest{
		Mode:     types.SessionBindingModeExactResume,
		Expected: &parentID,
	}, swarm.WithScope("opaque-fork-scope"))
	if err != nil {
		t.Fatalf("ExactResume original parent after opaque Fork: %v", err)
	}
	if got := bindingIdentity(t, resumedParent); got != parentID {
		t.Errorf("parent identity after opaque Fork = %+v, want %+v", got, parentID)
	}
	if created.Load() != 0 {
		t.Errorf("factory calls while resuming parent after opaque Fork = %d, want 0", created.Load())
	}
}

func TestAcquireSessionBinding_MissingExactOrParentRejectsBeforeFactory(t *testing.T) {
	var created atomic.Int32
	sw := swarm.New(func(name string) (types.ExecutorV2, error) {
		created.Add(1)
		return newLiveBindingExecutor(types.SessionIdentity{Provider: "neutral", ID: name, Generation: 1}, true), nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	for _, request := range []types.SessionBindingRequest{
		{Mode: types.SessionBindingModeExactResume},
		{Mode: types.SessionBindingModeFork},
	} {
		_, err := sw.AcquireSessionBinding(context.Background(), "agent", request, swarm.WithScope("missing"))
		if err == nil {
			t.Fatalf("%s without required identity: want rejection", request.Mode)
		}
	}
	if created.Load() != 0 {
		t.Errorf("factory calls = %d, want 0", created.Load())
	}
}

func TestAcquireSessionBinding_StatelessNeverExposesProviderSession(t *testing.T) {
	sw := swarm.New(func(string) (types.ExecutorV2, error) {
		return newLiveBindingExecutor(types.SessionIdentity{Provider: "neutral", ID: "must-not-escape", Generation: 1}, true), nil
	}, audit.DiscardLog{}, swarm.WithStatefulTTL(0))
	defer sw.Shutdown(context.Background())

	binding, err := sw.AcquireSessionBinding(context.Background(), "agent", types.SessionBindingRequest{
		Mode: types.SessionBindingModeStateless,
	}, swarm.WithScope("stateless-provider-scope"))
	if err != nil {
		t.Fatalf("Stateless: %v", err)
	}
	if binding.ProviderSession != nil {
		t.Fatalf("stateless ProviderSession = %+v, want nil", binding.ProviderSession)
	}
}
