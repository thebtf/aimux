package classifier

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor/types"
)

func TestClassifyKnownPrompts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name:   "code",
			prompt: "Implement the Go handler in pkg/server/task_router.go and add tests for cancellation.",
			want:   TaskClassCode,
		},
		{
			name:   "review",
			prompt: "Review PR #152 diff against HEAD and block on security regressions.",
			want:   TaskClassReview,
		},
		{
			name:   "research",
			prompt: "Research the latest official documentation and compare sources for OAuth device flow.",
			want:   TaskClassResearch,
		},
		{
			name:   "spec",
			prompt: "Create a feature spec with requirements, user stories, and acceptance criteria.",
			want:   TaskClassSpec,
		},
		{
			name:   "prompt",
			prompt: "Rewrite this agent prompt into a clear delegation brief with system instructions.",
			want:   TaskClassPrompt,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			candidates, confidence, err := Classify(tc.prompt)
			if err != nil {
				t.Fatalf("Classify() error = %v, want nil", err)
			}
			if len(candidates) != len(taskClasses) {
				t.Fatalf("candidate count = %d, want %d", len(candidates), len(taskClasses))
			}
			if candidates[0].TaskClass != tc.want {
				t.Fatalf("top task_class = %s, want %s; candidates = %#v", candidates[0].TaskClass, tc.want, candidates)
			}
			if confidence != candidates[0].Score {
				t.Fatalf("confidence = %.3f, top score = %.3f", confidence, candidates[0].Score)
			}
			if confidence < DefaultThreshold {
				t.Fatalf("confidence = %.3f, want >= %.3f", confidence, DefaultThreshold)
			}
			assertSorted(t, candidates)
		})
	}
}

func TestClassifyAmbiguousPromptReturnsTopThreeAndCLIError(t *testing.T) {
	t.Parallel()

	candidates, confidence, err := Classify("Help me make this better.")
	if err == nil {
		t.Fatal("Classify() error = nil, want ClassificationAmbiguous")
	}

	var cliErr *types.CLIError
	if !errors.As(err, &cliErr) {
		t.Fatalf("error type = %T, want *types.CLIError", err)
	}
	if cliErr.Code != types.CLIErrorCodeClassificationAmbiguous {
		t.Fatalf("CLIError code = %s, want %s", cliErr.Code, types.CLIErrorCodeClassificationAmbiguous)
	}
	if cliErr.Retryable {
		t.Fatal("ClassificationAmbiguous retryable = true, want false")
	}
	if len(candidates) != 3 {
		t.Fatalf("candidate count = %d, want top 3", len(candidates))
	}
	if confidence >= DefaultThreshold {
		t.Fatalf("confidence = %.3f, want < %.3f", confidence, DefaultThreshold)
	}
	assertSorted(t, candidates)
}

func TestClassifierNilReceiverUsesDefaultThreshold(t *testing.T) {
	t.Parallel()

	var c *Classifier
	candidates, confidence, err := c.Classify("Implement a bug fix in pkg/server/task_router.go.")
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("candidates empty, want at least one")
	}
	if candidates[0].TaskClass != TaskClassCode {
		t.Fatalf("top task_class = %s, want %s", candidates[0].TaskClass, TaskClassCode)
	}
	if confidence < DefaultThreshold {
		t.Fatalf("confidence = %.3f, want >= %.3f", confidence, DefaultThreshold)
	}
}

func TestClassifyStructuralCuesAffectScores(t *testing.T) {
	t.Parallel()

	withPath, _, err := Classify("Fix pkg/server/task_tool.go and run go test ./pkg/server.")
	if err != nil {
		t.Fatalf("with path: %v", err)
	}

	withoutPath, _, err := Classify("Fix the routing behavior and run the relevant tests.")
	if err != nil {
		t.Fatalf("without path: %v", err)
	}

	withPathCode := scoreFor(withPath, TaskClassCode)
	withoutPathCode := scoreFor(withoutPath, TaskClassCode)
	if withPathCode <= withoutPathCode {
		t.Fatalf("code score with file path = %.3f, without path = %.3f; want file path to increase score", withPathCode, withoutPathCode)
	}
}

func TestDistinctFixturesProduceDistinctScores(t *testing.T) {
	t.Parallel()

	codeCandidates, _, err := Classify("Implement pkg/server/task_router.go cancellation handling and add tests.")
	if err != nil {
		t.Fatalf("code fixture: %v", err)
	}
	reviewCandidates, _, err := Classify("Review this PR diff for regressions and blocking findings.")
	if err != nil {
		t.Fatalf("review fixture: %v", err)
	}
	researchCandidates, _, err := Classify("Research official docs and compare current source evidence.")
	if err != nil {
		t.Fatalf("research fixture: %v", err)
	}

	if reflect.DeepEqual(scoreVector(codeCandidates), scoreVector(reviewCandidates)) ||
		reflect.DeepEqual(scoreVector(codeCandidates), scoreVector(researchCandidates)) ||
		reflect.DeepEqual(scoreVector(reviewCandidates), scoreVector(researchCandidates)) {
		t.Fatalf("distinct fixtures produced identical score vectors: code=%#v review=%#v research=%#v", codeCandidates, reviewCandidates, researchCandidates)
	}
}

func TestClassifyLatencyP95UnderBudget(t *testing.T) {
	t.Parallel()

	prompt := thousandTokenPrompt()
	durations := make([]time.Duration, 200)
	for i := range durations {
		start := time.Now()
		if _, _, err := Classify(prompt); err != nil {
			t.Fatalf("Classify() error = %v", err)
		}
		durations[i] = time.Since(start)
	}

	sortDurations(durations)
	p95 := durations[int(float64(len(durations))*0.95)-1]
	if p95 > 50*time.Millisecond {
		t.Fatalf("p95 latency = %s, want <= 50ms", p95)
	}
}

func BenchmarkClassify1000TokenPrompt(b *testing.B) {
	prompt := thousandTokenPrompt()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := Classify(prompt); err != nil {
			b.Fatal(err)
		}
	}
}

func assertSorted(t *testing.T, candidates []Candidate) {
	t.Helper()
	for i := 1; i < len(candidates); i++ {
		if candidates[i-1].Score < candidates[i].Score {
			t.Fatalf("candidates not sorted at %d: %#v", i, candidates)
		}
	}
}

func scoreFor(candidates []Candidate, taskClass string) float64 {
	for _, candidate := range candidates {
		if candidate.TaskClass == taskClass {
			return candidate.Score
		}
	}
	return 0
}

func scoreVector(candidates []Candidate) map[string]float64 {
	out := make(map[string]float64, len(candidates))
	for _, candidate := range candidates {
		out[candidate.TaskClass] = candidate.Score
	}
	return out
}

func thousandTokenPrompt() string {
	terms := []string{
		"implement", "pkg/server/task_router.go", "tests", "handler", "cancellation",
		"context", "go", "build", "regression", "fixture",
	}
	var b strings.Builder
	for i := 0; i < 100; i++ {
		for _, term := range terms {
			b.WriteString(term)
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func sortDurations(values []time.Duration) {
	for i := 1; i < len(values); i++ {
		v := values[i]
		j := i - 1
		for j >= 0 && values[j] > v {
			values[j+1] = values[j]
			j--
		}
		values[j+1] = v
	}
}
