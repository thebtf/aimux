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
			if spec.ID == "cancel" || spec.ID == "timeout" || spec.ID == "source_built_zero_child_leak" {
				terminal = runT016LifecycleScenario(t, binary, spec)
			} else {
				terminal = runT016SourceOutputScenario(t, binary, spec)
			}
		case "runtime_fixture":
			if spec.ID == "late" {
				terminal = runT016LateScenario(t)
			} else {
				terminal = runT016LifecycleScenario(t, binary, spec)
			}
		case "supplied_evidence":
			terminal = runT016SuppliedScenario(t, spec.ID)
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
