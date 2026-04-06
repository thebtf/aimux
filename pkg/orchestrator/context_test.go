package orchestrator

import (
	"strings"
	"testing"
)

func TestComputeDialogBudget(t *testing.T) {
	t.Run("empty slice uses default", func(t *testing.T) {
		got := ComputeDialogBudget(nil)
		want := int(float64(DefaultContextWindow) * SafetyFactor * CharsPerToken)
		if got != want {
			t.Errorf("ComputeDialogBudget(nil) = %d, want %d", got, want)
		}
	})

	t.Run("empty slice explicit", func(t *testing.T) {
		got := ComputeDialogBudget([]int{})
		want := int(float64(DefaultContextWindow) * SafetyFactor * CharsPerToken)
		if got != want {
			t.Errorf("ComputeDialogBudget([]) = %d, want %d", got, want)
		}
	})

	t.Run("single value", func(t *testing.T) {
		got := ComputeDialogBudget([]int{100000})
		want := int(float64(100000) * SafetyFactor * CharsPerToken)
		if got != want {
			t.Errorf("ComputeDialogBudget([100000]) = %d, want %d", got, want)
		}
	})

	t.Run("multiple values takes min", func(t *testing.T) {
		got := ComputeDialogBudget([]int{200000, 100000, 150000})
		want := int(float64(100000) * SafetyFactor * CharsPerToken)
		if got != want {
			t.Errorf("ComputeDialogBudget([200000,100000,150000]) = %d, want %d", got, want)
		}
	})

	t.Run("zero value uses default", func(t *testing.T) {
		got := ComputeDialogBudget([]int{0, 50000})
		want := int(float64(50000) * SafetyFactor * CharsPerToken)
		if got != want {
			t.Errorf("ComputeDialogBudget([0,50000]) = %d, want %d", got, want)
		}
	})

	t.Run("negative value uses default", func(t *testing.T) {
		got := ComputeDialogBudget([]int{-1})
		want := int(float64(DefaultContextWindow) * SafetyFactor * CharsPerToken)
		if got != want {
			t.Errorf("ComputeDialogBudget([-1]) = %d, want %d", got, want)
		}
	})

	t.Run("all zero uses default for all", func(t *testing.T) {
		got := ComputeDialogBudget([]int{0, 0})
		want := int(float64(DefaultContextWindow) * SafetyFactor * CharsPerToken)
		if got != want {
			t.Errorf("ComputeDialogBudget([0,0]) = %d, want %d", got, want)
		}
	})
}

func TestCompactTurnContent(t *testing.T) {
	t.Run("blank line collapse", func(t *testing.T) {
		input := "line1\n\n\n\nline2\n\n\n\n\nline3"
		got := CompactTurnContent(input, 0)
		if strings.Contains(got, "\n\n\n") {
			t.Errorf("expected consecutive blank lines collapsed, got: %q", got)
		}
		if !strings.Contains(got, "line1\n\nline2\n\nline3") {
			t.Errorf("expected collapsed content, got: %q", got)
		}
	})

	t.Run("trailing whitespace strip", func(t *testing.T) {
		input := "line1   \nline2\t\t\nline3  \t "
		got := CompactTurnContent(input, 0)
		lines := strings.Split(got, "\n")
		for i, line := range lines {
			trimmed := strings.TrimRight(line, " \t")
			if line != trimmed {
				t.Errorf("line %d has trailing whitespace: %q", i, line)
			}
		}
	})

	t.Run("under limit no truncation", func(t *testing.T) {
		input := "short content"
		got := CompactTurnContent(input, 1000)
		if got != input {
			t.Errorf("expected %q, got %q", input, got)
		}
	})

	t.Run("over limit paragraph truncation", func(t *testing.T) {
		input := "first paragraph\n\nsecond paragraph\n\nthird paragraph that is much longer"
		got := CompactTurnContent(input, 40)
		if !strings.Contains(got, "... [truncated]") {
			t.Errorf("expected truncation marker, got: %q", got)
		}
		if strings.Contains(got, "third") {
			t.Errorf("expected third paragraph removed, got: %q", got)
		}
	})

	t.Run("over limit space truncation no paragraphs", func(t *testing.T) {
		input := "word1 word2 word3 word4 word5 word6 word7 word8 word9"
		got := CompactTurnContent(input, 25)
		if !strings.Contains(got, "... [truncated]") {
			t.Errorf("expected truncation marker, got: %q", got)
		}
	})

	t.Run("default maxChars when zero", func(t *testing.T) {
		input := "small"
		got := CompactTurnContent(input, 0)
		if got != input {
			t.Errorf("expected no truncation with default maxChars, got: %q", got)
		}
	})
}

func TestExtractSummary(t *testing.T) {
	t.Run("single paragraph as-is", func(t *testing.T) {
		input := "just one paragraph"
		got := ExtractSummary(input)
		if got != input {
			t.Errorf("expected %q, got %q", input, got)
		}
	})

	t.Run("two paragraphs as-is", func(t *testing.T) {
		input := "first paragraph\n\nsecond paragraph"
		got := ExtractSummary(input)
		if got != input {
			t.Errorf("expected %q, got %q", input, got)
		}
	})

	t.Run("three paragraphs returns first and last", func(t *testing.T) {
		input := "first paragraph\n\nmiddle paragraph\n\nlast paragraph"
		got := ExtractSummary(input)
		want := "first paragraph\n\n...\n\nlast paragraph"
		if got != want {
			t.Errorf("expected %q, got %q", want, got)
		}
	})

	t.Run("four paragraphs returns first and last", func(t *testing.T) {
		input := "first\n\nsecond\n\nthird\n\nfourth"
		got := ExtractSummary(input)
		want := "first\n\n...\n\nfourth"
		if got != want {
			t.Errorf("expected %q, got %q", want, got)
		}
	})
}

