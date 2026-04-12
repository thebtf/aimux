package guidance_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/thebtf/aimux/pkg/guidance"
)

type testPolicy struct {
	tool string
	plan guidance.NextActionPlan
	err  error
}

func (p testPolicy) ToolName() string {
	return p.tool
}

func (p testPolicy) BuildPlan(guidance.PolicyInput) (guidance.NextActionPlan, error) {
	if p.err != nil {
		return guidance.NextActionPlan{}, p.err
	}
	return p.plan, nil
}

func TestRegistryRegisterGetListCount(t *testing.T) {
	r := guidance.NewRegistry()

	if got := r.Count(); got != 0 {
		t.Fatalf("initial Count = %d, want 0", got)
	}

	if err := r.Register(testPolicy{tool: "workflow"}); err != nil {
		t.Fatalf("register workflow: %v", err)
	}
	if err := r.Register(testPolicy{tool: "investigate"}); err != nil {
		t.Fatalf("register investigate: %v", err)
	}

	if got := r.Count(); got != 2 {
		t.Fatalf("Count after register = %d, want 2", got)
	}

	policy, ok := r.Get("workflow")
	if !ok {
		t.Fatal("Get(workflow) = not found, want found")
	}
	if policy.ToolName() != "workflow" {
		t.Fatalf("Get(workflow).ToolName = %q, want workflow", policy.ToolName())
	}

	gotTools := r.ListTools()
	wantTools := []string{"investigate", "workflow"}
	if !reflect.DeepEqual(gotTools, wantTools) {
		t.Fatalf("ListTools = %#v, want %#v", gotTools, wantTools)
	}
}

func TestRegistryValidationAndErrors(t *testing.T) {
	r := guidance.NewRegistry()

	if err := r.Register(nil); err == nil {
		t.Fatal("Register(nil) error = nil, want error")
	}
	if err := r.Register(testPolicy{}); err == nil {
		t.Fatal("Register(empty tool) error = nil, want error")
	}

	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(missing) found policy, want not found")
	}

	if _, err := r.MustGet("missing"); err == nil {
		t.Fatal("MustGet(missing) error = nil, want error")
	}

	if err := r.Register(testPolicy{tool: "dialog"}); err != nil {
		t.Fatalf("Register(dialog): %v", err)
	}
	if err := r.Register(testPolicy{tool: "dialog"}); err == nil {
		t.Fatal("duplicate Register(dialog) error = nil, want error")
	}
}

func TestRegistryStoredPolicyCanBuildPlan(t *testing.T) {
	r := guidance.NewRegistry()

	wantErr := errors.New("policy error")
	if err := r.Register(testPolicy{tool: "consensus", err: wantErr}); err != nil {
		t.Fatalf("Register(consensus): %v", err)
	}

	p, err := r.MustGet("consensus")
	if err != nil {
		t.Fatalf("MustGet(consensus): %v", err)
	}

	_, gotErr := p.BuildPlan(guidance.PolicyInput{Action: "start"})
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("BuildPlan error = %v, want %v", gotErr, wantErr)
	}
}

func TestGuidedToolsReturnsCopy(t *testing.T) {
	first := guidance.GuidedTools()
	if len(first) == 0 {
		t.Fatal("GuidedTools() returned empty list")
	}

	first[0] = "mutated"
	second := guidance.GuidedTools()
	if second[0] == "mutated" {
		t.Fatal("GuidedTools() returned shared backing slice")
	}
}

func TestMissingGuidedToolsTracksUnregisteredPolicies(t *testing.T) {
	r := guidance.NewRegistry()
	missing := r.MissingGuidedTools()
	if len(missing) == 0 {
		t.Fatal("MissingGuidedTools() = empty, want canonical guided tools")
	}

	for _, tool := range guidance.GuidedTools() {
		if err := r.Register(testPolicy{tool: tool}); err != nil {
			t.Fatalf("Register(%s): %v", tool, err)
		}
	}

	missing = r.MissingGuidedTools()
	if len(missing) != 0 {
		t.Fatalf("MissingGuidedTools() = %#v, want empty", missing)
	}
}

func TestValidateRequiredPoliciesDevModeReturnsError(t *testing.T) {
	t.Setenv("AIMUX_ENV", "development")

	r := guidance.NewRegistry()
	err := r.ValidateRequiredPolicies()
	if err == nil {
		t.Fatal("ValidateRequiredPolicies() error = nil, want error in dev mode")
	}
	if !strings.Contains(err.Error(), "missing policies") {
		t.Fatalf("ValidateRequiredPolicies() error = %q, want to contain 'missing policies'", err.Error())
	}
}

func TestResolveMissingPolicyDevModeReturnsError(t *testing.T) {
	t.Setenv("AIMUX_ENV", "development")

	r := guidance.NewRegistry()
	policy, fallback, err := r.Resolve("investigate", map[string]any{"session_id": "abc"})
	if err == nil {
		t.Fatal("Resolve missing policy in dev mode error = nil, want error")
	}
	if policy != nil {
		t.Fatal("Resolve missing policy in dev mode policy != nil")
	}
	if fallback != nil {
		t.Fatalf("Resolve missing policy in dev mode fallback = %#v, want nil", fallback)
	}
	if !strings.Contains(err.Error(), "missing policy") {
		t.Fatalf("Resolve dev error = %q, want to contain 'missing policy'", err.Error())
	}
}

func TestResolveMissingPolicyProdModeReturnsFallbackEnvelope(t *testing.T) {
	t.Setenv("AIMUX_ENV", "")

	r := guidance.NewRegistry()
	raw := map[string]any{"result": map[string]any{"session_id": "abc"}}

	policy, fallback, err := r.Resolve("investigate", raw)
	if err != nil {
		t.Fatalf("Resolve missing policy in prod mode error = %v, want nil", err)
	}
	if policy != nil {
		t.Fatal("Resolve missing policy in prod mode policy != nil")
	}
	if fallback == nil {
		t.Fatal("Resolve missing policy in prod mode fallback = nil, want envelope")
	}
	if fallback.State != guidance.StateGuidanceNotImplemented {
		t.Fatalf("fallback.State = %q, want %q", fallback.State, guidance.StateGuidanceNotImplemented)
	}

	gotResult, ok := fallback.Result.(map[string]any)
	if !ok {
		t.Fatalf("fallback.Result type = %T, want map[string]any", fallback.Result)
	}
	if gotResult["session_id"] != "abc" {
		t.Fatalf("fallback.Result.session_id = %v, want abc", gotResult["session_id"])
	}
}

func TestResolveReturnsRegisteredPolicy(t *testing.T) {
	t.Setenv("AIMUX_ENV", "development")

	r := guidance.NewRegistry()
	if err := r.Register(testPolicy{tool: "investigate"}); err != nil {
		t.Fatalf("Register(investigate): %v", err)
	}

	policy, fallback, err := r.Resolve("investigate", map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("Resolve registered policy err = %v, want nil", err)
	}
	if fallback != nil {
		t.Fatalf("Resolve registered policy fallback = %#v, want nil", fallback)
	}
	if policy == nil {
		t.Fatal("Resolve registered policy returned nil policy")
	}
	if policy.ToolName() != "investigate" {
		t.Fatalf("Resolve registered policy ToolName = %q, want investigate", policy.ToolName())
	}
}
