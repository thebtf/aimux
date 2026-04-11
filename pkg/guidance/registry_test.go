package guidance_test

import (
	"errors"
	"reflect"
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
