package picker

import "testing"

func TestCapabilityScore_DefaultTable(t *testing.T) {
	cfg := DefaultPickerConfig()
	cs := NewCapabilityScore(&cfg)

	tests := []struct {
		cli       string
		taskClass string
		want      int
	}{
		// codex: strongest for code/review/write-task
		{"codex", "code", 95},
		{"codex", "review", 90},
		{"codex", "write-task", 85},
		{"codex", "task", 80},
		{"codex", "research", 40},
		// claude: balanced generalist
		{"claude", "task", 85},
		{"claude", "research", 80},
		{"claude", "code", 80},
		{"claude", "review", 70},
		{"claude", "write-task", 70},
		// gemini: strongest for research
		{"gemini", "research", 90},
		{"gemini", "task", 60},
		{"gemini", "code", 60},
		{"gemini", "review", 50},
		{"gemini", "write-task", 40},
	}

	for _, tc := range tests {
		got := cs.Score(tc.cli, tc.taskClass)
		if got != tc.want {
			t.Errorf("Score(%q, %q) = %d, want %d", tc.cli, tc.taskClass, got, tc.want)
		}
	}
}

func TestCapabilityScore_ConfigOverride(t *testing.T) {
	cfg := DefaultPickerConfig()
	cfg.Scores = map[string]map[string]int{
		"codex": {"research": 99},
	}
	cs := NewCapabilityScore(&cfg)

	// Override takes precedence.
	if got := cs.Score("codex", "research"); got != 99 {
		t.Errorf("Score(codex, research) with override = %d, want 99", got)
	}
	// Non-overridden still uses built-in default.
	if got := cs.Score("codex", "code"); got != 95 {
		t.Errorf("Score(codex, code) without override = %d, want 95", got)
	}
}

func TestCapabilityScore_UnknownTaskClass(t *testing.T) {
	cfg := DefaultPickerConfig()
	cs := NewCapabilityScore(&cfg)

	// Unknown task class → default score 50 for any CLI.
	for _, cli := range []string{"codex", "claude", "gemini"} {
		got := cs.Score(cli, "unknown-task-class")
		if got != defaultScoreForUnknown {
			t.Errorf("Score(%q, unknown-task-class) = %d, want %d", cli, got, defaultScoreForUnknown)
		}
	}
}

func TestCapabilityScore_UnknownCLI(t *testing.T) {
	cfg := DefaultPickerConfig()
	cs := NewCapabilityScore(&cfg)

	got := cs.Score("nonexistent-cli", "code")
	if got != defaultScoreForUnknown {
		t.Errorf("Score(nonexistent-cli, code) = %d, want %d", got, defaultScoreForUnknown)
	}
}

func TestCapabilityScore_Scoref_Boundaries(t *testing.T) {
	cfg := DefaultPickerConfig()

	tests := []struct {
		rawScore int
		want     float64
	}{
		{0, 0.0},
		{50, 0.5},
		{95, 0.95},
		{100, 1.0},
	}

	for _, tc := range tests {
		// Inject override so we can drive arbitrary raw scores.
		cfg.Scores = map[string]map[string]int{
			"codex": {"code": tc.rawScore},
		}
		cs := NewCapabilityScore(&cfg)
		got := cs.Scoref("codex", "code")
		if got != tc.want {
			t.Errorf("Scoref for Score=%d: got %v, want %v", tc.rawScore, got, tc.want)
		}
	}
}

func TestCapabilityScore_Scoref_MatchesScore(t *testing.T) {
	cfg := DefaultPickerConfig()
	cs := NewCapabilityScore(&cfg)

	clis := []string{"codex", "claude", "gemini"}
	taskClasses := []string{"code", "review", "write-task", "task", "research"}

	for _, cli := range clis {
		for _, task := range taskClasses {
			intScore := cs.Score(cli, task)
			want := float64(intScore) / 100.0
			got := cs.Scoref(cli, task)
			if got != want {
				t.Errorf("Scoref(%q, %q) = %v, want %v (from Score=%d)", cli, task, got, want, intScore)
			}
		}
	}
}
