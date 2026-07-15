package e2e

import (
	"fmt"
	"testing"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

func runT016SuppliedScenario(t *testing.T, id string) string {
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

	expectedProcess := types.ProcessIdentity{
		PID:              4242,
		StartFingerprint: "t016-supplied-start",
		TreeID:           "t016-supplied-tree",
	}
	var evidence *swarm.SuppliedProcessEvidence
	var want swarm.SuppliedEvidenceClassification
	switch id {
	case "supplied_crash":
		exitCode := 23
		evidence = &swarm.SuppliedProcessEvidence{
			ExpectedProcess: expectedProcess,
			ExitCode:        &exitCode,
		}
		want = swarm.SuppliedEvidenceFailedCrash
	case "supplied_orphan":
		evidence = &swarm.SuppliedProcessEvidence{
			ExpectedProcess:     expectedProcess,
			RootAbsent:          true,
			DescendantsSurvived: true,
		}
		want = swarm.SuppliedEvidenceOrphanedTree
	default:
		t.Fatalf("unknown supplied scenario %q", id)
		return ""
	}

	got := runtime.InspectSuppliedEvidence(evidence).Classification
	if got != want {
		t.Fatalf("InspectSuppliedEvidence(%s) classification = %q, want %q", id, got, want)
	}
	if factoryCalls != 0 {
		t.Fatalf("InspectSuppliedEvidence(%s) constructed %d executors, want 0", id, factoryCalls)
	}
	return string(got)
}