func TestBuildDialogContext(t *testing.T) {
	t.Run("empty turns", func(t *testing.T) {
		got := BuildDialogContext(nil, 10000)
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("within budget all turns full", func(t *testing.T) {
		turns := []turnEntry{
			{CLI: "claude", Content: "hello", Turn: 1},
			{CLI: "gemini", Content: "world", Turn: 2},
		}
		got := BuildDialogContext(turns, 100000)
		if !strings.Contains(got, "[Turn 1 - claude]") {
			t.Errorf("expected turn 1 header, got: %q", got)
		}
		if !strings.Contains(got, "[Turn 2 - gemini]") {
			t.Errorf("expected turn 2 header, got: %q", got)
		}
		if !strings.Contains(got, "hello") {
			t.Errorf("expected turn 1 content, got: %q", got)
		}
		if !strings.Contains(got, "world") {
			t.Errorf("expected turn 2 content, got: %q", got)
		}
		// Verify chronological order
		idx1 := strings.Index(got, "[Turn 1")
		idx2 := strings.Index(got, "[Turn 2")
		if idx1 >= idx2 {
			t.Errorf("expected chronological order, turn 1 at %d, turn 2 at %d", idx1, idx2)
		}
	})

	t.Run("over budget older turns summarized", func(t *testing.T) {
		longContent := "first paragraph\n\nmiddle details that are quite verbose and lengthy\n\nlast paragraph"
		turns := []turnEntry{
			{CLI: "claude", Content: longContent, Turn: 1},
			{CLI: "gemini", Content: "recent1", Turn: 2},
			{CLI: "claude", Content: "recent2", Turn: 3},
		}
		got := BuildDialogContext(turns, 100000)

		// Turn 1 (older) should be summarized - middle paragraph removed
		if strings.Contains(got, "middle details") {
			t.Errorf("expected older turn to be summarized (middle removed), got: %q", got)
		}
		// Turn 1 should still have first and last paragraphs
		if !strings.Contains(got, "first paragraph") {
			t.Errorf("expected first paragraph of older turn, got: %q", got)
		}
		if !strings.Contains(got, "last paragraph") {
			t.Errorf("expected last paragraph of older turn, got: %q", got)
		}
		// Recent turns should be full
		if !strings.Contains(got, "recent1") {
			t.Errorf("expected recent turn 2 content, got: %q", got)
		}
		if !strings.Contains(got, "recent2") {
			t.Errorf("expected recent turn 3 content, got: %q", got)
		}
	})

	t.Run("budget limits number of turns", func(t *testing.T) {
		turns := []turnEntry{
			{CLI: "claude", Content: "old content", Turn: 1},
			{CLI: "gemini", Content: "recent1", Turn: 2},
			{CLI: "claude", Content: "recent2", Turn: 3},
		}
		// Budget so small only last 2 turns fit
		got := BuildDialogContext(turns, 60)
		if strings.Contains(got, "old content") {
			t.Errorf("expected old turn excluded due to budget, got: %q", got)
		}
	})
}

func TestBuildSynthesisPrompt(t *testing.T) {
	t.Run("within budget", func(t *testing.T) {
		responses := []string{"answer one", "answer two"}
		got := BuildSynthesisPrompt("testing", responses, 100000)
		if !strings.Contains(got, "Synthesize the following responses about: testing") {
			t.Errorf("expected header, got: %q", got)
		}
		if !strings.Contains(got, "## Response 1\nanswer one") {
			t.Errorf("expected response 1, got: %q", got)
		}
		if !strings.Contains(got, "## Response 2\nanswer two") {
			t.Errorf("expected response 2, got: %q", got)
		}
	})

	t.Run("over budget proportional truncation", func(t *testing.T) {
		r1 := strings.Repeat("a ", 500)
		r2 := strings.Repeat("b ", 500)
		got := BuildSynthesisPrompt("topic", []string{r1, r2}, 200)
		if !strings.Contains(got, "## Response 1") {
			t.Errorf("expected response 1 header, got: %q", got)
		}
		if !strings.Contains(got, "## Response 2") {
			t.Errorf("expected response 2 header, got: %q", got)
		}
		// Both should be truncated
		if strings.Contains(got, r1) {
			t.Errorf("expected response 1 truncated")
		}
		if strings.Contains(got, r2) {
			t.Errorf("expected response 2 truncated")
		}
	})

	t.Run("single response", func(t *testing.T) {
		got := BuildSynthesisPrompt("topic", []string{"only response"}, 100000)
		if !strings.Contains(got, "## Response 1\nonly response") {
			t.Errorf("expected single response, got: %q", got)
		}
		if strings.Contains(got, "## Response 2") {
			t.Errorf("unexpected response 2, got: %q", got)
		}
	})

	t.Run("empty responses", func(t *testing.T) {
		got := BuildSynthesisPrompt("topic", []string{}, 100000)
		if !strings.Contains(got, "Synthesize the following responses about: topic") {
			t.Errorf("expected header only, got: %q", got)
		}
	})
}
