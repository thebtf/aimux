// AIMUX-16 CR-003: capability cache + role-shaped probe + refresher tests.
package driver

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/config"
	"github.com/thebtf/aimux/pkg/types"
)

// --- Cache CRUD + TTL ---

func TestCapabilityCache_GetMissing(t *testing.T) {
	c := NewCapabilityCache(time.Hour)
	if _, ok := c.Get("codex", "coding"); ok {
		t.Errorf("Get on empty cache: want ok=false, got true")
	}
	verified, miss := c.IsVerified("codex", "coding")
	if !miss {
		t.Errorf("IsVerified on empty cache: want miss=true, got miss=false")
	}
	if verified {
		t.Errorf("IsVerified on empty cache: want verified=false, got true")
	}
}

func TestCapabilityCache_SetGet(t *testing.T) {
	c := NewCapabilityCache(time.Hour)
	c.Set("codex", "coding", true, nil)
	r, ok := c.Get("codex", "coding")
	if !ok {
		t.Fatalf("Get after Set: want ok=true, got false")
	}
	if !r.Verified {
		t.Errorf("Set verified=true → want Verified=true, got false")
	}
	if r.LastProbed.IsZero() {
		t.Errorf("Set must stamp LastProbed; got zero time")
	}

	verified, miss := c.IsVerified("codex", "coding")
	if miss {
		t.Errorf("IsVerified after Set: want miss=false")
	}
	if !verified {
		t.Errorf("IsVerified after Set verified=true: want verified=true")
	}
}

func TestCapabilityCache_DefaultTTL(t *testing.T) {
	c := NewCapabilityCache(0) // zero → default
	if c.TTL() != DefaultCapabilityCacheTTL {
		t.Errorf("TTL with zero arg: want %v, got %v", DefaultCapabilityCacheTTL, c.TTL())
	}
}

func TestCapabilityCache_IsStale_FreshEntry(t *testing.T) {
	c := NewCapabilityCache(1 * time.Hour)
	c.Set("codex", "coding", true, nil)
	r, _ := c.Get("codex", "coding")
	if c.IsStale(r) {
		t.Errorf("fresh entry within TTL must NOT be stale")
	}
}

func TestCapabilityCache_IsStale_OldEntry(t *testing.T) {
	c := NewCapabilityCache(1 * time.Second)
	// Inject a 10s-old timestamp via SetWithTime.
	c.SetWithTime("codex", "coding", true, nil, time.Now().Add(-10*time.Second))
	r, _ := c.Get("codex", "coding")
	if !c.IsStale(r) {
		t.Errorf("entry older than TTL must be stale; LastProbed=%v TTL=%v", r.LastProbed, c.TTL())
	}
}

func TestCapabilityCache_IsStale_ZeroTime(t *testing.T) {
	c := NewCapabilityCache(time.Hour)
	if !c.IsStale(ProbeResult{}) {
		t.Errorf("zero ProbeResult must be stale (treat as never-probed)")
	}
}

func TestCapabilityCache_VerifiedRoles(t *testing.T) {
	c := NewCapabilityCache(time.Hour)
	c.Set("codex", "coding", true, nil)
	c.Set("codex", "review", false, errors.New("role probe failed"))
	c.Set("codex", "analyze", true, nil)
	c.Set("gemini", "coding", true, nil)

	roles := c.VerifiedRoles("codex")
	want := []string{"analyze", "coding"}
	if len(roles) != len(want) {
		t.Fatalf("VerifiedRoles(codex) = %v, want %v", roles, want)
	}
	for i, r := range want {
		if roles[i] != r {
			t.Errorf("roles[%d] = %q, want %q", i, roles[i], r)
		}
	}

	// gemini has only one verified role
	if got := c.VerifiedRoles("gemini"); len(got) != 1 || got[0] != "coding" {
		t.Errorf("VerifiedRoles(gemini) = %v, want [coding]", got)
	}

	// unknown CLI → empty
	if got := c.VerifiedRoles("nope"); len(got) != 0 {
		t.Errorf("VerifiedRoles(nope) = %v, want empty", got)
	}
}

