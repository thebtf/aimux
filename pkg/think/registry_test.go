package think

import "testing"

// stubPattern is a minimal PatternHandler for testing.
type stubPattern struct {
	name string
}

func (s *stubPattern) Name() string                    { return s.name }
func (s *stubPattern) Description() string             { return "stub" }
func (s *stubPattern) Validate(map[string]any) (map[string]any, error) {
	return nil, nil
}
func (s *stubPattern) Handle(map[string]any, string) (*ThinkResult, error) {
	return nil, nil
}

func TestRegisterAndGet(t *testing.T) {
	ClearPatterns()
	defer ClearPatterns()

	p := &stubPattern{name: "test_pattern"}
	RegisterPattern(p)

	got := GetPattern("test_pattern")
	if got == nil {
		t.Fatal("expected pattern, got nil")
	}
	if got.Name() != "test_pattern" {
		t.Errorf("name = %q, want %q", got.Name(), "test_pattern")
	}
}

func TestGetMissing(t *testing.T) {
	ClearPatterns()
	defer ClearPatterns()

	got := GetPattern("nonexistent")
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestGetAllPatterns(t *testing.T) {
	ClearPatterns()
	defer ClearPatterns()

	RegisterPattern(&stubPattern{name: "beta"})
	RegisterPattern(&stubPattern{name: "alpha"})

	all := GetAllPatterns()
	if len(all) != 2 {
		t.Fatalf("len = %d, want 2", len(all))
	}
	if all[0] != "alpha" || all[1] != "beta" {
		t.Errorf("patterns = %v, want [alpha beta]", all)
	}
}

func TestClearPatterns(t *testing.T) {
	ClearPatterns()
	RegisterPattern(&stubPattern{name: "x"})
	ClearPatterns()

	if len(GetAllPatterns()) != 0 {
		t.Error("expected empty after clear")
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	ClearPatterns()
	defer ClearPatterns()

	RegisterPattern(&stubPattern{name: "dup"})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	RegisterPattern(&stubPattern{name: "dup"})
}
