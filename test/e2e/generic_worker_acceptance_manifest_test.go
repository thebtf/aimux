package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type t016ScenarioSpec struct {
	ID       string
	Kind     string
	Proof    string
	Input    string
	Expected string
}

const t016ScenarioCanonicalSHA256 = "52e8fc8cd30d93df2fba087617358079372bf8a856d14c7386f7d2aa587b66c4"

func t016ScenarioSpecs() []t016ScenarioSpec {
	return []t016ScenarioSpec{
		{ID: "stream", Kind: "source_generic", Proof: "ordered_stream", Input: "argv:generic-worker --mode stream", Expected: "completed"},
		{ID: "flood", Kind: "source_generic", Proof: "bounded_flood", Input: "argv:generic-worker --mode flood --count 32 --chunk-bytes 256", Expected: "completed"},
		{ID: "byte_edge", Kind: "source_generic", Proof: "byte_exact", Input: "argv:generic-worker --mode framing", Expected: "completed"},
		{ID: "typed_input", Kind: "source_generic", Proof: "typed_input", Input: "argv:generic-worker --mode typed-input -- <argv-with-spaces-and-metachars>; stdin:binary", Expected: "completed"},
		{ID: "cancel", Kind: "source_generic", Proof: "termination", Input: "argv:generic-worker --mode tree --depth 2 --hold-ms 10000", Expected: "cancelled"},
		{ID: "timeout", Kind: "source_generic", Proof: "deadline", Input: "argv:generic-worker --mode tree --depth 2 --hold-ms 10000", Expected: "timeout"},
		{ID: "late", Kind: "runtime_fixture", Proof: "late_output", Input: "fixture:in-process-late-output", Expected: "failed"},
		{ID: "supplied_crash", Kind: "supplied_evidence", Proof: "exit_nonzero", Input: "evidence:exit-code=nonzero", Expected: "failed_crash"},
		{ID: "supplied_orphan", Kind: "supplied_evidence", Proof: "orphan_tree", Input: "evidence:root-absent+live-descendant", Expected: "orphaned_tree"},
		{ID: "invalid_input", Kind: "source_generic", Proof: "input_rejected", Input: "argv:generic-worker --mode invalid-input", Expected: "failed"},
		{ID: "oversize_input", Kind: "source_generic", Proof: "input_limit", Input: "argv:generic-worker --mode typed-input; stdin:>65536-bytes", Expected: "failed"},
		{ID: "source_built_zero_child_leak", Kind: "source_generic", Proof: "tree_liveness", Input: "argv:generic-worker --mode tree --depth 2 --hold-ms 10000", Expected: "cancelled"},
	}
}

func validateT016ScenarioSpecs(specs []t016ScenarioSpec) error {
	requiredIDs := []string{
		"stream",
		"flood",
		"byte_edge",
		"typed_input",
		"cancel",
		"timeout",
		"late",
		"supplied_crash",
		"supplied_orphan",
		"invalid_input",
		"oversize_input",
		"source_built_zero_child_leak",
	}
	proofByID := map[string]string{
		"stream":                       "ordered_stream",
		"flood":                        "bounded_flood",
		"byte_edge":                    "byte_exact",
		"typed_input":                  "typed_input",
		"cancel":                       "termination",
		"timeout":                      "deadline",
		"late":                         "late_output",
		"supplied_crash":               "exit_nonzero",
		"supplied_orphan":              "orphan_tree",
		"invalid_input":                "input_rejected",
		"oversize_input":               "input_limit",
		"source_built_zero_child_leak": "tree_liveness",
	}
	kindByID := map[string]string{
		"stream":                       "source_generic",
		"flood":                        "source_generic",
		"byte_edge":                    "source_generic",
		"typed_input":                  "source_generic",
		"cancel":                       "source_generic",
		"timeout":                      "source_generic",
		"late":                         "runtime_fixture",
		"supplied_crash":               "supplied_evidence",
		"supplied_orphan":              "supplied_evidence",
		"invalid_input":                "source_generic",
		"oversize_input":               "source_generic",
		"source_built_zero_child_leak": "source_generic",
	}
	inputByID := map[string]string{
		"stream":                       "argv:generic-worker --mode stream",
		"flood":                        "argv:generic-worker --mode flood --count 32 --chunk-bytes 256",
		"byte_edge":                    "argv:generic-worker --mode framing",
		"typed_input":                  "argv:generic-worker --mode typed-input -- <argv-with-spaces-and-metachars>; stdin:binary",
		"cancel":                       "argv:generic-worker --mode tree --depth 2 --hold-ms 10000",
		"timeout":                      "argv:generic-worker --mode tree --depth 2 --hold-ms 10000",
		"late":                         "fixture:in-process-late-output",
		"supplied_crash":               "evidence:exit-code=nonzero",
		"supplied_orphan":              "evidence:root-absent+live-descendant",
		"invalid_input":                "argv:generic-worker --mode invalid-input",
		"oversize_input":               "argv:generic-worker --mode typed-input; stdin:>65536-bytes",
		"source_built_zero_child_leak": "argv:generic-worker --mode tree --depth 2 --hold-ms 10000",
	}
	expectedByID := map[string]string{
		"stream":                       "completed",
		"flood":                        "completed",
		"byte_edge":                    "completed",
		"typed_input":                  "completed",
		"cancel":                       "cancelled",
		"timeout":                      "timeout",
		"late":                         "failed",
		"supplied_crash":               "failed_crash",
		"supplied_orphan":              "orphaned_tree",
		"invalid_input":                "failed",
		"oversize_input":               "failed",
		"source_built_zero_child_leak": "cancelled",
	}

	if len(specs) != len(requiredIDs) {
		return fmt.Errorf("scenario count: got %d, want %d", len(specs), len(requiredIDs))
	}

	seen := make(map[string]struct{}, len(specs))
	for i, spec := range specs {
		if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.Kind) == "" || strings.TrimSpace(spec.Proof) == "" || strings.TrimSpace(spec.Input) == "" || strings.TrimSpace(spec.Expected) == "" {
			return fmt.Errorf("blank scenario field at %d", i)
		}
		if _, ok := seen[spec.ID]; ok {
			return fmt.Errorf("duplicate scenario ID %q", spec.ID)
		}
		seen[spec.ID] = struct{}{}
		if spec.ID != requiredIDs[i] {
			return fmt.Errorf("scenario order at %d: got %q, want %q", i, spec.ID, requiredIDs[i])
		}
		if spec.Proof != proofByID[spec.ID] {
			return fmt.Errorf("proof for %q: got %q, want %q", spec.ID, spec.Proof, proofByID[spec.ID])
		}
		if spec.Kind != kindByID[spec.ID] {
			return fmt.Errorf("kind for %q: got %q, want %q", spec.ID, spec.Kind, kindByID[spec.ID])
		}
		if spec.Input != inputByID[spec.ID] {
			return fmt.Errorf("input for %q: got %q, want %q", spec.ID, spec.Input, inputByID[spec.ID])
		}
		if spec.Expected != expectedByID[spec.ID] {
			return fmt.Errorf("expected for %q: got %q, want %q", spec.ID, spec.Expected, expectedByID[spec.ID])
		}
	}

	encoded, err := json.Marshal(specs)
	if err != nil {
		return fmt.Errorf("canonical JSON: %w", err)
	}
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != t016ScenarioCanonicalSHA256 {
		return fmt.Errorf("canonical hash: got %x, want %s", digest, t016ScenarioCanonicalSHA256)
	}
	return nil
}

