package skills

import (
	"testing"
)

func TestSkillFuncMap(t *testing.T) {
	t.Run("CallerHasSkill/present", func(t *testing.T) {
		data := &SkillData{CallerSkills: []string{"tdd", "review"}}
		fns := SkillFuncMap(data)
		fn := fns["CallerHasSkill"].(func(string) bool)
		if !fn("tdd") {
			t.Error("expected CallerHasSkill(\"tdd\") == true")
		}
	})

	t.Run("CallerHasSkill/case-insensitive", func(t *testing.T) {
		data := &SkillData{CallerSkills: []string{"tdd", "review"}}
		fns := SkillFuncMap(data)
		fn := fns["CallerHasSkill"].(func(string) bool)
		if !fn("TDD") {
			t.Error("expected CallerHasSkill(\"TDD\") == true (case-insensitive)")
		}
	})

	t.Run("CallerHasSkill/missing", func(t *testing.T) {
		data := &SkillData{CallerSkills: []string{"tdd", "review"}}
		fns := SkillFuncMap(data)
		fn := fns["CallerHasSkill"].(func(string) bool)
		if fn("missing") {
			t.Error("expected CallerHasSkill(\"missing\") == false")
		}
	})

	t.Run("JoinCLIs/multiple", func(t *testing.T) {
		data := &SkillData{EnabledCLIs: []string{"codex", "gemini"}}
		fns := SkillFuncMap(data)
		fn := fns["JoinCLIs"].(func() string)
		if got := fn(); got != "codex, gemini" {
			t.Errorf("JoinCLIs() = %q, want %q", got, "codex, gemini")
		}
	})

	t.Run("JoinCLIs/empty", func(t *testing.T) {
		data := &SkillData{EnabledCLIs: nil}
		fns := SkillFuncMap(data)
		fn := fns["JoinCLIs"].(func() string)
		if got := fn(); got != "" {
			t.Errorf("JoinCLIs() = %q, want %q", got, "")
		}
	})

	t.Run("RoleFor/found", func(t *testing.T) {
		data := &SkillData{RoleRouting: map[string]string{"coding": "codex"}}
		fns := SkillFuncMap(data)
		fn := fns["RoleFor"].(func(string) string)
		if got := fn("coding"); got != "codex" {
			t.Errorf("RoleFor(\"coding\") = %q, want %q", got, "codex")
		}
	})

	t.Run("RoleFor/not-found", func(t *testing.T) {
		data := &SkillData{RoleRouting: map[string]string{"coding": "codex"}}
		fns := SkillFuncMap(data)
		fn := fns["RoleFor"].(func(string) string)
		if got := fn("unknown"); got != "unknown" {
			t.Errorf("RoleFor(\"unknown\") = %q, want %q", got, "unknown")
		}
	})

	t.Run("HasCLI/found", func(t *testing.T) {
		data := &SkillData{EnabledCLIs: []string{"codex", "gemini"}}
		fns := SkillFuncMap(data)
		fn := fns["HasCLI"].(func(string) bool)
		if !fn("gemini") {
			t.Error("expected HasCLI(\"gemini\") == true")
		}
	})

	t.Run("HasCLI/missing", func(t *testing.T) {
		data := &SkillData{EnabledCLIs: []string{"codex", "gemini"}}
		fns := SkillFuncMap(data)
		fn := fns["HasCLI"].(func(string) bool)
		if fn("aider") {
			t.Error("expected HasCLI(\"aider\") == false")
		}
	})
}