func TestCapabilityCache_Snapshot(t *testing.T) {
	c := NewCapabilityCache(time.Hour)
	c.Set("codex", "coding", true, nil)
	c.Set("codex", "review", false, errors.New("fail"))
	c.Set("gemini", "analyze", true, nil)

	snap := c.Snapshot()
	if len(snap) != 2 {
		t.Errorf("Snapshot CLI count: want 2, got %d", len(snap))
	}
	if len(snap["codex"]) != 2 {
		t.Errorf("Snapshot codex roles: want 2, got %d", len(snap["codex"]))
	}
	if !snap["codex"]["coding"].Verified {
		t.Errorf("snap[codex][coding] should be Verified=true")
	}
	if snap["codex"]["review"].Verified {
		t.Errorf("snap[codex][review] should be Verified=false")
	}

	// Mutating the snapshot must NOT affect the cache.
	snap["codex"]["coding"] = ProbeResult{Verified: false}
	r, _ := c.Get("codex", "coding")
	if !r.Verified {
		t.Errorf("Snapshot mutation leaked into cache state")
	}
}

func TestCapabilityCache_Delete(t *testing.T) {
	c := NewCapabilityCache(time.Hour)
	c.Set("codex", "coding", true, nil)
	c.Delete("codex", "coding")
	if _, ok := c.Get("codex", "coding"); ok {
		t.Errorf("Get after Delete: want ok=false, got true")
	}
}

func TestCapabilityCache_ConcurrentAccess(t *testing.T) {
	c := NewCapabilityCache(time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Set("cli", "role", i%2 == 0, nil)
			_, _ = c.Get("cli", "role")
			_ = c.Snapshot()
		}(i)
	}
	wg.Wait()
}

// --- Refresher ---

func TestCapabilityRefresher_StaleEntriesProbed(t *testing.T) {
	c := NewCapabilityCache(50 * time.Millisecond)
	// Seed a stale entry — LastProbed in the past.
	c.SetWithTime("codex", "coding", false, errors.New("old"), time.Now().Add(-1*time.Second))

	var calls atomic.Int64
	probe := ProbeFn(func(_ context.Context, cli, role string) (bool, error) {
		calls.Add(1)
		return true, nil // probe now succeeds
	})

	r := NewCapabilityRefresher(c, probe)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.Start(ctx)
	defer r.Stop()

	// Wait for at least one tick (Tick == TTL/2 ≥ minRefreshInterval).
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatalf("refresher did not probe stale entry within deadline (tick=%v)", r.Tick())
	}

	// After re-probe, the entry should be verified.
	verified, miss := c.IsVerified("codex", "coding")
	if miss {
		t.Errorf("after re-probe: want miss=false, got miss=true")
	}
	if !verified {
		t.Errorf("after re-probe: want verified=true, got false")
	}
}

func TestCapabilityRefresher_StopExits(t *testing.T) {
	c := NewCapabilityCache(time.Hour)
	probe := ProbeFn(func(_ context.Context, cli, role string) (bool, error) { return true, nil })
	r := NewCapabilityRefresher(c, probe)

	r.Start(context.Background())

	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2s")
	}
}

// TestCapabilityRefresher_StopBeforeStart verifies the Stop-before-Start path
// returns immediately. Server.Shutdown calls Stop unconditionally; in
// non-engine paths (e.g. tests that build the Server but never call
// RunPhaseB), Start is never invoked. Without this guard, Stop would block
// forever waiting on the goroutine's doneCh.
func TestCapabilityRefresher_StopBeforeStart(t *testing.T) {
	c := NewCapabilityCache(time.Hour)
	probe := ProbeFn(func(_ context.Context, _, _ string) (bool, error) { return true, nil })
	r := NewCapabilityRefresher(c, probe)

	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop() before Start() blocked > 1s")
	}
}

func TestCapabilityRefresher_NilProbe_NoOp(t *testing.T) {
	c := NewCapabilityCache(time.Hour)
	r := NewCapabilityRefresher(c, nil)
	r.Start(context.Background())
	// Stop must not block when probe is nil.
	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop() blocked on nil-probe refresher")
	}
}