func TestT016ScenarioSpecs(t *testing.T) {
	specs := t016ScenarioSpecs()
	encoded, err := json.Marshal(specs)
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	digest := sha256.Sum256(encoded)
	t.Logf("T016 scenario manifest digest=%x", digest)
	if err := validateT016ScenarioSpecs(specs); err != nil {
		t.Fatalf("validateT016ScenarioSpecs() error = %v", err)
	}
}

func TestValidateT016ScenarioSpecsRejectsMutation(t *testing.T) {
	copySpecs := func() []t016ScenarioSpec {
		return append([]t016ScenarioSpec(nil), t016ScenarioSpecs()...)
	}

	tests := []struct {
		name   string
		mutate func([]t016ScenarioSpec) []t016ScenarioSpec
		want   string
	}{
		{
			name: "omission",
			mutate: func(specs []t016ScenarioSpec) []t016ScenarioSpec {
				return specs[:len(specs)-1]
			},
			want: "scenario count",
		},
		{
			name: "duplicate",
			mutate: func(specs []t016ScenarioSpec) []t016ScenarioSpec {
				specs[1].ID = specs[0].ID
				return specs
			},
			want: "duplicate",
		},
		{
			name: "proof swap",
			mutate: func(specs []t016ScenarioSpec) []t016ScenarioSpec {
				specs[0].Proof, specs[1].Proof = specs[1].Proof, specs[0].Proof
				return specs
			},
			want: "proof",
		},
		{
			name: "kind",
			mutate: func(specs []t016ScenarioSpec) []t016ScenarioSpec {
				specs[0].Kind = "wrong-kind"
				return specs
			},
			want: "kind",
		},
		{
			name: "input",
			mutate: func(specs []t016ScenarioSpec) []t016ScenarioSpec {
				specs[0].Input = "wrong-input"
				return specs
			},
			want: "input",
		},
		{
			name: "expected",
			mutate: func(specs []t016ScenarioSpec) []t016ScenarioSpec {
				specs[0].Expected = "wrong-expected"
				return specs
			},
			want: "expected",
		},
		{
			name: "order",
			mutate: func(specs []t016ScenarioSpec) []t016ScenarioSpec {
				specs[0], specs[1] = specs[1], specs[0]
				return specs
			},
			want: "scenario order",
		},
		{
			name: "blank field",
			mutate: func(specs []t016ScenarioSpec) []t016ScenarioSpec {
				specs[0].Input = ""
				return specs
			},
			want: "blank scenario field",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateT016ScenarioSpecs(test.mutate(copySpecs()))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateT016ScenarioSpecs() error = %v, want substring %q", err, test.want)
			}
		})
	}
}
