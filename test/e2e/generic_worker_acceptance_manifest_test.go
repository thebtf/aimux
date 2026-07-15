package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type t016ScenarioInput struct {
	Args                []string `json:"args"`
	StdinBase64         string   `json:"stdin_base64"`
	StdinRepeatByte     int      `json:"stdin_repeat_byte"`
	StdinBytes          int      `json:"stdin_bytes"`
	TimeoutSeconds      int      `json:"timeout_seconds"`
	Fixture             string   `json:"fixture"`
	ExitCode            *int     `json:"exit_code"`
	RootAbsent          bool     `json:"root_absent"`
	DescendantsSurvived bool     `json:"descendants_survived"`
}

type t016ScenarioSpec struct {
	ID       string
	Kind     string
	Proof    string
	Input    t016ScenarioInput
	Expected string
}

func t016Int(value int) *int { return &value }

const t016ScenarioCanonicalSHA256 = "55ce5916f4842cdb69c089589c56dfd8ea3ed26b4c4d11224e8b4b3f063e8f23"

func t016ScenarioSpecs() []t016ScenarioSpec {
	treeArgs := []string{"generic-worker", "--mode", "tree", "--depth", "2", "--hold-ms", "10000"}
	return []t016ScenarioSpec{
		{ID: "stream", Kind: "source_generic", Proof: "ordered_stream", Input: t016ScenarioInput{Args: []string{"generic-worker", "--mode", "stream"}}, Expected: "completed"},
		{ID: "flood", Kind: "source_generic", Proof: "bounded_flood", Input: t016ScenarioInput{Args: []string{"generic-worker", "--mode", "flood", "--count", "32", "--chunk-bytes", "256"}}, Expected: "completed"},
		{ID: "byte_edge", Kind: "source_generic", Proof: "byte_exact", Input: t016ScenarioInput{Args: []string{"generic-worker", "--mode", "framing"}}, Expected: "completed"},
		{ID: "typed_input", Kind: "source_generic", Proof: "typed_input", Input: t016ScenarioInput{Args: []string{"generic-worker", "--mode", "typed-input", "--", "space value", "$HOME; & | < >", "quote\"'\\", "unicode-β"}, StdinBase64: "AP8bQQrOsg==", StdinBytes: 7}, Expected: "completed"},
		{ID: "cancel", Kind: "source_generic", Proof: "termination", Input: t016ScenarioInput{Args: treeArgs}, Expected: "cancelled"},
		{ID: "timeout", Kind: "source_generic", Proof: "deadline", Input: t016ScenarioInput{Args: treeArgs, TimeoutSeconds: 3}, Expected: "timeout"},
		{ID: "late", Kind: "runtime_fixture", Proof: "late_output", Input: t016ScenarioInput{Fixture: "in-process-late-output"}, Expected: "failed"},
		{ID: "supplied_crash", Kind: "supplied_evidence", Proof: "exit_nonzero", Input: t016ScenarioInput{ExitCode: t016Int(23)}, Expected: "failed_crash"},
		{ID: "supplied_orphan", Kind: "supplied_evidence", Proof: "orphan_tree", Input: t016ScenarioInput{RootAbsent: true, DescendantsSurvived: true}, Expected: "orphaned_tree"},
		{ID: "invalid_input", Kind: "source_generic", Proof: "input_rejected", Input: t016ScenarioInput{Args: []string{"generic-worker", "--mode", "invalid-input"}}, Expected: "failed"},
		{ID: "oversize_input", Kind: "source_generic", Proof: "input_limit", Input: t016ScenarioInput{Args: []string{"generic-worker", "--mode", "typed-input"}, StdinRepeatByte: 0xa5, StdinBytes: 65537}, Expected: "failed"},
		{ID: "source_built_zero_child_leak", Kind: "source_generic", Proof: "tree_liveness", Input: t016ScenarioInput{Args: treeArgs}, Expected: "cancelled"},
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
	inputByID := map[string]t016ScenarioInput{
		"stream":                       {Args: []string{"generic-worker", "--mode", "stream"}},
		"flood":                        {Args: []string{"generic-worker", "--mode", "flood", "--count", "32", "--chunk-bytes", "256"}},
		"byte_edge":                    {Args: []string{"generic-worker", "--mode", "framing"}},
		"typed_input":                  {Args: []string{"generic-worker", "--mode", "typed-input", "--", "space value", "$HOME; & | < >", "quote\"'\\", "unicode-β"}, StdinBase64: "AP8bQQrOsg==", StdinBytes: 7},
		"cancel":                       {Args: []string{"generic-worker", "--mode", "tree", "--depth", "2", "--hold-ms", "10000"}},
		"timeout":                      {Args: []string{"generic-worker", "--mode", "tree", "--depth", "2", "--hold-ms", "10000"}, TimeoutSeconds: 3},
		"late":                         {Fixture: "in-process-late-output"},
		"supplied_crash":               {ExitCode: t016Int(23)},
		"supplied_orphan":              {RootAbsent: true, DescendantsSurvived: true},
		"invalid_input":                {Args: []string{"generic-worker", "--mode", "invalid-input"}},
		"oversize_input":               {Args: []string{"generic-worker", "--mode", "typed-input"}, StdinRepeatByte: 0xa5, StdinBytes: 65537},
		"source_built_zero_child_leak": {Args: []string{"generic-worker", "--mode", "tree", "--depth", "2", "--hold-ms", "10000"}},
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
		if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.Kind) == "" || strings.TrimSpace(spec.Proof) == "" || reflect.DeepEqual(spec.Input, t016ScenarioInput{}) || strings.TrimSpace(spec.Expected) == "" {
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
		if !reflect.DeepEqual(spec.Input, inputByID[spec.ID]) {
			return fmt.Errorf("input for %q: got %#v, want %#v", spec.ID, spec.Input, inputByID[spec.ID])
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
		specs := append([]t016ScenarioSpec(nil), t016ScenarioSpecs()...)
		for i := range specs {
			specs[i].Input.Args = append([]string(nil), specs[i].Input.Args...)
			if specs[i].Input.ExitCode != nil {
				exitCode := *specs[i].Input.ExitCode
				specs[i].Input.ExitCode = &exitCode
			}
		}
		return specs
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
				specs[0].Input.Args[0] = "wrong-command"
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
				specs[0].Input = t016ScenarioInput{}
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