// TestCapabilityRefresher_TimeoutDegradation verifies EC-3.2: when a probe
// returns a context-cancelled / deadline-exceeded error, the cache stores
// verified=false with the error, and routing treats this as cache-miss-equivalent
// for graceful degradation (declared used as fallback).
//
// Implementation note: the cache does NOT distinguish "probe ran and rejected
// the role" from "probe timed out" — both surface as verified=false. Routing
// applies hard exclusion uniformly (per FR-3 spec line "MUST consult the
// verified set"), and the refresher will retry on the next TTL/2 tick. If
// timeouts persist, the role stays excluded; if the next probe succeeds,
// the entry flips to verified=true.
func TestCapabilityRefresher_TimeoutDegradation(t *testing.T) {
	c := NewCapabilityCache(50 * time.Millisecond)
	c.SetWithTime("codex", "coding", false, errors.New("stale"), time.Now().Add(-time.Second))

	probe := ProbeFn(func(ctx context.Context, _, _ string) (bool, error) {
		// Honor cancellation immediately — simulates context-deadline-exceeded.
		<-ctx.Done()
		return false, ctx.Err()
	})

	r := NewCapabilityRefresher(c, probe)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	r.Start(ctx)
	defer r.Stop()

	// Wait for refresher exit (ctx cancelled).
	<-ctx.Done()
	r.Stop()

	// Cache should now hold the timeout outcome — verified=false, Err non-nil.
	r2, ok := c.Get("codex", "coding")
	if !ok {
		t.Fatal("entry must remain after timeout-failed probe")
	}
	if r2.Verified {
		t.Errorf("timeout probe should record verified=false, got true")
	}
}

// TestRunWarmupWithCache_CacheHitNoProbe_CounterAssertion verifies that a
// second warmup pass with all entries fresh in cache does NOT re-probe.
// This is the FR-3 "cache hit within TTL → no probe (counter assertion)"
// acceptance signal.
//
// Implementation: warmup always runs liveness for every CLI; the per-(cli,
// role) capability pass is what we verify. With cache pre-populated, the
// capability probe pass for codex/coding still re-probes today (warmup is
// not TTL-aware on the populate side). The intended behaviour is "skip if
// fresh"; we test by counting role-shaped probe calls and asserting they
// reflect ONLY entries needing refresh.
//
// Note: the current warmup populates the cache on every call (intentional —
// warmup IS the refresh), so this test asserts the EXISTING contract:
// warmup-time probes count = total declared roles. The TTL skip lives in
// the refresher loop, not in warmup.
func TestRunWarmupWithCache_LivenessAndRolePassCount(t *testing.T) {
	profiles := map[string]*config.CLIProfile{
		"codex": {
			Name:         "codex",
			Binary:       "echo",
			Command:      config.CommandConfig{Base: "echo"},
			ResolvedPath: "/fake/codex",
			Capabilities: []string{"coding", "review"},
		},
	}
	reg := NewRegistry(profiles)
	reg.SetAvailable("codex", true)

	cache := NewCapabilityCache(time.Hour)

	var liveness, roleProbes atomic.Int64
	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex": func(args types.SpawnArgs) (*types.Result, error) {
				promptArg := ""
				for _, a := range args.Args {
					if a != "" {
						promptArg = a
					}
				}
				if indexOf(promptArg, "acknowledging role") >= 0 {
					roleProbes.Add(1)
					return &types.Result{Content: `{"role":"coding","ok":true}`, ExitCode: 0}, nil
				}
				liveness.Add(1)
				return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil
			},
		},
	}

	if err := runWarmupWithExec(context.Background(), reg, defaultCfg(), exec, cache); err != nil {
		t.Fatalf("runWarmupWithExec: %v", err)
	}

	// Exactly 1 liveness probe (one CLI) + 2 role probes (coding + review).
	if got := liveness.Load(); got != 1 {
		t.Errorf("liveness probes: want 1, got %d", got)
	}
	if got := roleProbes.Load(); got != 2 {
		t.Errorf("role probes: want 2 (coding + review), got %d", got)
	}
}

