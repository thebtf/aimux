package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestE2E_GenericWorkerAcceptanceMatrix(t *testing.T) {
	specs := t016ScenarioSpecs()
	if err := validateT016ScenarioSpecs(specs); err != nil {
		t.Fatalf("validate T016 scenario manifest: %v", err)
	}

	encoded, err := json.Marshal(specs)
	if err != nil {
		t.Fatalf("marshal T016 scenario manifest: %v", err)
	}
	digest := sha256.Sum256(encoded)
	manifestSHA256 := hex.EncodeToString(digest[:])

	binary := buildTestCLI(t)
	for _, spec := range specs {
		var terminal string
		switch spec.Kind {
		case "source_generic":
			switch spec.Proof {
			case "termination", "deadline", "tree_liveness":
				terminal = runT016LifecycleScenario(t, binary, spec)
			case "ordered_stream", "bounded_flood", "byte_exact", "typed_input", "input_rejected", "input_limit":
				terminal = runT016SourceOutputScenario(t, binary, spec)
			default:
				t.Fatalf("scenario %q has unsupported source proof %q", spec.ID, spec.Proof)
			}
		case "runtime_fixture":
			switch spec.Proof {
			case "late_output":
				terminal = runT016LateScenario(t, spec)
			default:
				t.Fatalf("scenario %q has unsupported fixture proof %q", spec.ID, spec.Proof)
			}
		case "supplied_evidence":
			switch spec.Proof {
			case "exit_nonzero", "orphan_tree":
				terminal = runT016SuppliedScenario(t, spec)
			default:
				t.Fatalf("scenario %q has unsupported supplied proof %q", spec.ID, spec.Proof)
			}
		default:
			t.Fatalf("scenario %q has unsupported kind %q", spec.ID, spec.Kind)
		}
		if terminal != spec.Expected {
			t.Fatalf("scenario %q terminal = %q, want %q", spec.ID, terminal, spec.Expected)
		}
		t.Logf("T016 scenario=%s PASS terminal=%s", spec.ID, terminal)
	}
	t.Logf("T016 manifest_sha256=%s", manifestSHA256)
}
