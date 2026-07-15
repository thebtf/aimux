package e2e

import (
	"fmt"
	"testing"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

func runT016SuppliedScenario(t *testing.T, spec t016ScenarioSpec) string {
	t.Helper()

	factoryCalls := 0
	s := swarm.New(func(string) (types.ExecutorV2, error) {
		factoryCalls++
		return nil, fmt.Errorf("executor construction is forbidden for supplied evidence")
	}, nil)
	runtime, err := workerruntime.New(s)
	if err != nil {
		t.Fatalf("create worker runtime: %v", err)
	}

	evidence := &swarm.SuppliedProcessEvidence{
		ExpectedProcess: types.ProcessIdentity{
			PID:              4242,
			StartFingerprint: "t016-supplied-start",
			TreeID:           "t016-supplied-tree",
		},
		RootAbsent:          spec.Input.RootAbsent,
		DescendantsSurvived: spec.Input.DescendantsSurvived,
	}
	if spec.Input.ExitCode != nil {
		exitCode := *spec.Input.ExitCode
		evidence.ExitCode = &exitCode
	}

	var want swarm.SuppliedEvidenceClassification
	switch spec.Proof {
	case "exit_nonzero":
		want = swarm.SuppliedEvidenceFailedCrash
	case "orphan_tree":
		want = swarm.SuppliedEvidenceOrphanedTree
	default:
		t.Fatalf("unknown supplied proof %q", spec.Proof)
		return ""
	}

	got := runtime.InspectSuppliedEvidence(evidence).Classification
	if got != want {
		t.Fatalf("InspectSuppliedEvidence(%s) classification = %q, want %q", spec.ID, got, want)
	}
	if factoryCalls != 0 {
		t.Fatalf("InspectSuppliedEvidence(%s) constructed %d executors, want 0", spec.ID, factoryCalls)
	}
	return string(got)
}