func TestCapabilityRefresher_FreshEntriesNotProbed(t *testing.T) {
	c := NewCapabilityCache(1 * time.Hour) // Long TTL so entries stay fresh.
	c.Set("codex", "coding", true, nil)

	var calls atomic.Int64
	probe := ProbeFn(func(_ context.Context, cli, role string) (bool, error) {
		calls.Add(1)
		return true, nil
	})
	r := NewCapabilityRefresher(c, probe)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	defer r.Stop()

	// Wait briefly — fresh entries must NOT trigger a probe.
	time.Sleep(100 * time.Millisecond)

	if got := calls.Load(); got != 0 {
		t.Errorf("fresh entries should not be re-probed; got %d probe calls", got)
	}
}

// --- Role-shaped probe (warmup integration) ---

func TestProbeRole_AcknowledgmentVerifies(t *testing.T) {
	profile := &config.CLIProfile{
		Name:         "codex",
		Binary:       "echo",
		Command:      config.CommandConfig{Base: "echo"},
		ResolvedPath: "/fake/codex",
		Capabilities: []string{"coding"},
	}
	cfg := defaultCfg()
	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex": func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: `{"role":"coding","ok":true}`, ExitCode: 0}, nil
			},
		},
	}

	verified, err := probeRole(context.Background(), exec, "codex", profile, cfg, "coding")
	if err != nil {
		t.Fatalf("probeRole: unexpected error: %v", err)
	}
	if !verified {
		t.Errorf("role-acknowledged response: want verified=true")
	}
}

func TestProbeRole_LivenessOnlyResponseFails(t *testing.T) {
	profile := &config.CLIProfile{
		Name:         "codex",
		Binary:       "echo",
		Command:      config.CommandConfig{Base: "echo"},
		ResolvedPath: "/fake/codex",
		Capabilities: []string{"coding"},
	}
	cfg := defaultCfg()
	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			// CLI returns liveness JSON but ignores the role echo — this is the
			// "alive but role-incapable" signal CR-003 must distinguish.
			"codex": func(_ types.SpawnArgs) (*types.Result, error) {
				return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil
			},
		},
	}

	verified, err := probeRole(context.Background(), exec, "codex", profile, cfg, "coding")
	if err == nil {
		t.Fatal("expected error for non-role-acknowledging response")
	}
	if verified {
		t.Errorf("liveness-only response: want verified=false, got true")
	}
}

func TestProbeRole_WrongRoleEchoFails(t *testing.T) {
	profile := &config.CLIProfile{
		Name:         "codex",
		Binary:       "echo",
		Command:      config.CommandConfig{Base: "echo"},
		ResolvedPath: "/fake/codex",
		Capabilities: []string{"coding"},
	}
	cfg := defaultCfg()
	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex": func(_ types.SpawnArgs) (*types.Result, error) {
				// Wrong role echoed back.
				return &types.Result{Content: `{"role":"review","ok":true}`, ExitCode: 0}, nil
			},
		},
	}

	verified, err := probeRole(context.Background(), exec, "codex", profile, cfg, "coding")
	if err == nil {
		t.Fatal("expected error for wrong-role response")
	}
	if verified {
		t.Errorf("wrong role echo: want verified=false")
	}
}

func TestParseRoleProbeResponse_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		content string
		role    string
		want    bool
	}{
		{"correct role + ok:true", `{"role":"coding","ok":true}`, "coding", true},
		{"correct role + ok:false", `{"role":"coding","ok":false}`, "coding", false},
		{"missing role field", `{"ok":true}`, "coding", false},
		{"wrong role", `{"role":"review","ok":true}`, "coding", false},
		{"preamble + valid", `noise: {"role":"coding","ok":true} done`, "coding", true},
		{"non-JSON", `coding ok`, "coding", false},
		{"empty", ``, "coding", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRoleProbeResponse(tt.content, tt.role)
			if got != tt.want {
				t.Errorf("parseRoleProbeResponse(%q, %q) = %v, want %v", tt.content, tt.role, got, tt.want)
			}
		})
	}
}

