package think

import "testing"

func TestGetDialogConfig_Known(t *testing.T) {
	cfg := GetDialogConfig("critical_thinking")
	if cfg == nil {
		t.Fatal("expected config for critical_thinking")
	}
	if cfg.ComplexityBias != 10 {
		t.Errorf("bias = %d, want 10", cfg.ComplexityBias)
	}
	if len(cfg.Participants) != 2 {
		t.Errorf("participants = %d, want 2", len(cfg.Participants))
	}
}

func TestGetDialogConfig_SoloOnly(t *testing.T) {
	cfg := GetDialogConfig("think")
	if cfg != nil {
		t.Error("expected nil for solo-only pattern")
	}

	cfg = GetDialogConfig("sequential_thinking")
	if cfg != nil {
		t.Error("expected nil for sequential_thinking")
	}
}

func TestGetDialogConfig_Unknown(t *testing.T) {
	cfg := GetDialogConfig("nonexistent")
	if cfg != nil {
		t.Error("expected nil for unknown pattern")
	}
}

func TestBuildDialogTopic(t *testing.T) {
	cfg := GetDialogConfig("critical_thinking")
	topic := BuildDialogTopic(cfg, map[string]any{"issue": "memory leak"})
	if topic != "Critical analysis: memory leak" {
		t.Errorf("topic = %q", topic)
	}
}

func TestBuildPatternDialogPrompt(t *testing.T) {
	cfg := GetDialogConfig("mental_model")
	prompt := BuildPatternDialogPrompt(cfg, map[string]any{
		"modelName": "inversion",
		"problem":   "test problem",
		"role":      "Skeptic",
	})
	if prompt == cfg.PromptTemplate {
		t.Error("template was not interpolated")
	}
}

func TestGetDialogPatterns(t *testing.T) {
	patterns := GetDialogPatterns()
	if len(patterns) != 12 {
		t.Errorf("dialog patterns count = %d, want 12", len(patterns))
	}
}

func TestFilterParticipants_AllAllowed(t *testing.T) {
	participants := []DialogParticipant{
		{CLI: "codex", Role: "A"},
		{CLI: "gemini", Role: "B"},
	}
	filtered, warnings := FilterParticipants(participants, ThinkConsensusCLIs)
	if len(filtered) != 2 {
		t.Errorf("filtered = %d, want 2", len(filtered))
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %d, want 0", len(warnings))
	}
}

func TestFilterParticipants_SomeFiltered(t *testing.T) {
	participants := []DialogParticipant{
		{CLI: "codex", Role: "A"},
		{CLI: "aider", Role: "B"},
		{CLI: "gemini", Role: "C"},
	}
	filtered, warnings := FilterParticipants(participants, ThinkConsensusCLIs)
	if len(filtered) != 2 {
		t.Errorf("filtered = %d, want 2", len(filtered))
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %d, want 1", len(warnings))
	}
	if filtered[0].CLI != "codex" || filtered[1].CLI != "gemini" {
		t.Errorf("expected codex+gemini, got %s+%s", filtered[0].CLI, filtered[1].CLI)
	}
}

func TestFilterParticipants_AllFiltered(t *testing.T) {
	participants := []DialogParticipant{
		{CLI: "aider", Role: "A"},
		{CLI: "goose", Role: "B"},
	}
	filtered, warnings := FilterParticipants(participants, ThinkConsensusCLIs)
	if len(filtered) != 0 {
		t.Errorf("filtered = %d, want 0", len(filtered))
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %d, want 2", len(warnings))
	}
}