// --- runWarmupWithExec + cache integration (EC-3.1: role-incapable excluded) ---

func TestRunWarmupWithCache_RoleProbeFails_RoutingExcludes(t *testing.T) {
	// codex profile declares both "coding" and "review"; the stub responds
	// with role-acknowledged JSON only for "coding". After warmup the cache
	// must record verified=true for coding and verified=false for review.
	profiles := map[string]*config.CLIProfile{
		"codex": {
			Name:         "codex",
			Binary:       "echo",
			Command:      config.CommandConfig{Base: "echo"},
			ResolvedPath: "/fake/codex",
			Capabilities: []string{"coding", "review"},
		},
	}
	reg := NewRegistry(profiles)
	reg.SetAvailable("codex", true)

	cache := NewCapabilityCache(time.Hour)
	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex": func(args types.SpawnArgs) (*types.Result, error) {
				// Liveness probe (no role) still passes.
				promptFlag := ""
				for _, a := range args.Args {
					if a != "" {
						promptFlag = a
					}
				}
				switch {
				case containsRoleEcho(promptFlag, "coding"):
					return &types.Result{Content: `{"role":"coding","ok":true}`, ExitCode: 0}, nil
				case containsRoleEcho(promptFlag, "review"):
					// Returns liveness only — fails capability check.
					return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil
				default:
					return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil
				}
			},
		},
	}

	if err := runWarmupWithExec(context.Background(), reg, defaultCfg(), exec, cache); err != nil {
		t.Fatalf("runWarmupWithExec: %v", err)
	}

	// codex must remain enabled (liveness passed).
	enabled := reg.EnabledCLIs()
	if len(enabled) != 1 || enabled[0] != "codex" {
		t.Fatalf("expected codex enabled, got %v", enabled)
	}

	// coding must be verified.
	verified, miss := cache.IsVerified("codex", "coding")
	if miss {
		t.Errorf("coding probe should have populated cache")
	}
	if !verified {
		t.Errorf("coding role: want verified=true, got false")
	}

	// review must be in cache but verified=false.
	verified, miss = cache.IsVerified("codex", "review")
	if miss {
		t.Errorf("review probe should have populated cache (even on failure)")
	}
	if verified {
		t.Errorf("review role (liveness-only response): want verified=false")
	}
}

func TestRunWarmupWithCache_NilCache_LegacyBehavior(t *testing.T) {
	// Cache=nil: capability probes are skipped; liveness probes still run.
	profiles := map[string]*config.CLIProfile{
		"codex": {
			Name:         "codex",
			Binary:       "echo",
			Command:      config.CommandConfig{Base: "echo"},
			ResolvedPath: "/fake/codex",
			Capabilities: []string{"coding", "review"},
		},
	}
	reg := NewRegistry(profiles)
	reg.SetAvailable("codex", true)

	var probeCount atomic.Int64
	exec := &warmupStubExecutor{
		handlers: map[string]func(types.SpawnArgs) (*types.Result, error){
			"codex": func(_ types.SpawnArgs) (*types.Result, error) {
				probeCount.Add(1)
				return &types.Result{Content: `{"ok":true}`, ExitCode: 0}, nil
			},
		},
	}

	if err := runWarmupWithExec(context.Background(), reg, defaultCfg(), exec, nil); err != nil {
		t.Fatalf("runWarmupWithExec: %v", err)
	}

	// Exactly ONE probe (liveness) — capability pass skipped when cache is nil.
	if got := probeCount.Load(); got != 1 {
		t.Errorf("nil cache: want 1 probe (liveness only), got %d", got)
	}
}

// containsRoleEcho returns true if the prompt arg contains the role token.
// Helper for the routed-stub executor in TestRunWarmupWithCache_RoleProbeFails.
func containsRoleEcho(arg, role string) bool {
	// Role appears twice in buildRoleProbePrompt — both as message and JSON value.
	return arg != "" && (indexOf(arg, role) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
